package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
)

func TestPersistedNoteWritesRejectVaultEscape(t *testing.T) {
	operations := []struct {
		name         string
		contained    string
		writeForPath func(config.Config, string) error
	}{
		{
			name:      "item",
			contained: "items/security/contained.md",
			writeForPath: func(cfg config.Config, notePath string) error {
				return WriteItem(cfg, model.Item{SourceKey: "item:security", Title: "Contained item", LinksJSON: "[]", NotePath: notePath})
			},
		},
		{
			name:      "source",
			contained: "sources/security/contained.md",
			writeForPath: func(cfg config.Config, notePath string) error {
				return WriteSource(cfg, model.SourceDocument{SourceKey: "src:security", Title: "Contained source", NotePath: notePath}, nil)
			},
		},
		{
			name:      "entity",
			contained: "entities/security/contained.md",
			writeForPath: func(cfg config.Config, notePath string) error {
				return WriteEntity(cfg, entities.Entity{Key: "project:security", Name: "Contained entity", Kind: entities.KindProject, NotePath: notePath})
			},
		},
	}

	for _, operation := range operations {
		operation := operation
		t.Run(operation.name+" contained", func(t *testing.T) {
			cfg := testVaultSecurityConfig(t)
			if err := operation.writeForPath(cfg, operation.contained); err != nil {
				t.Fatalf("contained write: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.VaultDir, filepath.FromSlash(operation.contained))); err != nil {
				t.Fatalf("contained note missing: %v", err)
			}
		})

		for _, escape := range []struct {
			name string
			path func(t *testing.T, cfg config.Config) (string, string)
		}{
			{
				name: "traversal",
				path: func(t *testing.T, cfg config.Config) (string, string) {
					t.Helper()
					outside := filepath.Join(filepath.Dir(cfg.VaultDir), operation.name+"-outside.md")
					rel, err := filepath.Rel(cfg.VaultDir, outside)
					if err != nil {
						t.Fatalf("relative outside path: %v", err)
					}
					return filepath.ToSlash(rel), outside
				},
			},
			{
				name: "escaping parent symlink",
				path: func(t *testing.T, cfg config.Config) (string, string) {
					t.Helper()
					outsideDir := filepath.Join(filepath.Dir(cfg.VaultDir), operation.name+"-outside-dir")
					if err := os.MkdirAll(outsideDir, 0o755); err != nil {
						t.Fatalf("mkdir outside: %v", err)
					}
					if err := os.Symlink(outsideDir, filepath.Join(cfg.VaultDir, "escape")); err != nil {
						t.Fatalf("symlink outside dir: %v", err)
					}
					return "escape/outside.md", filepath.Join(outsideDir, "outside.md")
				},
			},
		} {
			escape := escape
			t.Run(operation.name+" "+escape.name, func(t *testing.T) {
				cfg := testVaultSecurityConfig(t)
				notePath, outside := escape.path(t, cfg)
				if err := os.WriteFile(outside, []byte("outside sentinel"), 0o600); err != nil {
					t.Fatalf("write outside sentinel: %v", err)
				}
				if err := operation.writeForPath(cfg, notePath); err == nil {
					t.Error("expected escaping note path to be rejected")
				}
				data, err := os.ReadFile(outside)
				if err != nil {
					t.Fatalf("read outside sentinel: %v", err)
				}
				if string(data) != "outside sentinel" {
					t.Fatalf("outside sentinel changed to %q", data)
				}
			})
		}
	}
}

func testVaultSecurityConfig(t *testing.T) config.Config {
	t.Helper()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return cfg
}
