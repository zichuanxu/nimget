package magnet

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

type directoryPayload struct {
	manifest Manifest
	open     func(string, bool) (*os.File, error)
	sync     func() error
	close    func() error
}

func NewDirectoryStorage(manifest Manifest, open func(string, bool) (*os.File, error), sync, close func() error, recheck bool) StagedPayload {
	return newPieceStorage(directoryPayload{manifest: manifest, open: open, sync: sync, close: close}, recheck)
}

func (d directoryPayload) Sync() error  { return d.sync() }
func (d directoryPayload) Close() error { return d.close() }

func (d directoryPayload) ReadAt(b []byte, off int64) (int, error) {
	return d.access(b, off, false)
}

func (d directoryPayload) WriteAt(b []byte, off int64) (int, error) {
	return d.access(b, off, true)
}

func (d directoryPayload) access(b []byte, off int64, write bool) (int, error) {
	var done int
	index := d.manifest.extentAt(off)
	for len(b) != 0 && index < len(d.manifest.extents) {
		file := d.manifest.Files[d.manifest.extents[index]]
		if off < file.Offset {
			break
		}
		part := b[:min(int64(len(b)), file.Offset+file.Size-off)]
		if file.Pad {
			if write && len(bytes.Trim(part, "\x00")) != 0 {
				return done, errors.New("torrent padding contains nonzero bytes")
			}
			if !write {
				clear(part)
			}
			done += len(part)
		} else {
			f, err := d.open(strings.Join(file.Path, "/"), write)
			if err != nil {
				return done, err
			}
			var n int
			if write {
				n, err = f.WriteAt(part, off-file.Offset)
			} else {
				n, err = f.ReadAt(part, off-file.Offset)
			}
			done += n
			if err := errors.Join(err, f.Close()); err != nil {
				return done, err
			}
		}
		b = b[len(part):]
		off += int64(len(part))
		index++
	}
	if len(b) == 0 {
		return done, nil
	}
	if write {
		return done, io.ErrShortWrite
	}
	return done, io.EOF
}
