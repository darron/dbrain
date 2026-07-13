package ask

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func TestEvidenceNotePathRejectsVaultEscape(t *testing.T) {
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
			name: "escaping symlink",
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
			if err := os.WriteFile(filepath.Join(outsideDir, "sentinel.md"), []byte("outside sentinel"), 0o644); err != nil {
				t.Fatal(err)
			}

			candidate := evidenceFromItem(config.Config{VaultDir: vaultDir}, model.Item{
				SourceKey: "item:escape",
				NotePath:  tc.notePath(t, vaultDir, outsideDir),
			}, model.SearchResult{}, 1000, nil)
			if candidate.NotePath != "" {
				if data, err := os.ReadFile(candidate.NotePath); err == nil && string(data) == "outside sentinel" {
					t.Fatalf("evidence exposed outside sentinel through %q", candidate.NotePath)
				}
				t.Fatalf("expected unsafe evidence note path to be omitted, got %q", candidate.NotePath)
			}
		})
	}
}

func TestEvidenceNotePathAllowsContainedNote(t *testing.T) {
	vaultDir := t.TempDir()
	notePath := "items/contained.md"
	absolute := filepath.Join(vaultDir, filepath.FromSlash(notePath))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("contained"), 0o644); err != nil {
		t.Fatal(err)
	}

	candidate := evidenceFromItem(config.Config{VaultDir: vaultDir}, model.Item{
		SourceKey: "item:contained",
		NotePath:  notePath,
	}, model.SearchResult{}, 1000, nil)
	data, err := os.ReadFile(candidate.NotePath)
	if err != nil {
		t.Fatalf("read contained evidence path: %v", err)
	}
	if string(data) != "contained" {
		t.Fatalf("contained evidence = %q", data)
	}
}
