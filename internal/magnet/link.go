package magnet

import (
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/amemiya02/nimget/internal/download"
)

const (
	v1Topic               = "urn:btih:"
	v2Topic               = "urn:btmh:"
	sha256MultihashPrefix = "1220"
)

func IsLink(raw string) bool {
	scheme, _, _ := strings.Cut(raw, ":")
	return strings.EqualFold(scheme, "magnet")
}

// Parse validates the exact topics and tracker hints without making any
// requests. The returned public URL omits all discovery hints.
func Parse(raw string) (source download.Magnet, name, publicURL string, err error) {
	invalid := func(message string) (download.Magnet, string, string, error) {
		return download.Magnet{}, "", "", errors.New(message)
	}
	if len(raw) > 32<<10 {
		return invalid("magnet link is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || !IsLink(raw) || u.Opaque != "" || u.Host != "" || u.User != nil || u.Path != "" || u.Fragment != "" {
		return invalid("enter a magnet link starting with magnet:?xt=")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid("magnet link has invalid parameters")
	}
	topics := query["xt"]
	if len(topics) == 0 || len(topics) > 2 {
		return invalid("magnet link must contain one v1 info hash, one v2 info hash, or both")
	}
	for _, topic := range topics {
		topic = strings.ToLower(topic)
		switch {
		case strings.HasPrefix(topic, v1Topic):
			if source.V1 != "" {
				return invalid("magnet link contains multiple BitTorrent v1 info hashes")
			}
			hash := topic[len(v1Topic):]
			if len(hash) == 32 {
				decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(hash))
				if err != nil {
					return invalid("magnet link has an invalid BitTorrent v1 info hash")
				}
				hash = hex.EncodeToString(decoded)
			}
			if !validHexHash(hash, 40) {
				return invalid("magnet info hash must be 40 hexadecimal or 32 base32 characters")
			}
			source.V1 = hash
		case strings.HasPrefix(topic, v2Topic):
			if source.V2 != "" {
				return invalid("magnet link contains multiple BitTorrent v2 info hashes")
			}
			hash, ok := strings.CutPrefix(topic, v2Topic+sha256MultihashPrefix)
			if !ok || !validHexHash(hash, 64) {
				return invalid("magnet link has an invalid BitTorrent v2 info hash; expected a full SHA-256 multihash")
			}
			source.V2 = hash
		default:
			return invalid("magnet link has an unsupported info hash; use a BitTorrent v1 or v2 link")
		}
	}
	if trackers := query["tr"]; len(trackers) > 32 {
		return invalid("magnet link has too many trackers")
	} else {
		for _, tracker := range trackers {
			u, err := url.Parse(tracker)
			if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
				return invalid("magnet link contains an invalid tracker URL")
			}
			if port := u.Port(); port != "" {
				if value, err := strconv.ParseUint(port, 10, 16); err != nil || value == 0 {
					return invalid("magnet tracker URL contains an invalid port")
				}
			}
			switch u.Scheme {
			case "http", "https":
			case "udp":
				if u.Port() == "" {
					return invalid("UDP tracker URL must include a port")
				}
			default:
				return invalid("magnet trackers must use HTTP, HTTPS, or UDP")
			}
			if !slices.Contains(source.Trackers, tracker) {
				source.Trackers = append(source.Trackers, tracker)
			}
		}
	}
	if len(source.Trackers) == 0 {
		return invalid("magnet link must include a tracker")
	}
	publicTopics := make([]string, 0, 2)
	if source.V1 != "" {
		publicTopics = append(publicTopics, v1Topic+source.V1)
	}
	if source.V2 != "" {
		publicTopics = append(publicTopics, v2Topic+sha256MultihashPrefix+source.V2)
	}
	return source, query.Get("dn"), "magnet:?xt=" + strings.Join(publicTopics, "&xt="), nil
}

func validHexHash(hash string, size int) bool {
	if len(hash) != size || strings.Trim(hash, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}
