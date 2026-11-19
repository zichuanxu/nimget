package store

import (
	"encoding/json"
	"fmt"

	"github.com/amemiya02/nimget/internal/download"
)

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
