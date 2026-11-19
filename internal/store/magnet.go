package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/amemiya02/nimget/internal/download"
)

func (s *SQLite) UpdateMagnetOwnership(ctx context.Context, owner download.Item, duplicates []download.Item) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.update(ctx, tx, owner, nil, nil, nil); err != nil {
		return err
	}
	for _, item := range duplicates {
		if err := s.update(ctx, tx, item, nil, nil, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type magnetState struct {
	*download.Magnet
	DirectConnectionAllowed bool `json:"directConnectionAllowed,omitempty"`
}

func encodeMagnetState(item download.Item) (string, error) {
	if item.Magnet == nil {
		return "", nil
	}
	state, err := json.Marshal(magnetState{Magnet: item.Magnet, DirectConnectionAllowed: item.DirectConnectionAllowed})
	return string(state), err
}

func (s *SQLite) encodeMagnetTrackers(source *download.Magnet) (string, error) {
	if source == nil || len(source.Trackers) == 0 {
		return "", nil
	}
	trackers, err := json.Marshal(source.Trackers)
	if err != nil {
		return "", err
	}
	return s.seal(string(trackers))
}

func (s *SQLite) decodeMagnet(state, trackers string) (magnetState, error) {
	if state == "" {
		return magnetState{}, nil
	}
	source := magnetState{Magnet: &download.Magnet{}}
	if err := json.Unmarshal([]byte(state), &source); err != nil {
		return magnetState{}, fmt.Errorf("read magnet state: %w", err)
	}
	plain, err := s.open(trackers)
	if err != nil {
		return magnetState{}, err
	}
	if plain != "" {
		if err := json.Unmarshal([]byte(plain), &source.Trackers); err != nil {
			return magnetState{}, fmt.Errorf("read magnet tracker hints: %w", err)
		}
	}
	return source, nil
}

// UpdateMagnetCheckpoint flushes the WAL before allowing a staged payload to
// be written or moved. Its connection is reserved until NORMAL is restored.
func (s *SQLite) UpdateMagnetCheckpoint(ctx context.Context, item download.Item) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "PRAGMA synchronous=NORMAL")
	var metadata []byte
	if item.Magnet != nil {
		metadata = item.Magnet.Metadata
	}
	return s.update(ctx, conn, item, nil, nil, metadata)
}
