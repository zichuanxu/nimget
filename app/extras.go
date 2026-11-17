package main

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/amemiya02/nimget/internal/batch"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/power"
	"github.com/amemiya02/nimget/internal/queuefile"
	"github.com/amemiya02/nimget/internal/remux"
)

// eventQueueDone carries the action to run once the queue has emptied; the
// frontend shows a countdown and then calls PerformWhenDone.
const eventQueueDone = "queue-done"

func init() {
	application.RegisterEvent[string](eventQueueDone)
}

// CanConvert reports whether ffmpeg is available for ConvertToMP4.
func (d *Desktop) CanConvert() bool {
	m, err := d.manager()
	if err != nil {
		return false
	}
	_, err = remux.Find(m.Settings().Desktop.FFmpeg)
	return err == nil
}

// ConvertToMP4 remuxes a completed MPEG-TS download into an MP4 next to it,
// without re-encoding, and returns the new file's path.
func (d *Desktop) ConvertToMP4(id string) (string, error) {
	item, err := d.item(id)
	if err != nil {
		return "", err
	}
	if item.Status != download.StatusCompleted || !strings.EqualFold(filepath.Ext(item.Destination), ".ts") {
		return "", errors.New("only completed .ts downloads can be converted")
	}
	ffmpeg, err := remux.Find(d.mgr.Settings().Desktop.FFmpeg)
	if err != nil {
		return "", err
	}
	out, err := remux.ToMP4(context.Background(), ffmpeg, item.Destination)
	if err != nil {
		d.log.Warn("convert to MP4", "id", id, "err", err)
	}
	return out, err
}

// ExpandBatch returns the URLs a batch pattern such as img[001-120].jpg
// describes.
func (d *Desktop) ExpandBatch(pattern string) ([]string, error) {
	return batch.Expand(pattern)
}

// ExportQueue saves a summary of unfinished tasks to a file the user picks.
func (d *Desktop) ExportQueue() (string, error) {
	return d.exportQueue("nimget-queue-summary.json", queuefile.ExportSummary)
}

// BackupQueue saves exact source URLs and queue options without headers or sessions.
func (d *Desktop) BackupQueue() (string, error) {
	return d.exportQueue("nimget-source-backup.json", queuefile.ExportSources)
}

func (d *Desktop) exportQueue(filename string, encode func([]download.Item) ([]byte, error)) (string, error) {
	m, err := d.manager()
	if err != nil {
		return "", err
	}
	path, err := d.app.Dialog.SaveFile().SetFilename(filename).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	items, err := m.List(context.Background())
	if err != nil {
		return "", err
	}
	data, err := encode(items)
	if err != nil {
		return "", err
	}
	if err := queuefile.Write(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// ImportResult reports an import.
type ImportResult struct {
	Added    int    `json:"added"`
	Failed   []int  `json:"failed"`
	Problem  string `json:"problem,omitempty"`
	LegacyID string `json:"legacyId,omitempty"`
}

// ImportQueue queues the downloads in an exported file or a list of URLs the
// user picks.
func (d *Desktop) ImportQueue() (ImportResult, error) {
	m, err := d.manager()
	if err != nil {
		return ImportResult{}, err
	}
	d.legacyQueueImport.Store(nil)
	path, err := d.app.Dialog.OpenFile().CanChooseFiles(true).PromptForSingleSelection()
	if err != nil || path == "" {
		return ImportResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ImportResult{Problem: "unreadable"}, nil
	}
	reqs, err := queuefile.Import(data)
	if errors.Is(err, queuefile.ErrLegacyURLs) {
		pending := &pendingQueueImport{id: rand.Text(), data: data}
		d.legacyQueueImport.Store(pending)
		return ImportResult{LegacyID: pending.id}, nil
	}
	if errors.Is(err, queuefile.ErrSummary) {
		return ImportResult{Problem: "summary"}, nil
	}
	if err != nil {
		return ImportResult{Problem: "invalid"}, nil
	}
	return addImported(context.Background(), reqs, m.Add), nil
}

type pendingQueueImport struct {
	id   string
	data []byte
}

// FinishLegacyImport consumes the selected file after the user makes a choice.
func (d *Desktop) FinishLegacyImport(id string, confirm bool) (ImportResult, error) {
	pending := d.legacyQueueImport.Load()
	if pending == nil || pending.id != id || !d.legacyQueueImport.CompareAndSwap(pending, nil) {
		return ImportResult{Problem: "expired"}, nil
	}
	if !confirm {
		return ImportResult{}, nil
	}
	m, err := d.manager()
	if err != nil {
		return ImportResult{}, err
	}
	reqs, err := queuefile.ImportLegacyURLs(pending.data)
	if err != nil {
		return ImportResult{Problem: "invalid"}, nil
	}
	return addImported(context.Background(), reqs, m.Add), nil
}

func addImported(ctx context.Context, reqs []download.Request, add func(context.Context, download.Request) (download.Item, error)) ImportResult {
	res := ImportResult{Failed: []int{}}
	for i, req := range reqs {
		if _, err := add(ctx, req); err != nil {
			res.Failed = append(res.Failed, i+1)
			continue
		}
		res.Added++
	}
	return res
}

// SetWhenDone chooses what happens once no download is queued or running:
// "" for nothing, "sleep", or "shutdown". It fires once and then resets.
func (d *Desktop) SetWhenDone(action string) error {
	switch power.Action(action) {
	case "", power.Sleep, power.Shutdown:
	default:
		return errors.New("unknown action " + action)
	}
	if err := power.Prepare(power.Action(action)); err != nil {
		return err
	}
	d.whenDone.Store(action)
	return nil
}

// WhenDone returns the pending action.
func (d *Desktop) WhenDone() string {
	action, _ := d.whenDone.Load().(string)
	return action
}

// PerformWhenDone runs action after the frontend's countdown, unless it was
// cancelled meanwhile.
func (d *Desktop) PerformWhenDone(action string) error {
	// Downloads may have been added during the countdown.
	items, err := d.List()
	if err != nil {
		return err
	}
	if !drained(items) {
		return errors.New("downloads were added during the countdown; the action stays pending")
	}
	if action == "" || !d.whenDone.CompareAndSwap(action, "") {
		return errors.New("the action was cancelled")
	}
	d.log.Info("queue done", "action", action)
	return power.Do(power.Action(action))
}

// afterStop runs when a download stops: it opens completed files if asked to
// and, once nothing is queued or running, announces the pending action.
func (d *Desktop) afterStop(item download.Item) {
	var err error
	// Pausing ("Pause all") is not finishing.
	if item.Status != download.StatusCompleted && item.Status != download.StatusFailed {
		return
	}
	if item.Status == download.StatusCompleted && d.mgr.Settings().Desktop.OpenWhenDone {
		// The server picks the name; only passive files open on their own.
		if passive(item.Destination) {
			err = d.app.Browser.OpenFile(item.Destination)
		} else {
			err = d.app.Env.OpenFileManager(item.Destination, true)
		}
		if err != nil {
			d.log.Warn("open completed download", "err", err)
		}
	}
	action := d.WhenDone()
	if action == "" {
		return
	}
	items, err := d.mgr.List(context.Background())
	if err != nil || !drained(items) {
		return
	}
	d.show()
	d.app.Event.Emit(eventQueueDone, action)
}

// passiveTypes open with a viewer, never as a program.
var passiveTypes = map[string]bool{
	".mp4": true, ".mkv": true, ".webm": true, ".mov": true, ".avi": true, ".m4v": true, ".ts": true,
	".mp3": true, ".flac": true, ".wav": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true,
	".pdf": true, ".txt": true, ".epub": true,
}

func passive(path string) bool { return passiveTypes[strings.ToLower(filepath.Ext(path))] }

// drained reports that no download is waiting or running.
func drained(items []download.Item) bool {
	for _, item := range items {
		if item.Status == download.StatusQueued || item.Status == download.StatusRunning {
			return false
		}
	}
	return true
}
