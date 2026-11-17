package queuefile

import (
	"errors"
	"strings"
	"testing"

	"github.com/amemiya02/nimget/internal/download"
)

func TestExportImportRoundTrip(t *testing.T) {
	items := []download.Item{
		{URL: "https://x/a%2Fb?key=literal#frag", Destination: "/d/a", Status: download.StatusQueued, Priority: 1, SpeedLimit: 5, Headers: map[string]string{"Cookie": "s=1"}},
		{URL: "https://x/b", Destination: "/d/b", Status: download.StatusCompleted},
		{URL: "https://x/c", Destination: "/d/c", Status: download.StatusFailed, Checksum: "sha256:00"},
	}
	data, err := ExportSources(items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s=1") {
		t.Fatal("source backup exported request headers")
	}
	reqs, err := Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 || reqs[0].URL != "https://x/a%2Fb?key=literal#frag" || reqs[0].Priority != 1 || reqs[0].SpeedLimit != 5 || reqs[1].Checksum != "sha256:00" {
		t.Fatalf("imported %+v", reqs)
	}
}

func TestImportURLList(t *testing.T) {
	reqs, err := Import([]byte("# my list\nhttps://x/1\n\n  https://x/2  \r\n"))
	if err != nil || len(reqs) != 2 || reqs[1].URL != "https://x/2" {
		t.Fatalf("got %+v, %v", reqs, err)
	}
	for _, bad := range []string{"", "# only comments", `{"version":9,"downloads":[]}`, `{"version":1,"extra":1}`} {
		if _, err := Import([]byte(bad)); err == nil {
			t.Errorf("Import(%q) accepted", bad)
		}
	}
}

func TestLegacyQueueURLsRemainReadable(t *testing.T) {
	legacy := []byte(`{"version":1,"downloads":[{"url":"https://x/old?ticket=abc","headers":{"Cookie":"x=1"}}]}`)
	if _, err := Import(legacy); !errors.Is(err, ErrLegacyURLs) {
		t.Fatalf("legacy parse without acknowledgement: %v", err)
	}
	reqs, err := ImportLegacyURLs(legacy)
	if err != nil || len(reqs) != 1 || reqs[0].URL != "https://x/old?ticket=abc" || reqs[0].Headers["Cookie"] != "x=1" {
		t.Fatalf("legacy requests = %+v, %v", reqs, err)
	}
}

func TestBackupRejectsInjectedHeadersWithoutEchoingInput(t *testing.T) {
	for _, field := range []string{"headers", "websiteSession", "secret-field"} {
		_, err := Import([]byte(`{"version":2,"mode":"source-backup","downloads":[{"url":"https://x/secret","` + field + `":{}}]}`))
		if err == nil || err.Error() != "invalid queue file" {
			t.Fatalf("import of %s = %v", field, err)
		}
	}
}
