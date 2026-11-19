package magnet

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

type mappedPayload struct {
	manifest   Manifest
	accessFile func(PayloadFile, []byte, int64, bool) (int, error)
	sync       func() error
	close      func() error
}

func NewDirectoryStorage(manifest Manifest, open func(string, bool) (*os.File, error), sync, close func() error, recheck bool) StagedPayload {
	return newPieceStorage(mappedPayload{manifest: manifest, accessFile: func(file PayloadFile, b []byte, off int64, write bool) (int, error) {
		f, err := open(strings.Join(file.Path, "/"), write)
		if err != nil {
			return 0, err
		}
		n, err := accessPayloadFile(f, b, off, write)
		return n, errors.Join(err, f.Close())
	}, sync: sync, close: close}, recheck)
}

func NewFileStorage(manifest Manifest, file *os.File, recheck bool) StagedPayload {
	return newPieceStorage(mappedPayload{manifest: manifest, accessFile: func(_ PayloadFile, b []byte, off int64, write bool) (int, error) {
		return accessPayloadFile(file, b, off, write)
	}, sync: file.Sync, close: file.Close}, recheck)
}

func accessPayloadFile(file *os.File, b []byte, off int64, write bool) (int, error) {
	if write {
		return file.WriteAt(b, off)
	}
	return file.ReadAt(b, off)
}

func (d mappedPayload) Sync() error  { return d.sync() }
func (d mappedPayload) Close() error { return d.close() }

func (d mappedPayload) ReadAt(b []byte, off int64) (int, error) {
	return d.access(b, off, false)
}

func (d mappedPayload) WriteAt(b []byte, off int64) (int, error) {
	return d.access(b, off, true)
}

func (d mappedPayload) access(b []byte, off int64, write bool) (int, error) {
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
			n, err := d.accessFile(file, part, off-file.Offset, write)
			done += n
			if err != nil {
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
