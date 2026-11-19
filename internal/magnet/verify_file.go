package magnet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// VerifySingleFile binds the staged bytes to verified torrent metadata and
// returns the content digest used when reconciling publication and cleanup.
func (m Manifest) VerifySingleFile(ctx context.Context, f *os.File) (string, error) {
	var digest string
	err := m.VerifyDirectory(ctx, func(_ PayloadFile, verify func(*os.File) (string, error)) (err error) {
		digest, err = verify(f)
		return err
	})
	return digest, err
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
