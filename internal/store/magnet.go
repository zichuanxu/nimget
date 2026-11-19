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
	if err := s.update(ctx, tx, owner, nil, nil); err != nil {
		return err
	}
	for _, item := range duplicates {
		if err := s.update(ctx, tx, item, nil, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func encodeMagnetState(source *download.Magnet) (string, error) {
	if source == nil {
		return "", nil
	}
	state, err := json.Marshal(source)
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

func (s *SQLite) decodeMagnet(state, trackers string) (*download.Magnet, error) {
	if state == "" {
		return nil, nil
	}
	var source download.Magnet
	if err := json.Unmarshal([]byte(state), &source); err != nil {
		return nil, fmt.Errorf("read magnet state: %w", err)
	}
	plain, err := s.open(trackers)
	if err != nil {
		return nil, err
	}
	if plain != "" {
		if err := json.Unmarshal([]byte(plain), &source.Trackers); err != nil {
			return nil, fmt.Errorf("read magnet tracker hints: %w", err)
		}
	}
	return &source, nil
}
