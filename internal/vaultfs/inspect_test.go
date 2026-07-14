package vaultfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRootInspectReturnsSanitizedMetadata(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	vault := filepath.Join(base, "vault")
	outside := filepath.Join(base, "outside.txt")
	if err := os.MkdirAll(filepath.Join(vault, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, "notes", "note.md"), []byte("secret evidence"), 0o600); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, "secret.md"), []byte("normalized alternate target"), 0o600); err != nil {
		t.Fatalf("write alternate target: %v", err)
	}
	unreadable := filepath.Join(vault, "notes", "unreadable.md")
	if err := os.WriteFile(unreadable, []byte("secret"), 0o000); err != nil {
		t.Fatalf("write unreadable note: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink("note.md", filepath.Join(vault, "notes", "alias.md")); err != nil {
		t.Fatalf("contained symlink: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(vault, "escape")); err != nil {
		t.Fatalf("escaping symlink: %v", err)
	}
	outsideDir := filepath.Join(base, "outside-dir")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "nested.md"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside nested: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(vault, "escape-parent")); err != nil {
		t.Fatalf("escaping parent symlink: %v", err)
	}

	root, err := Open(vault)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()
	for _, name := range []string{"notes/note.md", "notes/alias.md"} {
		got, err := root.Inspect(name)
		if err != nil {
			t.Fatalf("Inspect(%q): %v", name, err)
		}
		if !got.Exists || !got.Regular || got.SizeBytes != int64(len("secret evidence")) {
			t.Fatalf("Inspect(%q) = %+v", name, got)
		}
	}

	assertLogicalFileErrorCode(t, root, "", "outside_root")
	assertLogicalFileErrorCode(t, root, outside, "outside_root")
	assertLogicalFileErrorCode(t, root, "../outside.txt", "outside_root")
	assertLogicalFileErrorCode(t, root, "notes/../secret.md", "outside_root")
	assertLogicalFileErrorCode(t, root, "notes/./note.md", "outside_root")
	assertLogicalFileErrorCode(t, root, "./secret.md", "outside_root")
	assertLogicalFileErrorCode(t, root, "missing.md", "missing")
	assertLogicalFileErrorCode(t, root, "notes/unreadable.md", "unreadable")
	assertLogicalFileErrorCode(t, root, "escape", "symlink_rejected")
	assertLogicalFileErrorCode(t, root, "escape-parent/nested.md", "symlink_rejected")
}

func TestRootInspectNamedPipeDoesNotBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.md"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	root, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	type result struct {
		metadata LogicalFileMetadata
		err      error
	}
	done := make(chan result, 1)
	go func() {
		metadata, inspectErr := root.Inspect("pipe.md")
		done <- result{metadata: metadata, err: inspectErr}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Inspect FIFO: %v", got.err)
		}
		if !got.metadata.Exists || got.metadata.Regular {
			t.Fatalf("Inspect FIFO metadata = %+v", got.metadata)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Inspect blocked opening a named pipe")
	}
}

func assertLogicalFileErrorCode(t *testing.T, root *Root, name string, code string) {
	t.Helper()
	_, err := root.Inspect(name)
	if err == nil {
		t.Fatalf("Inspect(%q) succeeded", name)
	}
	var logical *LogicalFileError
	if !errors.As(err, &logical) || logical.Code != code {
		t.Fatalf("Inspect(%q) error = %#v, want code %q", name, err, code)
	}
	if err.Error() != code {
		t.Fatalf("Inspect(%q) leaked detail: %q", name, err)
	}
}
