package runtimeenv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLoadConfigSnapshotBoundsAndParsesRegularYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("audit:\n  timeouts:\n    local_query: 3s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadConfigSnapshot(context.Background(), path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	value := snapshot["audit"].(map[string]any)["timeouts"].(map[string]any)["local_query"]
	if value != "3s" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1025)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigSnapshot(context.Background(), path, 1024); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestLoadConfigSnapshotRejectsSymlinkAndFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix no-follow and FIFO semantics")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(target, []byte("audit: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "symlink.yaml")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigSnapshot(context.Background(), symlink, 1024); err == nil {
		t.Fatal("expected symlink rejection")
	}
	fifo := filepath.Join(dir, "config.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := LoadConfigSnapshot(ctx, fifo, 1024); err == nil {
		t.Fatal("expected FIFO rejection")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("FIFO config read blocked for %s", elapsed)
	}
}

func TestRegisteredConfigSnapshotIsReadOnceAndCleanupRestoresPriorRegistration(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.yaml")
	if err := os.WriteFile(original, []byte("test:\n  value: original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterConfigFile(root, original)
	cleanup := RegisterConfigSnapshot(root, map[string]any{"test": map[string]any{"value": "snapshot"}})
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DBRAIN_TEST_VALUE=dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := FirstNonEmpty(root, "DBRAIN_TEST_VALUE"); got != "snapshot" {
		t.Fatalf("snapshot lookup = %q", got)
	}
	cleanup()
	cleanup()
	if got := FirstNonEmpty(root, "DBRAIN_TEST_VALUE"); got != "dotenv" {
		t.Fatalf("cleanup did not restore ordinary lookup = %q", got)
	}
}
