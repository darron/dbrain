package startuplog

import (
	"fmt"
	"io"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
)

func WriteVersion(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, version.StartupLine())
}

func MigrationReporter(w io.Writer) store.MigrationReporter {
	if w == nil {
		return nil
	}
	return func(event store.MigrationEvent) {
		switch event.Phase {
		case store.MigrationStarted:
			_, _ = fmt.Fprintf(w, "SQLite migration running schema_version=%d latest=%d name=%s\n", event.Version, event.LatestVersion, event.Name)
		case store.MigrationApplied:
			_, _ = fmt.Fprintf(w, "SQLite migration applied schema_version=%d latest=%d name=%s\n", event.Version, event.LatestVersion, event.Name)
		case store.MigrationFailed:
			_, _ = fmt.Fprintf(w, "SQLite migration failed schema_version=%d latest=%d name=%s error=%v\n", event.Version, event.LatestVersion, event.Name, event.Err)
		}
	}
}
