package magnet

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

type fileStorage struct {
	recheck    bool
	file       payloadIO
	writeError chan error
	mu         sync.Mutex
	completed  []storage.Completion
}

type payloadIO interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Close() error
}

// StagedPayload stays open until the torrent has stopped all payload access.
type StagedPayload interface {
	storage.ClientImpl
	WriteErrors() <-chan error
	Checkpoint() error
	Close() error
}

func newPieceStorage(file payloadIO, recheck bool) StagedPayload {
	return &fileStorage{file: file, recheck: recheck, writeError: make(chan error, 1)}
}

func (s *fileStorage) WriteErrors() <-chan error { return s.writeError }
func (s *fileStorage) Checkpoint() error         { return s.file.Sync() }
func (s *fileStorage) Close() error              { return s.file.Close() }

func (s *fileStorage) OpenTorrent(_ context.Context, info *metainfo.Info, _ metainfo.Hash) (storage.TorrentImpl, error) {
	if s.file == nil {
		return storage.TorrentImpl{}, errors.New("torrent storage requires a validated manifest")
	}
	s.completed = make([]storage.Completion, info.NumPieces())
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
	s.completed[p.piece.Index()] = storage.Completion{Ok: true, Complete: complete}
	s.mu.Unlock()
	return nil
}

func (p filePiece) MarkComplete() error    { return p.setComplete(true) }
func (p filePiece) MarkNotComplete() error { return p.setComplete(false) }

func (p filePiece) Completion() storage.Completion {
	s := p.storage
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.recheck {
		return storage.Completion{Ok: true, Complete: s.completed[p.piece.Index()].Complete}
	}
	return s.completed[p.piece.Index()]
}
