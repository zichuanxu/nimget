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

const v1Topic = "urn:btih:"

func IsLink(raw string) bool {
	scheme, _, _ := strings.Cut(raw, ":")
	return strings.EqualFold(scheme, "magnet")
}

// Parse validates the supported v1 topic and tracker hints without making any
// requests. The returned public URL omits all discovery hints.
func Parse(raw string) (source download.Magnet, name, publicURL string, err error) {
	invalid := func(message string) (download.Magnet, string, string, error) {
		return download.Magnet{}, "", "", errors.New(message)
	}
	if len(raw) > 32<<10 {
		return invalid("magnet link is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host != "" || u.User != nil || u.Path != "" || u.Fragment != "" {
		return invalid("enter a magnet link starting with magnet:?xt=")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid("magnet link has invalid parameters")
	}
	topics := query["xt"]
	if len(topics) != 1 || !strings.HasPrefix(strings.ToLower(topics[0]), v1Topic) {
		return invalid("magnet link must contain one BitTorrent v1 info hash")
	}
	hash := topics[0][len(v1Topic):]
	var bytes []byte
	switch len(hash) {
	case 40:
		bytes, err = hex.DecodeString(hash)
	case 32:
		bytes, err = base32.StdEncoding.DecodeString(strings.ToUpper(hash))
	default:
		return invalid("magnet info hash must be 40 hexadecimal or 32 base32 characters")
	}
	if err != nil || strings.Trim(hex.EncodeToString(bytes), "0") == "" {
		return invalid("magnet link has an invalid BitTorrent v1 info hash")
	}
	source.V1 = hex.EncodeToString(bytes)
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
	return source, query.Get("dn"), "magnet:?xt=" + v1Topic + source.V1, nil
}
