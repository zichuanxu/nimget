// Package update finds stable NimGet releases and their verified package metadata.
package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/amemiya02/nimget/internal/download"
)

const ReleasesURL = "https://api.github.com/repos/amemiya02/nimget/releases?per_page=20"
const releaseBase = "https://github.com/amemiya02/nimget/releases"

// Interval is how often the desktop app checks.
const Interval = 24 * time.Hour

// Release is a published version.
type Release struct {
	Version string   `json:"version"`
	URL     string   `json:"url"`
	Package *Package `json:"package,omitempty"`
}

type Package struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

func CurrentPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func packageName(tag, platform string) string {
	switch platform {
	case "windows/amd64":
		return "NimGet-" + tag + "-windows-x64-setup.exe"
	case "darwin/arm64", "darwin/amd64":
		return "NimGet-" + tag + "-macos-universal.dmg"
	default:
		return ""
	}
}

// DownloadRequest derives the URL from the repository, tag and platform so
// neither API nor cached URLs can redirect the download to a different source.
func (r Release) DownloadRequest(platform string) (download.Request, error) {
	name := packageName(r.Version, platform)
	if name == "" || !stableTag(r.Version) || r.Package == nil || r.Package.Name != name || !validDigest(r.Package.Digest) {
		return download.Request{}, errors.New("there is no verified update package for this platform")
	}
	return download.Request{
		URL:      releaseBase + "/download/" + r.Version + "/" + name,
		Checksum: strings.ToLower(r.Package.Digest),
	}, nil
}

func validDigest(digest string) bool {
	raw, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(raw) != 64 {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func fetchReleases(ctx context.Context, client *http.Client, url, platform string) (State, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return State{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nimget-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return State{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return State{}, fmt.Errorf("release check: HTTP %s", resp.Status)
	}
	var body []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Package
			State string `json:"state"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 2<<20)).Decode(&body); err != nil {
		return State{}, fmt.Errorf("release check: %w", err)
	}
	s := State{Platform: platform}
	for _, entry := range body {
		if entry.Draft || entry.Prerelease || !stableTag(entry.TagName) {
			continue
		}
		rel := Release{Version: entry.TagName}
		if s.Latest.Version == "" || Newer(rel.Version, s.Latest.Version) {
			s.Latest = rel
		}
		if s.Compatible.Version != "" && !Newer(rel.Version, s.Compatible.Version) {
			continue
		}
		for _, asset := range entry.Assets {
			if asset.State == "uploaded" && asset.Name == packageName(rel.Version, platform) && asset.Name != "" && validDigest(asset.Digest) {
				rel.Package = &asset.Package
				s.Compatible = rel
				break
			}
		}
	}
	return s, nil
}

func stableTag(tag string) bool {
	v, ok := parse(tag)
	return ok && tag == fmt.Sprintf("v%d.%d.%d", v.nums[0], v.nums[1], v.nums[2])
}

// Versioned reports whether this build can compare its version to a release.
func Versioned(build string) bool {
	_, ok := parse(build)
	return ok
}

func (s State) selectRelease(current, platform string) (Release, bool) {
	for _, rel := range []Release{s.Compatible, s.Latest} {
		if !stableTag(rel.Version) || !Newer(rel.Version, current) {
			continue
		}
		if _, err := rel.DownloadRequest(platform); err != nil {
			rel.Package = nil
			if rel.Version != s.Latest.Version {
				continue
			}
		}
		rel.URL = releaseBase + "/tag/" + rel.Version
		return rel, true
	}
	return Release{}, false
}

// Newer reports whether candidate is a later version than current. Builds
// that are not versioned ("dev") never see updates.
func Newer(candidate, current string) bool {
	c, ok1 := parse(candidate)
	v, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if c.nums[i] != v.nums[i] {
			return c.nums[i] > v.nums[i]
		}
	}
	// 1.0.0 is newer than 1.0.0-rc.1; prereleases compare as text.
	switch {
	case c.pre == v.pre:
		return false
	case c.pre == "":
		return true
	case v.pre == "":
		return false
	}
	return c.pre > v.pre
}

type version struct {
	nums [3]int
	pre  string
}

// parse reads "v1.2.3" or "1.2.3-rc.1" (build metadata ignored).
func parse(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.nums[i] = n
	}
	v.pre = pre
	return v, true
}

// State remembers the last check so the app polls at most once per Interval.
type State struct {
	CheckedAt  time.Time `json:"checkedAt"`
	Platform   string    `json:"platform"`
	Latest     Release   `json:"latest"`
	Compatible Release   `json:"compatible"`
}

func LoadState(path string) State {
	var s State
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &s) != nil {
			return State{}
		}
	}
	return s
}

func SaveState(path string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Due reports whether a check is due at now.
func (s State) Due(now time.Time) bool {
	return now.Sub(s.CheckedAt) >= Interval || now.Before(s.CheckedAt)
}

// StatePath is the state file in dir.
func StatePath(dir string) string { return filepath.Join(dir, "update.json") }

var errDev = errors.New("development builds do not check for updates")

// Checker remembers successful checks on disk and in memory. Callers serialize
// automatic and manual checks on the same instance.
type Checker struct {
	Client   *http.Client
	URL      string
	Path     string
	Platform string
	last     State
}

// Check returns the newest release and whether it is newer than current.
func (c *Checker) Check(ctx context.Context, current string, now time.Time) (Release, bool, error) {
	return c.check(ctx, current, now, false)
}

// CheckNow looks for a release without waiting for the automatic interval.
func (c *Checker) CheckNow(ctx context.Context, current string, now time.Time) (Release, bool, error) {
	return c.check(ctx, current, now, true)
}

func (c *Checker) check(ctx context.Context, current string, now time.Time, force bool) (Release, bool, error) {
	if !Versioned(current) {
		return Release{}, false, errDev
	}
	platform := c.Platform
	if platform == "" {
		platform = CurrentPlatform()
	}
	s := LoadState(c.Path)
	if c.last.CheckedAt.After(s.CheckedAt) {
		s = c.last
	}
	if force || s.Platform != platform || s.Due(now) {
		var err error
		s, err = fetchReleases(ctx, c.Client, c.URL, platform)
		if err != nil {
			return Release{}, false, err
		}
		s.CheckedAt = now
		c.last = s
		_ = SaveState(c.Path, s) // the in-memory copy still throttles
	}
	rel, ok := s.selectRelease(current, platform)
	return rel, ok, nil
}
