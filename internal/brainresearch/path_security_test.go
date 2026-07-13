package brainresearch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/model"
)

func TestExactTagEvidenceNotePathRejectsVaultEscape(t *testing.T) {
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
	if err := os.Symlink(outsideDir, filepath.Join(vaultDir, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, notePath := range []string{
		filepath.ToSlash(filepath.Join("..", filepath.Base(outsideDir), "sentinel.md")),
		"escape/sentinel.md",
	} {
		evidence := exactTagEvidenceFromItem(vaultDir, model.Item{
			SourceKey: "item:escape",
			NotePath:  notePath,
		}, model.SearchResult{}, "security", 1000, nil)
		if evidence.NotePath != "" {
			if data, err := os.ReadFile(evidence.NotePath); err == nil && string(data) == "outside sentinel" {
				t.Fatalf("research evidence exposed outside sentinel through %q", evidence.NotePath)
			}
			t.Fatalf("expected unsafe research note path to be omitted, got %q", evidence.NotePath)
		}
	}
}

func TestExactTagEvidenceNotePathAllowsContainedNote(t *testing.T) {
	vaultDir := t.TempDir()
	notePath := "items/contained.md"
	absolute := filepath.Join(vaultDir, filepath.FromSlash(notePath))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("contained"), 0o644); err != nil {
		t.Fatal(err)
	}

	evidence := exactTagEvidenceFromItem(vaultDir, model.Item{
		SourceKey: "item:contained",
		NotePath:  notePath,
	}, model.SearchResult{}, "security", 1000, nil)
	data, err := os.ReadFile(evidence.NotePath)
	if err != nil {
		t.Fatalf("read contained research note: %v", err)
	}
	if string(data) != "contained" {
		t.Fatalf("contained research evidence = %q", data)
	}
}
