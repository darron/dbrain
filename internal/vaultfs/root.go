// Package vaultfs provides capability-scoped access to files beneath a vault.
package vaultfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root confines filesystem operations to a vault directory. Path containment,
// including symlink resolution, is enforced by os.Root at the operation.
type Root struct {
	root *os.Root
}

// Open opens dir as a capability root.
func Open(dir string) (*Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open vault root: %w", err)
	}
	return &Root{root: root}, nil
}

// Close releases the operating-system handle for the root.
func (r *Root) Close() error {
	return r.root.Close()
}

// Open opens name for reading beneath the root.
func (r *Root) Open(name string) (*os.File, error) {
	name, err := rootName(name)
	if err != nil {
		return nil, err
	}
	return r.root.Open(name)
}

// ReadFile reads name beneath the root.
func (r *Root) ReadFile(name string) ([]byte, error) {
	name, err := rootName(name)
	if err != nil {
		return nil, err
	}
	return r.root.ReadFile(name)
}

// WriteFile writes name beneath the root.
func (r *Root) WriteFile(name string, data []byte, perm os.FileMode) error {
	name, err := rootName(name)
	if err != nil {
		return err
	}
	return r.root.WriteFile(name, data, perm)
}

// MkdirAll creates name and missing parents beneath the root.
func (r *Root) MkdirAll(name string, perm os.FileMode) error {
	name, err := rootName(name)
	if err != nil {
		return err
	}
	return r.root.MkdirAll(name, perm)
}

// Remove removes name beneath the root.
func (r *Root) Remove(name string) error {
	name, err := rootName(name)
	if err != nil {
		return err
	}
	return r.root.Remove(name)
}

// Stat returns information about name beneath the root.
func (r *Root) Stat(name string) (os.FileInfo, error) {
	name, err := rootName(name)
	if err != nil {
		return nil, err
	}
	return r.root.Stat(name)
}

func rootName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("vault path is empty")
	}
	return filepath.FromSlash(name), nil
}
