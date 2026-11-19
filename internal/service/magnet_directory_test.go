package service_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

func TestMagnetPublishesNestedDirectory(t *testing.T) {
	for _, kind := range []string{"v1", "v2", "hybrid"} {
		t.Run(kind, func(t *testing.T) { testMagnetPublishesNestedDirectory(t, kind) })
	}
}

func testMagnetPublishesNestedDirectory(t *testing.T, kind string) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	const name = "field-notes"
	files := directoryFixtureFiles()
	link, _ := trackedDirectoryMagnet(t, name, files, kind)
	item, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
	assertMagnetDirectory(t, done, filepath.Join(cfg.DownloadRoots[0], name), files)
	if _, err := os.Lstat(filepath.Join(done.Destination, ".pad")); !os.IsNotExist(err) {
		t.Fatalf("padding must not be published: %v", err)
	}
}

func TestMagnetWithOnePayloadAndPaddingPublishesAFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	seedRoot := t.TempDir()
	folder := filepath.Join(seedRoot, "container")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	const name = "field-notes.txt"
	payload := bytes.Repeat([]byte("field notes"), 3500)
	for file, data := range map[string][]byte{
		"first-pad": make([]byte, 519), name: payload, "last-pad": make([]byte, 123),
	} {
		if err := os.WriteFile(filepath.Join(folder, file), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(folder); err != nil {
		t.Fatal(err)
	}
	for i := range info.Files {
		if info.Files[i].Path[0] != name {
			info.Files[i].Attr = "p"
		}
	}
	raw, err := bencode.Marshal(&info)
	if err != nil {
		t.Fatal(err)
	}
	meta := &metainfo.MetaInfo{InfoBytes: raw}
	tracker, _ := trackDirectoryFixture(t, seedRoot, meta)
	link := fmt.Sprintf("magnet:?xt=urn:btih:%s&tr=%s", meta.HashInfoBytes().HexString(), url.QueryEscape(tracker))
	item, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
	want := filepath.Join(cfg.DownloadRoots[0], name)
	got, err := os.ReadFile(want)
	if done.Status != download.StatusCompleted || done.Total != int64(len(payload)) ||
		done.Completed != done.Total || err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("single payload must be published without padding: %+v, %d bytes, %v", done, len(got), err)
	}
	assertMagnetOutputPath(t, want, done.Destination)
}

func TestMagnetDirectoryReservationExcludesDescendantDownloads(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	const name = "reserved"
	root := cfg.DownloadRoots[0]
	if _, err := c.Add(ctx, download.Request{
		URL: "https://example.test/appendix.txt", Destination: filepath.Join(root, name, "appendix.txt"),
	}); err != nil {
		t.Fatal(err)
	}
	files := directoryFixtureFiles()
	link, _ := trackedDirectoryMagnet(t, name, files, "v1")
	item, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
	assertMagnetDirectory(t, done, filepath.Join(root, name+" (1)"), files)
	if _, err := c.Add(ctx, download.Request{
		URL: "https://example.test/another.txt", Destination: filepath.Join(done.Destination, "another.txt"),
	}); err == nil {
		t.Fatal("a download could reserve a path under the magnet output")
	}
}

func TestMagnetRejectsUnsafeDirectoryManifestBeforeStaging(t *testing.T) {
	cases := []struct {
		name  string
		paths [][]string
	}{
		{"traversal", [][]string{{"notes", "..", "escape.txt"}, {"readme.txt"}}},
		{"windows device", [][]string{{"notes", "NUL.txt"}, {"readme.txt"}}},
		{"case collision", [][]string{{"notes", "Page.txt"}, {"notes", "page.txt"}}},
		{"unicode collision", [][]string{{"notes", "caf\u00e9.txt"}, {"notes", "cafe\u0301.txt"}}},
		{"file directory collision", [][]string{{"notes"}, {"notes", "page.txt"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			cfg := handoffServiceConfig(t)
			c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
			prefs, err := c.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
			if _, err := c.PutSettings(ctx, prefs); err != nil {
				t.Fatal(err)
			}
			info := metainfo.Info{Name: "unsafe-manifest", PieceLength: 16 << 10}
			for _, parts := range tc.paths {
				info.Files = append(info.Files, metainfo.FileInfo{Path: parts, Length: 1})
			}
			sum := sha1.Sum([]byte("xy"))
			info.Pieces = sum[:]
			raw, err := bencode.Marshal(&info)
			if err != nil {
				t.Fatal(err)
			}
			meta := &metainfo.MetaInfo{InfoBytes: raw}
			tracker, _ := trackDirectoryFixture(t, "", meta)
			saveDir := filepath.Join(cfg.DownloadRoots[0], "no-payload-storage")
			item, err := c.Add(ctx, download.Request{
				URL:       fmt.Sprintf("magnet:?xt=urn:btih:%s&tr=%s", meta.HashInfoBytes().HexString(), url.QueryEscape(tracker)),
				Directory: saveDir,
			})
			if err != nil {
				t.Fatal(err)
			}
			done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
			if done.Status != download.StatusFailed || !strings.Contains(done.Error, "output paths") || done.Destination != "" {
				t.Fatalf("expected the unsafe manifest to fail before reservation: %+v", done)
			}
			if _, err := os.Lstat(saveDir); !os.IsNotExist(err) {
				t.Fatalf("the unsafe manifest opened staging: %v", err)
			}
		})
	}
}

func TestMagnetDirectoryPublicationRecoversWithOccupiedName(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	const name = "crash-directory"
	files := directoryFixtureFiles()
	link, seed := trackedDirectoryMagnet(t, name, files, "hybrid")
	params, err := json.Marshal(magnetCrashFixture{cfg.DatabasePath, cfg.TokenPath, cfg.DownloadRoots[0], link, download.MagnetPublicationRecorded})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMagnetPublicationCrashHelper$")
	cmd.Env = append(os.Environ(), magnetCrashEnv+"="+string(params))
	output, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != magnetCrashCode {
		t.Fatalf("helper did not exit before publication: %v\n%s", err, output)
	}
	_, line, ok := strings.Cut(string(output), "publication=")
	if !ok {
		t.Fatalf("missing crash fixture task ID: %s", output)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(line), "\n")
	seed.Drop()
	foreign := filepath.Join(cfg.DownloadRoots[0], name, "existing.txt")
	if err := os.Mkdir(filepath.Dir(foreign), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("previous contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, id)
	assertMagnetDirectory(t, done, filepath.Join(cfg.DownloadRoots[0], name+" (1)"), files)
	if content, err := os.ReadFile(foreign); err != nil || string(content) != "previous contents" {
		t.Fatalf("recovery modified the occupied directory: %q, %v", content, err)
	}
	entries, err := os.ReadDir(cfg.DownloadRoots[0])
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected one finished output and the occupied directory: %v, %v", entries, err)
	}
	if err := c.Delete(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	assertMagnetDirectory(t, done, done.Destination, files)
}

func TestMagnetDirectoryDeletePreservesChangesAndForeignFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	files := directoryFixtureFiles()
	files["empty-after-cleanup/owned.txt"] = []byte("remove me")
	files["linked/owned.txt"] = []byte("external bytes")
	const name = "directory-cleanup"
	link, _ := trackedDirectoryMagnet(t, name, files, "v1")
	item, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
	assertMagnetDirectory(t, done, filepath.Join(cfg.DownloadRoots[0], name), files)
	disk := func(rel string) string { return filepath.Join(done.Destination, filepath.FromSlash(rel)) }
	write := func(file string, data []byte) {
		t.Helper()
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	edited := bytes.Clone(files["notes/deep/b.txt"])
	edited[0] ^= 0xff
	write(disk("notes/deep/b.txt"), edited)
	write(disk("notes/deep/replace.tmp"), files["notes/deep/a.bin"])
	if err := os.Remove(disk("notes/deep/a.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(disk("notes/deep/replace.tmp"), disk("notes/deep/a.bin")); err != nil {
		t.Fatal(err)
	}
	write(disk("notes/added.dat"), []byte("user data"))
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "owned.txt")
	write(outside, files["linked/owned.txt"])
	if err := os.Rename(disk("linked"), disk("preserved-directory")); err != nil {
		t.Fatal(err)
	}
	linkErr := os.Symlink(outsideDir, disk("linked"))
	if linkErr != nil {
		if os.PathSeparator != '\\' {
			t.Fatal(linkErr)
		}
		// A Windows test account may lack permission to create symlinks.
		if err := os.Mkdir(disk("linked"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(disk("linked/owned.txt"), files["linked/owned.txt"])
	}
	if err := c.Delete(ctx, item.ID, true); err == nil {
		t.Fatal("the service did not report the preserved files")
	}
	for file, want := range map[string][]byte{
		disk("notes/added.dat"):               []byte("user data"),
		disk("notes/deep/a.bin"):              files["notes/deep/a.bin"],
		disk("notes/deep/b.txt"):              edited,
		outside:                               files["linked/owned.txt"],
		disk("linked/owned.txt"):              files["linked/owned.txt"],
		disk("preserved-directory/owned.txt"): files["linked/owned.txt"],
	} {
		got, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("preserve %q: %v", file, err)
		}
	}
	if linkErr == nil {
		if target, err := os.Readlink(disk("linked")); err != nil || filepath.Clean(target) != filepath.Clean(outsideDir) {
			t.Errorf("preserve outside reference: target %q, %v", target, err)
		}
	}
	for _, rel := range []string{"readme.txt", "notes/empty.txt", "empty-after-cleanup"} {
		if _, err := os.Lstat(disk(rel)); !os.IsNotExist(err) {
			t.Errorf("remove unedited output %q: %v", rel, err)
		}
	}
	if items, err := c.List(ctx); err != nil || len(items) != 0 {
		t.Errorf("task should be removed after cleanup: %v, %v", items, err)
	}
}

func directoryFixtureFiles() map[string][]byte {
	return map[string][]byte{
		"readme.txt":       []byte("field observations"),
		"notes/empty.txt":  nil,
		"notes/deep/a.bin": bytes.Repeat([]byte("nested bytes\n"), 1401),
		"notes/deep/b.txt": bytes.Repeat([]byte("appendix\n"), 1877),
	}
}

func assertMagnetDirectory(t *testing.T, item download.Item, destination string, files map[string][]byte) {
	t.Helper()
	var total int64
	for _, data := range files {
		total += int64(len(data))
	}
	if item.Status != download.StatusCompleted || item.Completed != total || item.Total != total {
		t.Fatalf("expected directory containing %d verified bytes: %+v", total, item)
	}
	assertMagnetOutputPath(t, destination, item.Destination)
	for rel, want := range files {
		path := filepath.Join(destination, filepath.FromSlash(rel))
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%q: got %d bytes, want %d (%v)", rel, len(got), len(want), err)
		}
		assertMagnetQuarantine(t, path)
	}
}

func trackedDirectoryMagnet(t *testing.T, name string, files map[string][]byte, kind string) (string, *torrent.Torrent) {
	t.Helper()
	dir := t.TempDir()
	folder := filepath.Join(dir, name)
	for rel, data := range files {
		path := filepath.Join(folder, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10, Name: name}
	layers := map[string]string{}
	if kind == "v1" {
		if err := info.BuildFromFilePath(folder); err != nil {
			t.Fatal(err)
		}
	} else {
		info.MetaVersion = 2
		paths := slices.Sorted(maps.Keys(files))
		for i, rel := range paths {
			data := files[rel]
			var hashes [][32]byte
			var layer []byte
			for chunk := range slices.Chunk(data, int(info.PieceLength)) {
				sum := sha256.Sum256(chunk)
				hashes = append(hashes, sum)
				layer = append(layer, sum[:]...)
			}
			entry := metainfo.FileTreeFile{Length: int64(len(data))}
			if len(hashes) != 0 {
				root := merkle.RootWithPadHash(hashes, metainfo.HashForPiecePad(info.PieceLength))
				entry.PiecesRoot = string(root[:])
				if len(hashes) > 1 {
					layers[entry.PiecesRoot] = string(layer)
				}
			}
			parts := strings.Split(rel, "/")
			addDirectoryFixtureFile(&info.FileTree, parts, entry)
			if kind == "hybrid" {
				info.Files = append(info.Files, metainfo.FileInfo{Path: parts, Length: int64(len(data))})
				if remainder := int64(len(data)) % info.PieceLength; i != len(paths)-1 && remainder != 0 {
					pad := info.PieceLength - remainder
					info.Files = append(info.Files, metainfo.FileInfo{
						Path: []string{".pad", fmt.Sprint(pad)}, Length: pad,
						ExtendedFileAttrs: metainfo.ExtendedFileAttrs{Attr: "p"},
					})
					data = append(append([]byte(nil), data...), make([]byte, pad)...)
				}
				for chunk := range slices.Chunk(data, int(info.PieceLength)) {
					sum := sha1.Sum(chunk)
					info.Pieces = append(info.Pieces, sum[:]...)
				}
			}
		}
	}
	raw, err := bencode.Marshal(&info)
	if err != nil {
		t.Fatal(err)
	}
	meta := &metainfo.MetaInfo{InfoBytes: raw}
	if len(layers) > 0 {
		meta.PieceLayers = layers
	}
	tracker, seeder := trackDirectoryFixture(t, dir, meta)
	v1 := "urn:btih:" + meta.HashInfoBytes().HexString()
	topic := v1
	if kind != "v1" {
		hash := sha256.Sum256(raw)
		topic = "urn:btmh:1220" + hex.EncodeToString(hash[:])
		if kind == "hybrid" {
			topic = v1 + "&xt=" + topic
		}
	}
	return fmt.Sprintf("magnet:?xt=%s&tr=%s", topic, url.QueryEscape(tracker)), seeder
}

func addDirectoryFixtureFile(tree *metainfo.FileTree, parts []string, file metainfo.FileTreeFile) {
	if len(parts) == 0 {
		tree.File = file
		return
	}
	if tree.Dir == nil {
		tree.Dir = make(map[string]metainfo.FileTree)
	}
	child := tree.Dir[parts[0]]
	addDirectoryFixtureFile(&child, parts[1:], file)
	tree.Dir[parts[0]] = child
}

func trackDirectoryFixture(t *testing.T, dir string, meta *metainfo.MetaInfo) (string, *torrent.Torrent) {
	t.Helper()
	cfg := torrent.NewDefaultClientConfig().SetListenAddr("127.0.0.1:0")
	cfg.DataDir, cfg.Seed = dir, true
	if dir == "" {
		// The fixture seeds the verified payload through safe backing names so
		// the untrusted manifest paths are used only by the receiver under test.
		cfg.DataDir = t.TempDir()
		backingName := func(offset int64) string { return fmt.Sprintf("payload-%d", offset) }
		for offset, data := range []byte("xy") {
			if err := os.WriteFile(filepath.Join(cfg.DataDir, backingName(int64(offset))), []byte{data}, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		backend := storage.NewFileOpts(storage.NewFileClientOpts{
			ClientBaseDir: cfg.DataDir, UsePartFiles: generics.Some(false), PieceCompletion: storage.NewMapPieceCompletion(),
			FilePathMaker: func(opts storage.FilePathMakerOpts) string { return backingName(opts.File.TorrentOffset) },
		})
		cfg.DefaultStorage = backend
		t.Cleanup(func() { _ = backend.Close() })
	}
	cfg.NoDHT, cfg.NoDefaultPortForwarding, cfg.DisableUTP, cfg.DisableIPv6 = true, true, true, true
	cfg.DisableTrackers = true
	cfg.Slogger = slog.New(slog.DiscardHandler)
	seed, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seed.Close() })
	seeder, err := seed.AddTorrent(meta)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-seeder.Complete().On():
	case <-time.After(8 * time.Second):
		t.Fatal("fixture peer did not verify the seed directory")
	}
	addr := seed.ListenAddrs()[0].(*net.TCPAddr)
	compact := binary.BigEndian.AppendUint16(append([]byte(nil), addr.IP.To4()...), uint16(addr.Port))
	announce, err := bencode.Marshal(map[string]any{"interval": 30, "peers": string(compact)})
	if err != nil {
		t.Fatal(err)
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(announce)
	}))
	t.Cleanup(tracker.Close)
	return tracker.URL, seeder
}
