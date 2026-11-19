package magnet

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"

	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
)

var errTorrentContents = errors.New("staged files no longer match the torrent")

// VerifyDirectory rereads the staged files before publication. Each seal call
// checks one file's content and keeps the resulting digest with its identity.
func (m Manifest) VerifyDirectory(ctx context.Context, seal func(PayloadFile, func(*os.File) (string, error)) error) error {
	var v1 *v1PieceVerifier
	if m.info.HasV1() {
		v1 = &v1PieceVerifier{info: m.info, hash: sha1.New()}
	}
	var v1Offset int64
	fillV1 := func(end int64) error {
		if v1 == nil || end == v1Offset {
			return nil
		}
		_, err := io.CopyN(v1, cancellableReader{ctx, zeroReader{}}, end-v1Offset)
		v1Offset = end
		return err
	}
	for _, entry := range m.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fillV1(entry.Offset); err != nil {
			return err
		}
		if entry.Pad {
			if err := fillV1(entry.Offset + entry.Size); err != nil {
				return err
			}
			continue
		}
		if err := seal(entry, func(file *os.File) (string, error) {
			stat, err := file.Stat()
			if err != nil {
				return "", err
			}
			if stat.Size() != entry.Size {
				return "", errTorrentContents
			}
			digest := sha256.New()
			var writers = []io.Writer{digest}
			if v1 != nil {
				writers = append(writers, v1)
			}
			var v2 *merkle.Hash
			if m.info.HasV2() {
				v2 = merkle.NewHash()
				writers = append(writers, v2)
			}
			if _, err := io.CopyN(io.MultiWriter(writers...), cancellableReader{ctx, file}, entry.Size); err != nil {
				return "", err
			}
			if v2 != nil && entry.Size != 0 && (!entry.meta.PiecesRoot.Ok || !bytes.Equal(v2.Sum(nil), entry.meta.PiecesRoot.Value[:])) {
				return "", errTorrentContents
			}
			return hex.EncodeToString(digest.Sum(nil)), nil
		}); err != nil {
			return err
		}
		v1Offset = entry.Offset + entry.Size
	}
	if v1 != nil {
		if err := fillV1(m.v1Size); err != nil {
			return err
		}
		if v1.written > 0 {
			if err := v1.finishPiece(); err != nil {
				return err
			}
		}
		if v1.index != m.info.NumPieces() {
			return errTorrentContents
		}
	}
	return nil
}

type v1PieceVerifier struct {
	info    *metainfo.Info
	hash    hash.Hash
	index   int
	written int64
}

func (v *v1PieceVerifier) Write(data []byte) (int, error) {
	var done int
	for len(data) != 0 {
		n := int(min(int64(len(data)), v.info.PieceLength-v.written))
		_, _ = v.hash.Write(data[:n])
		v.written += int64(n)
		done += n
		if v.written == v.info.PieceLength {
			if err := v.finishPiece(); err != nil {
				return done, err
			}
		}
		data = data[n:]
	}
	return done, nil
}

func (v *v1PieceVerifier) finishPiece() error {
	if v.index >= len(v.info.Pieces)/sha1.Size {
		return errTorrentContents
	}
	want := v.info.Piece(v.index).V1Hash()
	if !want.Ok || !bytes.Equal(v.hash.Sum(nil), want.Value[:]) {
		return errTorrentContents
	}
	v.hash.Reset()
	v.written = 0
	v.index++
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}
