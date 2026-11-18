package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/amemiya02/nimget/internal/download"
)

type MissingDownloadFile struct {
	Destination   string `json:"destination"`
	FolderMissing bool   `json:"folderMissing"`
}

// MissingDownloadFiles checks the recorded destinations of completed tasks.
func (d *Desktop) MissingDownloadFiles(ctx context.Context, ids []string) (map[string]MissingDownloadFile, error) {
	m, err := d.manager()
	if err != nil {
		return nil, err
	}
	missing := make(map[string]MissingDownloadFile)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, err := m.Get(ctx, id)
		if errors.Is(err, download.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if item.Status != download.StatusCompleted {
			continue
		}
		if exists, err := downloadedFileExists(item.Destination); err == nil && !exists {
			missing[id] = MissingDownloadFile{
				Destination:   item.Destination,
				FolderMissing: downloadFolderMissing(filepath.Dir(item.Destination)),
			}
		}
	}
	return missing, nil
}

func (d *Desktop) requireDownloadFile(path string) error {
	exists, err := downloadedFileExists(path)
	if err == nil && !exists {
		return errors.New(d.tr("The downloaded file no longer exists."))
	}
	return err
}

func downloadedFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if isMissingPath(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func downloadFolderMissing(path string) bool {
	info, err := os.Stat(path)
	return isMissingPath(err) || (err == nil && !info.IsDir())
}

func isMissingPath(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
