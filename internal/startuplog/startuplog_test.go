package startuplog

import (
	"bytes"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/store"
)

func TestMigrationReporterWritesStartupSafeLines(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	reporter := MigrationReporter(&out)
	reporter(store.MigrationEvent{
		Phase:         store.MigrationStarted,
		Version:       2,
		LatestVersion: 3,
		Name:          "media_download_retry_state",
	})
	reporter(store.MigrationEvent{
		Phase:         store.MigrationApplied,
		Version:       2,
		LatestVersion: 3,
		Name:          "media_download_retry_state",
	})

	output := out.String()
	for _, want := range []string{
		"SQLite migration running schema_version=2 latest=3 name=media_download_retry_state",
		"SQLite migration applied schema_version=2 latest=3 name=media_download_retry_state",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("migration output = %q, want substring %q", output, want)
		}
	}
	if strings.Contains(output, " complete:") {
		t.Fatalf("migration output should not look like sync stage completion: %q", output)
	}
}
