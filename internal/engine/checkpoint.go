package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/amemiya02/nimget/internal/partialfile"
)

const checkpointVersion = 2

// checkpoint is the persisted resume state: every byte range not yet written,
// plus the validators that prove the remote file is still the same one.
type checkpoint struct {
	Version      int        `json:"version"`
	Identity     string     `json:"identity"`
	Size         int64      `json:"size"`
	FileID       string     `json:"fileId,omitempty"`
	SourceScope  string     `json:"sourceScope,omitempty"`
	ETag         string     `json:"etag,omitempty"`
	LastModified string     `json:"lastModified,omitempty"`
	Remaining    []interval `json:"remaining"`
}

// resumable reports whether c describes the same remote file as want. Without
// an ETag or Last-Modified there is no way to tell, so resume is refused.
func (c checkpoint) resumable(want checkpoint) bool {
	if c.ETag == "" && c.LastModified == "" {
		return false
	}
	return c.Version == checkpointVersion && c.Identity == want.Identity && c.Size == want.Size &&
		(c.SourceScope == "" || c.SourceScope == want.SourceScope) &&
		c.ETag == want.ETag && c.LastModified == want.LastModified
}

// createCheckpoint reserves the sidecar without overwriting another file.
func createCheckpoint(path string, state checkpoint) error {
	if err := state.validate(); err != nil {
		return err
	}
	f, err := partialfile.Open(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(state); err != nil {
		_ = f.Close()
		return err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err == nil {
		syncDir(filepath.Dir(path))
	}
	return err
}

func writeCheckpoint(path string, state checkpoint) error {
	if err := state.validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}
	temporary := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(temporary)
		}
	}()
	if err := json.NewEncoder(file).Encode(state); err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close checkpoint: %w", err)
	}
	if err := replaceCheckpoint(temporary, path); err != nil {
		return fmt.Errorf("replace checkpoint: %w", err)
	}
	cleanup = false
	syncDir(dir)
	return nil
}

// Root.Rename uses Windows replacement semantics that let readers finish
// reading their open checkpoint while new opens see its replacement.
func replaceCheckpoint(temporary, path string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(temporary, path)
	}
	dir, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Rename(filepath.Base(temporary), filepath.Base(path))
}

func readCheckpoint(path string) (checkpoint, error) {
	f, err := partialfile.Open(path, os.O_RDONLY, 0)
	if err != nil {
		return checkpoint{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return checkpoint{}, err
	}
	var state checkpoint
	if err := json.Unmarshal(data, &state); err != nil {
		return checkpoint{}, errors.New("invalid partial download checkpoint")
	}
	if err := state.validate(); err != nil {
		return checkpoint{}, err
	}
	return state, nil
}

func (c checkpoint) validate() error {
	if c.Version != checkpointVersion {
		return fmt.Errorf("unsupported checkpoint version %d", c.Version)
	}
	if c.Identity == "" || (c.Size <= 0 && !(c.Size == 0 && c.FileID != "" && len(c.Remaining) == 0)) {
		return errors.New("checkpoint has no identity or size")
	}
	remaining := append([]interval(nil), c.Remaining...)
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Start < remaining[j].Start })
	for i, iv := range remaining {
		if iv.Start < 0 || iv.End <= iv.Start || iv.End > c.Size {
			return fmt.Errorf("invalid remaining interval %+v", iv)
		}
		if i > 0 && remaining[i-1].End > iv.Start {
			return fmt.Errorf("overlapping remaining intervals %+v and %+v", remaining[i-1], iv)
		}
	}
	return nil
}

// syncDir makes a rename durable. Some platforms cannot sync directories; the
// file itself is already synced, so failure is ignored.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
