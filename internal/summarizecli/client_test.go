package summarizecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreferredCLIProviderUsesCLIState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	stateDir := filepath.Join(home, ".summarize")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir summarize dir: %v", err)
	}
	statePath := filepath.Join(stateDir, "cli-state.json")
	if err := os.WriteFile(statePath, []byte(`{"lastSuccessfulProvider":"claude"}`), 0o644); err != nil {
		t.Fatalf("write cli-state: %v", err)
	}

	if got := PreferredCLIProvider(); got != "claude" {
		t.Fatalf("expected claude provider, got %q", got)
	}
}

func TestPreferredCLIProviderFallsBackToCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := PreferredCLIProvider(); got != "codex" {
		t.Fatalf("expected codex fallback, got %q", got)
	}
}
