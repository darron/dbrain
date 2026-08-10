package metrics

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func rotatedMetricsPath(path string, suffix int) string {
	return fmt.Sprintf("%s.%d", filepath.Clean(path), suffix)
}

func rotateActive(path string, keep int) error {
	if keep < 0 || keep > maxRotateKeepFiles {
		return fmt.Errorf("invalid metrics.rotate_keep_files %d; expected an integer from 0 through %d", keep, maxRotateKeepFiles)
	}
	if err := cleanupRotatedBackups(path, keep); err != nil {
		return err
	}
	if keep == 0 {
		if err := removeRegularFile(path); err != nil {
			return fmt.Errorf("remove active metrics file: %w", err)
		}
		return nil
	}
	for suffix := keep - 1; suffix >= 1; suffix-- {
		if err := moveRotatedBackup(path, suffix, suffix+1); err != nil {
			return err
		}
	}
	if err := moveActiveMetricsFile(path, rotatedMetricsPath(path, 1)); err != nil {
		return err
	}
	return nil
}

func cleanupRotatedBackups(path string, keep int) error {
	for suffix := maxRotateKeepFiles; suffix > keep; suffix-- {
		if err := removeRegularFile(rotatedMetricsPath(path, suffix)); err != nil {
			return fmt.Errorf("remove expired metrics backup %s: %w", rotatedMetricsPath(path, suffix), err)
		}
	}
	return nil
}

func removeRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return os.Remove(path)
}

func moveRotatedBackup(path string, fromSuffix, toSuffix int) error {
	source := rotatedMetricsPath(path, fromSuffix)
	destination := rotatedMetricsPath(path, toSuffix)
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect metrics backup %s: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("metrics backup %s is not a regular file", source)
	}
	if err := removeRotationDestination(destination); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("rename metrics backup %s to %s: %w", source, destination, err)
	}
	return nil
}

func moveActiveMetricsFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect active metrics file %s: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("active metrics path %s is not a regular file", source)
	}
	if err := removeRotationDestination(destination); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("rename active metrics file %s to %s: %w", source, destination, err)
	}
	return nil
}

func removeRotationDestination(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect metrics rotation destination %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("metrics rotation destination %s is not a regular file", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove metrics rotation destination %s: %w", path, err)
	}
	return nil
}

func canonicalRotatedSuffix(path string, name string) (int, bool) {
	prefix := filepath.Base(path) + "."
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return 0, false
	}
	raw := name[len(prefix):]
	if len(raw) > 1 && raw[0] == '0' {
		return 0, false
	}
	suffix, err := strconv.Atoi(raw)
	if err != nil || suffix < 1 || suffix > maxRotateKeepFiles || strconv.Itoa(suffix) != raw {
		return 0, false
	}
	return suffix, true
}
