package runtimeenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	cleanup := RegisterConfigSnapshot(root, map[string]any{"test": map[string]any{"value": "snapshot"}}, nil)
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

func TestRegisteredConfigSnapshotOutOfOrderCleanupRestoresExactPreexistingRegistration(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.yaml")
	if err := os.WriteFile(original, []byte("DBRAIN_STACK_YAML: original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterConfigFile(root, original)
	preexisting, ok := registeredConfigFiles.Load(root)
	if !ok {
		t.Fatal("preexisting registration missing")
	}
	t.Cleanup(func() { registeredConfigFiles.Delete(root) })

	cleanupA := RegisterConfigSnapshot(root,
		map[string]any{"DBRAIN_STACK_YAML": "a-yaml"},
		map[string]string{"DBRAIN_STACK_DOTENV": "a-dotenv"})
	cleanupB := RegisterConfigSnapshot(root,
		map[string]any{"DBRAIN_STACK_YAML": "b-yaml"},
		map[string]string{"DBRAIN_STACK_DOTENV": "b-dotenv"})

	cleanupA()
	cleanupA()
	if got := FirstNonEmpty(root, "DBRAIN_STACK_YAML"); got != "b-yaml" {
		t.Fatalf("buried cleanup changed current YAML snapshot: %q", got)
	}
	if got := FirstNonEmpty(root, "DBRAIN_STACK_DOTENV"); got != "b-dotenv" {
		t.Fatalf("buried cleanup changed current dotenv snapshot: %q", got)
	}
	cleanupB()
	cleanupB()

	current, ok := registeredConfigFiles.Load(root)
	if !ok || current != preexisting {
		t.Fatalf("registration after cleanup = %#v, want exact preexisting %#v", current, preexisting)
	}
	if got := FirstNonEmpty(root, "DBRAIN_STACK_YAML"); got != "original" {
		t.Fatalf("restored YAML value = %q", got)
	}
	if got := FirstNonEmpty(root, "DBRAIN_STACK_DOTENV"); got != "" {
		t.Fatalf("stale dotenv snapshot leaked: %q", got)
	}
}

func TestRegisteredConfigSnapshotThreeLevelOutOfOrderCleanupLeavesNoStaleSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { registeredConfigFiles.Delete(root) })
	cleanupA := RegisterConfigSnapshot(root, map[string]any{"DBRAIN_STACK_VALUE": "a"}, nil)
	cleanupB := RegisterConfigSnapshot(root, map[string]any{"DBRAIN_STACK_VALUE": "b"}, nil)
	cleanupC := RegisterConfigSnapshot(root, map[string]any{"DBRAIN_STACK_VALUE": "c"}, nil)

	cleanupB()
	cleanupA()
	if got := FirstNonEmpty(root, "DBRAIN_STACK_VALUE"); got != "c" {
		t.Fatalf("top snapshot changed after buried cleanup: %q", got)
	}
	cleanupC()
	cleanupC()
	cleanupB()
	cleanupA()
	if _, ok := registeredConfigFiles.Load(root); ok {
		t.Fatal("all cleaned registrations must restore absence")
	}
	if got := FirstNonEmpty(root, "DBRAIN_STACK_VALUE"); got != "" {
		t.Fatalf("stale frozen snapshot leaked: %q", got)
	}
}

func TestRegisteredConfigSnapshotConcurrentBuriedCleanupRestoresPreexistingRegistration(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.yaml")
	if err := os.WriteFile(original, []byte("DBRAIN_STACK_VALUE: original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterConfigFile(root, original)
	preexisting, _ := registeredConfigFiles.Load(root)
	t.Cleanup(func() { registeredConfigFiles.Delete(root) })

	cleanupA := RegisterConfigSnapshot(root, map[string]any{"DBRAIN_STACK_VALUE": "a"}, nil)
	cleanupB := RegisterConfigSnapshot(root, map[string]any{"DBRAIN_STACK_VALUE": "b"}, nil)
	cleanupC := RegisterConfigSnapshot(root, map[string]any{"DBRAIN_STACK_VALUE": "c"}, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, cleanup := range []func(){cleanupA, cleanupB} {
		cleanup := cleanup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 10 {
				cleanup()
				_ = FirstNonEmpty(root, "DBRAIN_STACK_VALUE")
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := FirstNonEmpty(root, "DBRAIN_STACK_VALUE"); got != "c" {
		t.Fatalf("concurrent buried cleanup changed top snapshot: %q", got)
	}
	cleanupC()
	current, ok := registeredConfigFiles.Load(root)
	if !ok || current != preexisting {
		t.Fatalf("registration after concurrent cleanup = %#v, want %#v", current, preexisting)
	}
}

func TestRegisteredRuntimeSnapshotPreservesShellDotEnvAndYAMLPrecedence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte(strings.Join([]string{
		"DBRAIN_FROZEN_SHARED=from-envrc",
		"DBRAIN_FROZEN_ENVRC_ONLY=from-envrc",
		"DBRAIN_FROZEN_LIST=envrc-a,envrc-b",
		"DBRAIN_FROZEN_SECRET_REF=op://vault/item/field",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(strings.Join([]string{
		"DBRAIN_FROZEN_SHARED=from-env",
		"DBRAIN_FROZEN_ENV_ONLY=from-env",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dotenv, err := LoadDotEnvSnapshot(t.Context(), root, 4096)
	if err != nil {
		t.Fatal(err)
	}
	yaml := map[string]any{
		"DBRAIN_FROZEN_SHARED":    "from-yaml",
		"DBRAIN_FROZEN_YAML_ONLY": "from-yaml",
	}
	cleanup := RegisterConfigSnapshot(root, yaml, dotenv)
	defer cleanup()
	t.Setenv("DBRAIN_FROZEN_SHELL", "from-shell")

	for key, want := range map[string]string{
		"DBRAIN_FROZEN_SHELL":      "from-shell",
		"DBRAIN_FROZEN_SHARED":     "from-envrc",
		"DBRAIN_FROZEN_ENVRC_ONLY": "from-envrc",
		"DBRAIN_FROZEN_ENV_ONLY":   "from-env",
		"DBRAIN_FROZEN_YAML_ONLY":  "from-yaml",
		"DBRAIN_FROZEN_SECRET_REF": "op://vault/item/field",
	} {
		if got := FirstNonEmpty(root, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if got := LookupList(root, "DBRAIN_FROZEN_LIST"); len(got) != 2 || got[0] != "envrc-a" || got[1] != "envrc-b" {
		t.Fatalf("frozen list = %#v", got)
	}

	if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte("DBRAIN_FROZEN_SHARED=mutated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DBRAIN_FROZEN_ENV_ONLY=mutated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := FirstNonEmpty(root, "DBRAIN_FROZEN_SHARED"); got != "from-envrc" {
		t.Fatalf("frozen dotenv was reread: %q", got)
	}
	if got := FirstNonEmpty(root, "DBRAIN_FROZEN_ENV_ONLY"); got != "from-env" {
		t.Fatalf("frozen .env was reread: %q", got)
	}
}

func TestLoadDotEnvSnapshotAllowsAbsentFiles(t *testing.T) {
	got, err := LoadDotEnvSnapshot(t.Context(), t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestLoadDotEnvSnapshotRejectsUnsafeAndOversizedFilesAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix no-follow and FIFO semantics")
	}
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, []byte("DBRAIN_VALUE=target\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, ".envrc")); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDotEnvSnapshot(t.Context(), root, 1024); err == nil {
			t.Fatal("expected symlink rejection")
		}
	})
	t.Run("fifo", func(t *testing.T) {
		root := t.TempDir()
		if err := syscall.Mkfifo(filepath.Join(root, ".env"), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		started := time.Now()
		if _, err := LoadDotEnvSnapshot(ctx, root, 1024); err == nil {
			t.Fatal("expected FIFO rejection")
		}
		if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
			t.Fatalf("FIFO dotenv read blocked for %s", elapsed)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte(strings.Repeat("x", 1025)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDotEnvSnapshot(t.Context(), root, 1024); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("oversize error = %v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte("DBRAIN_VALUE=value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := LoadDotEnvSnapshot(ctx, root, 1024); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	})
}
