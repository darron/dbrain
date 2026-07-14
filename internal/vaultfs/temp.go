package vaultfs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PrivateTemp is a generated, root-confined temporary directory. It only
// creates private regular files and removes the entire generated directory on
// cleanup.
type PrivateTemp struct {
	mu      sync.Mutex
	base    string
	name    string
	parent  *os.Root
	root    *os.Root
	cleaned bool
}

// NewPrivateTemp creates a generated 0700 directory below an existing,
// non-symlinked temporary root.
func NewPrivateTemp(base string) (*PrivateTemp, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil, fmt.Errorf("temporary root is required")
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("resolve temporary root: %w", err)
	}
	resolved, err := noFollowTempPath(abs)
	if err != nil {
		return nil, err
	}
	parent, err := openRootNoFollow(resolved)
	if err != nil {
		return nil, fmt.Errorf("open temporary root: %w", err)
	}
	for attempt := 0; attempt < 32; attempt++ {
		name, randomErr := generatedTempName()
		if randomErr != nil {
			_ = parent.Close()
			return nil, randomErr
		}
		if err := parent.Mkdir(name, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			_ = parent.Close()
			return nil, fmt.Errorf("create private temporary directory: %w", err)
		}
		root, err := parent.OpenRoot(name)
		if err != nil {
			_ = parent.RemoveAll(name)
			_ = parent.Close()
			return nil, fmt.Errorf("open private temporary directory: %w", err)
		}
		return &PrivateTemp{base: abs, name: name, parent: parent, root: root}, nil
	}
	_ = parent.Close()
	return nil, fmt.Errorf("create private temporary directory: name collision budget exhausted")
}

func noFollowTempPath(path string) (string, error) {
	path = filepath.Clean(path)
	trustedSystemTemp := filepath.Clean(os.TempDir())
	if path == trustedSystemTemp || strings.HasPrefix(path, trustedSystemTemp+string(filepath.Separator)) {
		resolved, err := filepath.EvalSymlinks(trustedSystemTemp)
		if err != nil {
			return "", fmt.Errorf("resolve trusted system temporary root: %w", err)
		}
		relative, err := filepath.Rel(trustedSystemTemp, path)
		if err != nil {
			return "", fmt.Errorf("resolve temporary root below trusted system root: %w", err)
		}
		path = filepath.Join(resolved, relative)
	}
	return path, nil
}

func generatedTempName() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate private temporary directory name: %w", err)
	}
	return "dbrain-audit-" + hex.EncodeToString(value[:]), nil
}

func privateTempName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || filepath.IsAbs(name) || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("temporary file name is not confined")
	}
	return name, nil
}

// Create creates a new 0600 file directly beneath the generated directory.
func (t *PrivateTemp) Create(name string) (*os.File, error) {
	name, err := privateTempName(name)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cleaned || t.root == nil {
		return nil, fmt.Errorf("private temporary directory is closed")
	}
	file, err := t.root.OpenFile(name, privateCreateFlags(), 0o600)
	if err != nil {
		return nil, fmt.Errorf("create private temporary file: %w", err)
	}
	return file, nil
}

// Open opens an existing regular file through the held root capability. It
// never resolves the generated directory or file again by pathname.
func (t *PrivateTemp) Open(name string) (*os.File, error) {
	name, err := privateTempName(name)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cleaned || t.root == nil {
		return nil, fmt.Errorf("private temporary directory is closed")
	}
	file, err := t.root.OpenFile(name, privateOpenFlags(), 0)
	if err != nil {
		return nil, fmt.Errorf("open private temporary file: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect private temporary file: %w", err)
		}
		return nil, fmt.Errorf("private temporary file is not regular")
	}
	return file, nil
}

// Dir returns the generated directory path for local cleanup diagnostics only.
func (t *PrivateTemp) Dir() string { return filepath.Join(t.base, t.name) }

// AvailableBytes returns the current free-space estimate for the temp volume.
func (t *PrivateTemp) AvailableBytes() (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cleaned || t.root == nil {
		return 0, fmt.Errorf("private temporary directory is closed")
	}
	return availableBytes(t.root)
}

// Cleanup closes capabilities and removes the generated directory. It is safe
// to call more than once.
func (t *PrivateTemp) Cleanup() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cleaned {
		return nil
	}
	t.cleaned = true
	var first error
	if t.root != nil {
		if err := t.root.Close(); err != nil {
			first = err
		}
		t.root = nil
	}
	if t.parent != nil {
		if err := t.parent.RemoveAll(t.name); err != nil && first == nil {
			first = err
		}
		if err := t.parent.Close(); err != nil && first == nil {
			first = err
		}
		t.parent = nil
	}
	if first != nil {
		return fmt.Errorf("clean private temporary directory: %w", first)
	}
	return nil
}
