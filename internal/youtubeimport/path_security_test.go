package youtubeimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
)

func TestRemoveNoteFilesRejectsVaultEscape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		notePath func(t *testing.T, vaultDir, outsideDir string) string
	}{
		{
			name: "traversal",
			notePath: func(t *testing.T, vaultDir, outsideDir string) string {
				return filepath.ToSlash(filepath.Join("..", filepath.Base(outsideDir), "sentinel.md"))
			},
		},
		{
			name: "escaping parent symlink",
			notePath: func(t *testing.T, vaultDir, outsideDir string) string {
				if err := os.Symlink(outsideDir, filepath.Join(vaultDir, "escape")); err != nil {
					t.Fatalf("create symlink: %v", err)
				}
				return "escape/sentinel.md"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			vaultDir := filepath.Join(root, "vault")
			outsideDir := filepath.Join(root, "outside")
			if err := os.MkdirAll(vaultDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outsideDir, 0o755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(outsideDir, "sentinel.md")
			if err := os.WriteFile(sentinel, []byte("outside sentinel"), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg := config.Config{VaultDir: vaultDir}
			if err := removeNoteFiles(cfg, []string{tc.notePath(t, vaultDir, outsideDir)}); err == nil {
				t.Fatal("expected escaping note path to be rejected")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil {
				t.Fatalf("outside sentinel was deleted: %v", err)
			}
			if string(data) != "outside sentinel" {
				t.Fatalf("outside sentinel changed to %q", data)
			}
		})
	}
}

func TestRemoveNoteFilesRemovesContainedNote(t *testing.T) {
	vaultDir := t.TempDir()
	notePath := "items/contained.md"
	absolute := filepath.Join(vaultDir, filepath.FromSlash(notePath))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("contained"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeNoteFiles(config.Config{VaultDir: vaultDir}, []string{notePath}); err != nil {
		t.Fatalf("remove contained note: %v", err)
	}
	if _, err := os.Stat(absolute); !os.IsNotExist(err) {
		t.Fatalf("contained note still exists: %v", err)
	}
}
