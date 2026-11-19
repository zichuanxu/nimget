package magnet

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// The verified single-file manifest always maps to this one task-owned file.
// Metadata never supplies a storage path.
type fileStorage struct {
	name       string
	file       *os.File
	writeError chan error
	mu         sync.Mutex
	completed  []bool
}

func newStagedFile(root *os.Root) (*fileStorage, error) {
	name := ".nimget-magnet-" + rand.Text() + ".part"
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return &fileStorage{name: name, file: file, writeError: make(chan error, 1)}, nil
}

func (s *fileStorage) OpenTorrent(_ context.Context, info *metainfo.Info, _ metainfo.Hash) (storage.TorrentImpl, error) {
	if s.file == nil {
		return storage.TorrentImpl{}, errors.New("torrent storage requires a validated manifest")
	}
	s.completed = make([]bool, info.NumPieces())
	return storage.TorrentImpl{Piece: func(piece metainfo.Piece) storage.PieceImpl {
		return filePiece{storage: s, piece: piece}
	}}, nil
}

type filePiece struct {
	storage *fileStorage
	piece   metainfo.Piece
}

func (p filePiece) ReadAt(b []byte, off int64) (int, error) {
	return io.NewSectionReader(p.storage.file, p.piece.Offset(), p.piece.Length()).ReadAt(b, off)
}

func (p filePiece) WriteAt(b []byte, off int64) (int, error) {
	if off < 0 || int64(len(b)) > p.piece.Length()-off {
		return 0, io.ErrShortWrite
	}
	n, err := p.storage.file.WriteAt(b, p.piece.Offset()+off)
	if err != nil {
		select {
		case p.storage.writeError <- err:
		default:
		}
	}
	return n, err
}

func (p filePiece) setComplete(complete bool) error {
	s := p.storage
	s.mu.Lock()
	s.completed[p.piece.Index()] = complete
	s.mu.Unlock()
	return nil
}

func (p filePiece) MarkComplete() error    { return p.setComplete(true) }
func (p filePiece) MarkNotComplete() error { return p.setComplete(false) }

func (p filePiece) Completion() storage.Completion {
	s := p.storage
	s.mu.Lock()
	defer s.mu.Unlock()
	return storage.Completion{Ok: true, Complete: s.completed[p.piece.Index()]}
}
