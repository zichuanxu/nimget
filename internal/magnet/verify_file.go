package magnet

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
)

// VerifySingleFile binds the staged bytes to verified torrent metadata and
// returns the content digest used when reconciling publication and cleanup.
func VerifySingleFile(ctx context.Context, f *os.File, info *metainfo.Info) (string, error) {
	size := info.TotalLength()
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if stat.Size() != size {
		return "", errors.New("staged file size changed after verification")
	}
	digest := sha256.New()
	reader := cancellableReader{ctx, f}
	if info.HasV2() && size != 0 {
		files := info.UpvertedFiles()
		hash := merkle.NewHash()
		if _, err := io.CopyN(io.MultiWriter(hash, digest), reader, size); err != nil {
			return "", err
		}
		if len(files) != 1 || !files[0].PiecesRoot.Ok || !bytes.Equal(hash.Sum(nil), files[0].PiecesRoot.Value[:]) {
			return "", errors.New("staged file no longer matches the torrent")
		}
	} else {
		hash := sha1.New()
		writer := io.MultiWriter(hash, digest)
		for i := range info.NumPieces() {
			piece := info.Piece(i)
			want := piece.V1Hash()
			hash.Reset()
			if _, err := io.CopyN(writer, reader, piece.Length()); err != nil {
				return "", err
			}
			if !want.Ok || !bytes.Equal(hash.Sum(nil), want.Value[:]) {
				return "", errors.New("staged file no longer matches the torrent")
			}
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func FileDigest(ctx context.Context, f *os.File, size int64) (string, error) {
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if stat.Size() != size {
		return "", errors.New("download file size changed")
	}
	digest := sha256.New()
	if _, err := io.CopyN(digest, cancellableReader{ctx, f}, size); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type cancellableReader struct {
	ctx context.Context
	io.Reader
}

func (r cancellableReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
