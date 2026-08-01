package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
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
			storefixture.PrepareCurrent(t, cfg.DBPath)

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

			if _, stderr, err := runRootCommandErr(t, root, args...); err != nil {
				t.Fatalf("stats %s failed: %v (stderr=%q)", args[1], err, stderr)
			}

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

func TestSemanticStatusReadOnlyOnPreRetrievalSchema(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("research:\n  semantic:\n    mode: on\n    model: test-embed\n    dimensions: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Clean(cfg.DBPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, source_key TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	before := readSchemaMetadata(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runRootCommandErr(t, root, "semantic", "status", "--json")
	if err != nil {
		t.Fatalf("semantic status: %v stderr=%q", err, stderr)
	}
	var status struct {
		Status    string `json:"status"`
		ProfileID string `json:"profile_id"`
		Mode      string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "unavailable" || status.Mode != "on" || status.ProfileID == "" {
		t.Fatalf("status=%+v output=%s", status, stdout)
	}
	db, err = sql.Open("sqlite", filepath.Clean(cfg.DBPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	after := readSchemaMetadata(t, db)
	if string(after) != string(before) {
		t.Fatalf("semantic status mutated schema\nbefore=%s\nafter=%s", before, after)
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
