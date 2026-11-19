package main

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/amemiya02/nimget/internal/download"
)

func TestDesktopOpensAndRevealsFileAndDirectoryOutputs(t *testing.T) {
	d, root := desktopWithBulkTasks(t, map[string]download.Item{
		"report.txt": {Status: download.StatusCompleted},
		"field-notes": {
			Status: download.StatusCompleted, SourceKind: download.SourceMagnet,
			Magnet: &download.Magnet{Output: &download.MagnetObject{Directory: true}},
		},
	})
	file, dir := filepath.Join(root, "report.txt"), filepath.Join(root, "field-notes")
	if err := os.WriteFile(file, []byte("verified report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	type call struct {
		kind, path string
		selectFile bool
	}
	var calls []call
	d.launchDownloadFile = func(path string) error {
		calls = append(calls, call{kind: "file", path: path})
		return nil
	}
	d.showDownloadPath = func(path string, selectFile bool) error {
		calls = append(calls, call{kind: "manager", path: path, selectFile: selectFile})
		return nil
	}
	ids := []string{"report.txt", "field-notes"}
	if missing, err := d.MissingDownloadFiles(context.Background(), ids); err != nil || len(missing) != 0 {
		t.Fatalf("both outputs exist: missing %+v, %v", missing, err)
	}
	for _, id := range ids {
		if err := d.Open(id); err != nil {
			t.Fatal(err)
		}
		if err := d.Reveal(id); err != nil {
			t.Fatal(err)
		}
	}
	want := []call{{"file", file, false}, {"manager", file, true}, {"manager", dir, false}, {"manager", dir, false}}
	if !slices.Equal(calls, want) {
		t.Fatalf("output actions = %+v; want %+v", calls, want)
	}
}

func TestDesktopRevealsMagnetSaveFolderBeforeMetadata(t *testing.T) {
	d, root := desktopWithBulkTasks(t, nil)
	task, err := d.Add(download.Request{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	var shown string
	d.showDownloadPath = func(path string, selectFile bool) error {
		if selectFile {
			t.Error("reveal selected a file before metadata")
		}
		shown = path
		return nil
	}
	if err := d.Reveal(task.ID); err != nil || shown != root {
		t.Fatalf("Reveal = %q, %v; want %q", shown, err, root)
	}
}

func TestMissingDownloadFilesTracksMovesAndRestoration(t *testing.T) {
	folderTask := filepath.Join("category", "report.zip")
	d, root := desktopWithBulkTasks(t, map[string]download.Item{
		"video.ts": {Status: download.StatusCompleted},
		folderTask: {Status: download.StatusCompleted},
		"queued":   {Status: download.StatusQueued},
	})
	d.SetLocale("zh-CN")
	video := filepath.Join(root, "video.ts")
	report := filepath.Join(root, folderTask)
	category := filepath.Dir(report)
	if err := os.Mkdir(category, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{video, report} {
		if err := os.WriteFile(file, []byte("downloaded data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want map[string]MissingDownloadFile) {
		t.Helper()
		got, err := d.MissingDownloadFiles(context.Background(), []string{"video.ts", folderTask, "queued", "deleted"})
		if err != nil || !maps.Equal(got, want) {
			t.Fatalf("MissingDownloadFiles = %+v, %v; want %+v", got, err, want)
		}
	}
	check(nil)
	movedVideo, movedFolder := video+".moved", category+".moved"
	if err := os.Rename(video, movedVideo); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(category, movedFolder); err != nil {
		t.Fatal(err)
	}
	check(map[string]MissingDownloadFile{
		"video.ts": {Destination: video},
		folderTask: {Destination: report, FolderMissing: true},
	})
	if err := d.Open("video.ts"); err == nil || err.Error() != "下载的文件已不存在。" {
		t.Errorf("Open missing file = %v", err)
	}
	if _, err := d.ConvertToMP4("video.ts"); err == nil || err.Error() != "下载的文件已不存在。" {
		t.Errorf("Convert missing file = %v", err)
	}
	if err := d.Reveal(folderTask); err == nil || err.Error() != "下载文件夹已不存在。" {
		t.Errorf("Reveal missing folder = %v", err)
	}
	if err := os.Mkdir(video, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(category, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	check(map[string]MissingDownloadFile{
		"video.ts": {Destination: video},
		folderTask: {Destination: report, FolderMissing: true},
	})
	for _, path := range []string{video, category} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".moved", path); err != nil {
			t.Fatal(err)
		}
	}
	check(nil)
	item, err := d.item(folderTask)
	if err != nil || item.Status != download.StatusCompleted {
		t.Fatalf("stored task = %+v, %v; want completed", item, err)
	}
}
