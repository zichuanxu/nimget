package store

import (
	"encoding/json"
	"errors"

	"github.com/amemiya02/nimget/internal/download"
)

func (s *SQLite) encodeWebsiteSession(session *download.WebsiteSession) (string, error) {
	if session == nil {
		return "", nil
	}
	if s.sealer == nil {
		return "", errors.New("no website session sealer")
	}
	plain, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	return s.sealer.Seal(string(plain))
}

func (s *SQLite) decodeWebsiteSession(sealed string) (*download.WebsiteSession, error) {
	if sealed == "" || s.sealer == nil {
		return nil, nil
	}
	plain, err := s.open(sealed)
	if err != nil || plain == "" {
		return nil, err
	}
	var session download.WebsiteSession
	if err := json.Unmarshal([]byte(plain), &session); err != nil {
		return nil, errors.New("invalid stored website session")
	}
	return &session, nil
}
