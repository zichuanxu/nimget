// Package queuefile exports the queue to a portable JSON file and imports it,
// or a plain list of URLs, back.
package queuefile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/amemiya02/nimget/internal/download"
)

const (
	version     = 2
	summaryMode = "summary"
	backupMode  = "source-backup"
)

var (
	ErrSummary    = errors.New("queue summaries contain no source URLs; import a source URL backup or a list of download links")
	ErrLegacyURLs = errors.New("older queue files may contain incomplete source URLs")
)

type file[T any] struct {
	Version   int    `json:"version"`
	Mode      string `json:"mode,omitempty"`
	Downloads []T    `json:"downloads"`
}

type options struct {
	Priority    int   `json:"priority"`
	Connections int   `json:"connections,omitempty"`
	SpeedLimit  int64 `json:"speedLimit,omitempty"`
}

type summaryEntry struct {
	options
	Status    download.Status `json:"status"`
	Completed int64           `json:"completed"`
	Total     int64           `json:"total"`
}

type sourceEntry struct {
	options
	URL         string `json:"url"`
	Destination string `json:"destination,omitempty"`
	Checksum    string `json:"checksum,omitempty"`
}

// ExportSummary records unfinished queue progress and options without source
// or destination strings, which may both carry credentials.
func ExportSummary(items []download.Item) ([]byte, error) {
	return export(items, summaryMode, func(item download.Item, settings options) summaryEntry {
		return summaryEntry{options: settings, Status: item.Status, Completed: item.Completed, Total: item.Total}
	})
}

// ExportSources backs up exact unfinished source URLs and queue options.
// URLs and destinations may contain secrets. Headers and sessions are omitted.
func ExportSources(items []download.Item) ([]byte, error) {
	return export(items, backupMode, func(item download.Item, settings options) sourceEntry {
		return sourceEntry{options: settings, URL: item.URL, Destination: item.Destination, Checksum: item.Checksum}
	})
}

// Write atomically replaces a queue file using a new owner-writable file.
func Write(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".nimget-queue-*")
	if err != nil {
		return errors.New("could not save queue file")
	}
	defer os.Remove(temporary.Name())
	_, err = temporary.Write(append(data, '\n'))
	if errors.Join(err, temporary.Sync(), temporary.Close()) != nil {
		return errors.New("could not save queue file")
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return errors.New("could not save queue file")
	}
	return nil
}

func export[T any](items []download.Item, mode string, entry func(download.Item, options) T) ([]byte, error) {
	f := file[T]{Version: version, Mode: mode, Downloads: []T{}}
	for _, item := range items {
		if item.Status != download.StatusCompleted {
			f.Downloads = append(f.Downloads, entry(item, options{item.Priority, item.Connections, item.SpeedLimit}))
		}
	}
	return json.MarshalIndent(f, "", "  ")
}

// Import reads source backups and text lists. Legacy JSON requires acknowledgement.
func Import(data []byte) ([]download.Request, error) {
	return importData(data, false)
}

// ImportLegacyURLs also accepts the original URLs in an older JSON queue file.
func ImportLegacyURLs(data []byte) ([]download.Request, error) {
	return importData(data, true)
}

func importData(data []byte, allowLegacyURLs bool) ([]download.Request, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return importJSON(trimmed, allowLegacyURLs)
	}
	var reqs []download.Request
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		reqs = append(reqs, download.Request{URL: line})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, errors.New("no downloads found")
	}
	return reqs, nil
}

func importJSON(data []byte, allowLegacyURLs bool) ([]download.Request, error) {
	var format struct {
		Version int    `json:"version"`
		Mode    string `json:"mode"`
	}
	invalid := errors.New("invalid queue file")
	if err := json.Unmarshal(data, &format); err != nil {
		return nil, invalid
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	switch {
	case format.Version == 1 && format.Mode == "":
		var legacy file[download.Request]
		if err := decoder.Decode(&legacy); err != nil {
			return nil, invalid
		}
		if !allowLegacyURLs {
			return nil, ErrLegacyURLs
		}
		return legacy.Downloads, nil
	case format.Version == version && format.Mode == summaryMode:
		return nil, ErrSummary
	case format.Version == version && format.Mode == backupMode:
		var backup file[sourceEntry]
		if err := decoder.Decode(&backup); err != nil {
			return nil, invalid
		}
		requests := make([]download.Request, 0, len(backup.Downloads))
		for _, entry := range backup.Downloads {
			requests = append(requests, download.Request{
				URL: entry.URL, Destination: entry.Destination, Checksum: entry.Checksum,
				Priority: entry.Priority, Connections: entry.Connections, SpeedLimit: entry.SpeedLimit,
			})
		}
		return requests, nil
	default:
		return nil, errors.New("unsupported queue file format")
	}
}
