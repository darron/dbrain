package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func TestStatsCommandsReadOnly(t *testing.T) {
	tests := [][]string{
		{"stats", "activity", "--json"},
		{"stats", "items", "--json"},
		{"stats", "sources", "--json"},
		{"stats", "backlog", "--json"},
		{"stats", "pipeline", "--json"},
	}

	for _, args := range tests {
		t.Run(args[1], func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load(root)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("ensure dirs: %v", err)
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}
			if err := st.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}

			db, err := sql.Open("sqlite", filepath.Clean(cfg.DBPath))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			var current int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
				t.Fatalf("read current user_version: %v", err)
			}
			if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, current); err != nil {
				t.Fatalf("remove newest migration: %v", err)
			}
			if _, err := db.Exec(`PRAGMA user_version = ` + sqlInt(current-1)); err != nil {
				t.Fatalf("set legacy user_version: %v", err)
			}
			before := readSchemaMetadata(t, db)
			if err := db.Close(); err != nil {
				t.Fatalf("close fixture: %v", err)
			}

			_ = runRootCommand(t, root, args...)

			db, err = sql.Open("sqlite", filepath.Clean(cfg.DBPath))
			if err != nil {
				t.Fatalf("reopen fixture: %v", err)
			}
			defer func() { _ = db.Close() }()
			after := readSchemaMetadata(t, db)
			if string(after) != string(before) {
				t.Fatalf("stats %s mutated schema metadata\nbefore=%s\nafter=%s", args[1], before, after)
			}
		})
	}
}

func readSchemaMetadata(t *testing.T, db *sql.DB) []byte {
	t.Helper()
	var userVersion int
	if err := db.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), `SELECT version, name, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	type migration struct {
		Version   int
		Name      string
		AppliedAt string
	}
	var migrations []migration
	for rows.Next() {
		var row migration
		if err := rows.Scan(&row.Version, &row.Name, &row.AppliedAt); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		migrations = append(migrations, row)
	}
	payload, err := json.Marshal(struct {
		UserVersion int
		Migrations  []migration
	}{userVersion, migrations})
	if err != nil {
		t.Fatalf("marshal schema metadata: %v", err)
	}
	return payload
}

func sqlInt(value int) string {
	return fmt.Sprintf("%d", value)
}
