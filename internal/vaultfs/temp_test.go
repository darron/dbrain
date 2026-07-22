package vaultfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateTempCreatesPrivateConfinedFilesAndCleans(t *testing.T) {
	base := t.TempDir()
	tmp, err := NewPrivateTemp(base)
	if err != nil {
		t.Fatal(err)
	}
	dir := tmp.Dir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %o", info.Mode().Perm())
	}
	file, err := tmp.Create("candidate.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("private"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := tmp.Open("candidate.db")
	if err != nil {
		t.Fatal(err)
	}
	info, err = opened.Stat()
	_ = opened.Close()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
	if _, err := tmp.Create("../escape"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := tmp.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Cleanup(); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated directory remains: %v", err)
	}
}

func TestPrivateTempOpenRemainsBoundAfterGeneratedPathReplacement(t *testing.T) {
	base := t.TempDir()
	tmp, err := NewPrivateTemp(base)
	if err != nil {
		t.Fatal(err)
	}
	file, err := tmp.Create("candidate.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("confined"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	original := tmp.Dir()
	moved := original + "-moved"
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(base, "attacker")
	if err := os.Mkdir(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attacker, "candidate.db"), []byte("escaped"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attacker, original); err != nil {
		t.Fatal(err)
	}

	opened, err := tmp.Open("candidate.db")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(opened)
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Close()
	if string(data) != "confined" {
		t.Fatalf("opened replacement content %q", data)
	}
	if err := tmp.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(moved); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateTempRejectsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPrivateTemp(link); err == nil {
		t.Fatal("expected symlinked temporary root rejection")
	}
}

func TestPrivateTempRejectsIntermediateSymlinkBelowTrustedTempRoot(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real-parent")
	if err := os.MkdirAll(filepath.Join(realParent, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(base, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPrivateTemp(filepath.Join(linkedParent, "child")); err == nil {
		t.Fatal("expected intermediate symlink rejection")
	}
}
