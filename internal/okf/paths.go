package okf

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const maxPathComponentLength = 160

func ValidateConceptPath(rel string) error {
	if err := validateBundleRelPath(rel); err != nil {
		return err
	}
	base := path.Base(filepath.ToSlash(rel))
	switch strings.ToLower(base) {
	case "index.md", "log.md":
		return fmt.Errorf("reserved concept filename %q", base)
	}
	if path.Ext(base) != ".md" {
		return fmt.Errorf("concept path must end in .md: %s", rel)
	}
	return nil
}

func validateBundleRelPath(rel string) error {
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return fmt.Errorf("path is required")
	}
	if strings.Contains(trimmed, `\`) {
		return fmt.Errorf("path must use forward slashes: %s", rel)
	}
	if path.IsAbs(trimmed) || filepath.IsAbs(trimmed) {
		return fmt.Errorf("absolute path is not allowed: %s", rel)
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid path segment %q in %s", part, rel)
		}
		if len(part) > maxPathComponentLength {
			return fmt.Errorf("path segment too long in %s", rel)
		}
	}
	clean := path.Clean(trimmed)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return fmt.Errorf("path escapes bundle root: %s", rel)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid path segment %q in %s", part, rel)
		}
		if len(part) > maxPathComponentLength {
			return fmt.Errorf("path segment too long in %s", rel)
		}
	}
	return nil
}

func safeJoin(root, rel string) (string, error) {
	if err := validateBundleRelPath(rel); err != nil {
		return "", err
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve bundle root: %w", err)
	}
	if resolvedRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = resolvedRoot
	}
	full := filepath.Join(cleanRoot, filepath.FromSlash(path.Clean(rel)))
	within, err := isPathWithin(cleanRoot, full)
	if err != nil {
		return "", err
	}
	if !within {
		return "", fmt.Errorf("path escapes bundle root: %s", rel)
	}
	parent := filepath.Dir(full)
	if _, err := os.Lstat(parent); err == nil {
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return "", fmt.Errorf("resolve parent path %s: %w", parent, err)
		}
		within, err := isPathWithin(cleanRoot, resolvedParent)
		if err != nil {
			return "", err
		}
		if !within {
			return "", fmt.Errorf("path parent escapes bundle root through symlink: %s", rel)
		}
	}
	return full, nil
}

func isPathWithin(root, child string) (bool, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(rootAbs, childAbs)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false, nil
	}
	if runtime.GOOS == "windows" && strings.Contains(rel, ":") {
		return false, nil
	}
	return true, nil
}

func manifestCollisionKey(rel string) string {
	clean := path.Clean(filepath.ToSlash(strings.TrimSpace(rel)))
	return strings.ToLower(clean)
}
