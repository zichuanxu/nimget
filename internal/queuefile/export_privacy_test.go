package queuefile_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/queuefile"
)

func TestOrdinaryExportNeverSerializesSourceOrDerivedToken(t *testing.T) {
	const pathToken = "fixture-directory-ticket"
	const filenameToken = "fixture-terminal-ticket"
	items := []download.Item{{
		URL:         "https://fixture-user:fixture-password@example.invalid/" + pathToken + "/" + filenameToken + "?q=fixture-query#fixture-fragment",
		Destination: filepath.Join(t.TempDir(), filenameToken),
		Status:      download.StatusPaused,
		Headers:     map[string]string{"Cookie": "fixture-cookie", "Authorization": "fixture-bearer"},
	}}
	data, err := queuefile.ExportSummary(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{pathToken, filenameToken, "fixture-user", "fixture-password", "fixture-query", "fixture-fragment", "fixture-cookie", "fixture-bearer"} {
		if strings.Contains(string(data), marker) {
			t.Errorf("ordinary export exposed synthetic marker %q", marker)
		}
	}
	if reqs, err := queuefile.Import(data); err == nil || len(reqs) != 0 {
		t.Errorf("ordinary export accepted as executable download requests")
	}
}
