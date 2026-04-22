package summarizecli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestRunRetriesDatabaseLocked(t *testing.T) {
	root := t.TempDir()
	countPath := filepath.Join(root, "count.txt")
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
count=0
if [ -f "` + countPath + `" ]; then
  count="$(cat "` + countPath + `")"
fi
count=$((count + 1))
printf '%s' "$count" > "` + countPath + `"
if [ "$count" -eq 1 ]; then
  echo "database is locked" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"https://example.com","title":"Example","description":"","siteName":"Example","content":"body"},"summary":null}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Binary:  binary,
		Input:   "https://example.com",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Extract.Status != "ok" {
		t.Fatalf("expected extract status ok, got %q", result.Extract.Status)
	}

	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatalf("read count: %v", err)
	}
	if string(data) != "2" {
		t.Fatalf("expected 2 attempts, got %q", string(data))
	}
}
