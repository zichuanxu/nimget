package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"v1.0.1", "v1.0.0", true},
		{"v1.10.0", "v1.9.9", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.1", "v1.0.0", false},
		{"v2.0.0", "dev", false},
		{"garbage", "v1.0.0", false},
		{"1.2.3+meta", "1.2.2", true},
	}
	for _, c := range cases {
		if got := Newer(c.candidate, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.candidate, c.current, got)
		}
	}
}

func TestCheckPollsDaily(t *testing.T) {
	var hits atomic.Int64
	tag := "v1.2.0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"tag_name":"` + tag + `"}]`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "update.json")
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	c := &Checker{Client: server.Client(), URL: server.URL, Path: path}
	check := func(current string, at time.Time) (Release, bool, error) {
		return c.Check(context.Background(), current, at)
	}

	rel, ok, err := check("v1.1.0", now)
	if err != nil || !ok || rel.Version != "v1.2.0" {
		t.Fatalf("first check = %+v %v %v", rel, ok, err)
	}
	tag = "v1.3.0"
	if rel, _, _ := check("v1.1.0", now.Add(time.Hour)); rel.Version != "v1.2.0" || hits.Load() != 1 {
		t.Fatalf("polled again within a day: %+v, %d hits", rel, hits.Load())
	}
	if rel, _, _ := check("v1.1.0", now.Add(Interval)); rel.Version != "v1.3.0" || hits.Load() != 2 {
		t.Fatalf("daily check = %+v, %d hits", rel, hits.Load())
	}
	if _, ok, _ := check("v1.3.0", now.Add(Interval)); ok {
		t.Fatal("current version reported as an update")
	}
	if _, _, err := check("dev", now); err == nil {
		t.Fatal("dev build checked for updates")
	}
	tag = "v1.4.0"
	if rel, ok, err := c.CheckNow(context.Background(), "v1.3.0", now.Add(Interval)); err != nil || !ok || rel.Version != tag || hits.Load() != 3 {
		t.Fatalf("manual check = %+v, %v, %v, %d hits", rel, ok, err, hits.Load())
	}
}

func TestCheckSelectsVerifiedPackagesForTheCurrentPlatform(t *testing.T) {
	const digest = "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	type asset struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	}
	type release struct {
		Tag        string  `json:"tag_name"`
		Prerelease bool    `json:"prerelease,omitempty"`
		Draft      bool    `json:"draft,omitempty"`
		Assets     []asset `json:"assets,omitempty"`
		URL        string  `json:"html_url,omitempty"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]release{
			{Tag: "v3.1.0", Draft: true, Assets: []asset{{"NimGet-v3.1.0-windows-x64-setup.exe", "uploaded", digest, ""}}},
			{Tag: "v3.0.0-rc.1", Prerelease: true},
			{Tag: "v2.3.0", URL: "https://evil.test", Assets: []asset{
				{"NimGet-v2.3.0-windows-x64-setup.exe", "uploaded", "sha256:bad", ""},
				{"NimGet-v2.3.0-macos-universal.dmg", "uploaded", digest, "https://evil.test"},
			}},
			{Tag: "v2.0.0", Assets: []asset{{"NimGet-v2.0.0-windows-x64-setup.exe", "uploaded", digest, "https://evil.test"}}},
			{Tag: "v2.1.0", Assets: []asset{{"NimGet-v2.1.0-windows-x64-setup.exe", "new", digest, ""}}},
		})
	}))
	t.Cleanup(server.Close)

	for _, tt := range []struct {
		platform, current, want, name string
	}{
		{"windows/amd64", "v1.9.0", "v2.0.0", "NimGet-v2.0.0-windows-x64-setup.exe"},
		{"windows/amd64", "v2.0.0", "v2.3.0", ""},
		{"darwin/arm64", "v1.9.0", "v2.3.0", "NimGet-v2.3.0-macos-universal.dmg"},
		{"darwin/amd64", "v1.9.0", "v2.3.0", "NimGet-v2.3.0-macos-universal.dmg"},
		{"linux/amd64", "v1.9.0", "v2.3.0", ""},
	} {
		t.Run(tt.platform+"/"+tt.current, func(t *testing.T) {
			c := &Checker{Client: server.Client(), URL: server.URL, Path: filepath.Join(t.TempDir(), "update.json"), Platform: tt.platform}
			rel, newer, err := c.Check(context.Background(), tt.current, time.Now())
			if err != nil || !newer || rel.Version != tt.want || rel.URL != "https://github.com/amemiya02/nimget/releases/tag/"+tt.want {
				t.Fatalf("release = %+v, %v, %v", rel, newer, err)
			}
			req, err := rel.DownloadRequest(tt.platform)
			if tt.name == "" {
				if rel.Package != nil || err == nil {
					t.Fatalf("unexpected download request: %+v, %v", req, err)
				}
			} else if err != nil || req.URL != "https://github.com/amemiya02/nimget/releases/download/"+tt.want+"/"+tt.name ||
				req.Checksum != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
				t.Fatalf("download request = %+v, %v", req, err)
			}
		})
	}
}

func TestDownloadRequestRejectsUntrustedCacheEntries(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, rel := range []Release{
		{Version: "v2.0.0", Package: &Package{Name: "../setup.exe", Digest: digest}},
		{Version: "v2.0.0", Package: &Package{Name: "NimGet-v2.0.0-windows-x64-setup.exe"}},
		{Version: "v2.0.0-../bad", Package: &Package{Name: "NimGet-v2.0.0-../bad-windows-x64-setup.exe", Digest: digest}},
	} {
		if req, err := rel.DownloadRequest("windows/amd64"); err == nil {
			t.Errorf("invalid cached release %+v accepted as %+v", rel, req)
		}
	}
}

// With an unwritable state file the in-memory copy still keeps the check
// weekly, and the release is still reported.
func TestCheckWithUnwritableState(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"tag_name":"v2.0.0"}]`))
	}))
	defer server.Close()
	c := &Checker{Client: server.Client(), URL: server.URL, Path: filepath.Join(t.TempDir(), "missing-dir", "update.json")}
	now := time.Now()
	for i := range 3 {
		rel, ok, err := c.Check(context.Background(), "v1.0.0", now.Add(time.Duration(i)*time.Hour))
		if err != nil || !ok || rel.Version != "v2.0.0" {
			t.Fatalf("check %d = %+v %v %v", i, rel, ok, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("polled %d times within a day", hits.Load())
	}
}
