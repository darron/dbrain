package vaultfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRootContainedOperations(t *testing.T) {
	t.Parallel()

	vaultDir := t.TempDir()
	root, err := Open(vaultDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()

	if err := root.MkdirAll("items/nested", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := root.WriteFile("items/nested/note.md", []byte("contained"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := root.ReadFile("items/nested/note.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "contained" {
		t.Fatalf("ReadFile = %q", data)
	}
	info, err := root.Stat("items/nested/note.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("mode = %v, want regular file", info.Mode())
	}
	file, err := root.Open("items/nested/note.md")
	if err != nil {
		t.Fatalf("Open file: %v", err)
	}
	opened, err := io.ReadAll(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatalf("read opened file: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close opened file: %v", closeErr)
	}
	if string(opened) != "contained" {
		t.Fatalf("opened content = %q", opened)
	}
	if err := root.Remove("items/nested/note.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "items/nested/note.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("contained file still exists: %v", err)
	}
}

func TestRootRejectsBlankTraversalAndAbsoluteNames(t *testing.T) {
	t.Parallel()

	vaultDir := t.TempDir()
	outside := filepath.Join(filepath.Dir(vaultDir), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside sentinel: %v", err)
	}
	root, err := Open(vaultDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()

	for _, name := range []string{"", "   ", "../outside.txt", outside} {
		t.Run(name, func(t *testing.T) {
			if _, err := root.ReadFile(name); err == nil {
				t.Errorf("ReadFile(%q) succeeded", name)
			}
			if err := root.WriteFile(name, []byte("changed"), 0o600); err == nil {
				t.Errorf("WriteFile(%q) succeeded", name)
			}
			if _, err := root.Stat(name); err == nil {
				t.Errorf("Stat(%q) succeeded", name)
			}
			if err := root.Remove(name); err == nil {
				t.Errorf("Remove(%q) succeeded", name)
			}
		})
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside sentinel: %v", err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside sentinel changed to %q", data)
	}
}

func TestRootRejectsEscapingParentAndLeafSymlinks(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	vaultDir := filepath.Join(base, "vault")
	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	outside := filepath.Join(outsideDir, "sentinel.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside sentinel: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(vaultDir, "parent-link")); err != nil {
		t.Fatalf("symlink parent: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(vaultDir, "leaf-link")); err != nil {
		t.Fatalf("symlink leaf: %v", err)
	}

	root, err := Open(vaultDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()

	for _, name := range []string{"parent-link/sentinel.txt", "leaf-link"} {
		t.Run(name, func(t *testing.T) {
			if _, err := root.ReadFile(name); err == nil {
				t.Errorf("ReadFile(%q) followed escaping symlink", name)
			}
			if err := root.WriteFile(name, []byte("changed"), 0o600); err == nil {
				t.Errorf("WriteFile(%q) followed escaping symlink", name)
			}
			if _, err := root.Stat(name); err == nil {
				t.Errorf("Stat(%q) followed escaping symlink", name)
			}
		})
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside sentinel: %v", err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside sentinel changed to %q", data)
	}
}

func TestRootAllowsContainedRelativeSymlink(t *testing.T) {
	t.Parallel()

	vaultDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, "items"), 0o755); err != nil {
		t.Fatalf("mkdir items: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "items", "target.md"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink("target.md", filepath.Join(vaultDir, "items", "alias.md")); err != nil {
		t.Fatalf("symlink contained target: %v", err)
	}

	root, err := Open(vaultDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile("items/alias.md")
	if err != nil {
		t.Fatalf("ReadFile contained symlink: %v", err)
	}
	if string(data) != "inside" {
		t.Fatalf("contained symlink content = %q", data)
	}
}
