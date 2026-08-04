//go:build !unix

package notify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type otherStateStoreFS struct {
	dir string
}

type otherStateReaderFS struct {
	logDir string
}

func openStateStoreFS(logDir string) (stateStoreFS, error) {
	if logDir == "" {
		return nil, fmt.Errorf("notification log directory is required")
	}
	dir := filepath.Join(logDir, "notifications")
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("notification directory is not a regular directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	return &otherStateStoreFS{dir: dir}, nil
}

func openStateReaderFS(logDir string) (stateFS, error) {
	if logDir == "" {
		return nil, fmt.Errorf("notification log directory is required")
	}
	return &otherStateReaderFS{logDir: logDir}, nil
}

func (f *otherStateStoreFS) ValidateStateLeaf() error {
	path := filepath.Join(f.dir, "state.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("notification state leaf is not regular")
	}
	return nil
}

func (f *otherStateStoreFS) ReadState(limit int64) ([]byte, error) {
	return readOtherState(filepath.Join(f.dir, "state.json"), limit)
}

func (f *otherStateReaderFS) ReadState(limit int64) ([]byte, error) {
	return readOtherState(filepath.Join(f.logDir, "notifications", "state.json"), limit)
}

func (*otherStateReaderFS) ReplaceState([]byte) error {
	return fmt.Errorf("notification state reader is read-only")
}

func readOtherState(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("notification state leaf is not regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return readBoundedState(file, limit)
}

func (f *otherStateStoreFS) ReplaceState(data []byte) error {
	if err := f.ValidateStateLeaf(); err != nil {
		return err
	}
	temp, err := os.CreateTemp(f.dir, ".state-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := syncNotificationStateFile(temp); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, filepath.Join(f.dir, "state.json")); err != nil {
		return err
	}
	dir, err := os.Open(f.dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return syncNotificationStateDirectory(dir.Sync)
}
