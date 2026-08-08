package bskyapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
)

const bskyStorageKey = "BSKY_STORAGE"

const maxSnapshotAttempts = 3

type levelDBFileState struct {
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	Hash    string
}

func isBSKYStorageKey(key string) bool {
	key = strings.TrimSpace(key)
	for _, origin := range []string{
		"_https://bsky.app\x00",
		"https://bsky.app\x00",
		"_https://bsky.app/\x00",
		"https://bsky.app/\x00",
	} {
		for _, marker := range []string{"", "\x01"} {
			if key == origin+marker+bskyStorageKey {
				return true
			}
		}
	}
	return false
}

func snapshotLevelDB(sourceDir string) (string, error) {
	sourceDir = filepath.Clean(sourceDir)
	if info, err := os.Stat(sourceDir); err != nil {
		return "", fmt.Errorf("stat Chrome local storage LevelDB: %w", err)
	} else if !info.IsDir() {
		return "", errors.New("chrome local storage LevelDB path is not a directory")
	}

	var lastErr error
	for attempt := 0; attempt < maxSnapshotAttempts; attempt++ {
		targetDir, err := os.MkdirTemp("", "dbrain-bsky-leveldb-")
		if err != nil {
			return "", fmt.Errorf("create LevelDB snapshot directory: %w", err)
		}

		before, err := levelDBFileStates(sourceDir)
		if err == nil {
			err = copyLevelDBFiles(sourceDir, targetDir, before)
		}
		if err == nil {
			after, stateErr := levelDBFileStates(sourceDir)
			if stateErr != nil {
				err = stateErr
			} else if !reflect.DeepEqual(before, after) {
				err = errors.New("chrome local storage LevelDB changed during snapshot")
			} else {
				return targetDir, nil
			}
		}

		removeSnapshot(targetDir)
		lastErr = err
		// Chrome may rotate or compact the database while it is being copied.
		// Treat any source-file error in this bounded window as transient; the
		// final error still preserves the last observed cause.
	}
	return "", fmt.Errorf("copy Chrome local storage LevelDB after %d attempts: %w", maxSnapshotAttempts, lastErr)
}

func levelDBFileStates(dir string) (map[string]levelDBFileState, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	states := make(map[string]levelDBFileState, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "LOCK" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		state := levelDBFileState{Size: info.Size(), Mode: info.Mode().Perm(), ModTime: info.ModTime()}
		if entry.Name() == "CURRENT" || strings.HasPrefix(entry.Name(), "MANIFEST-") {
			state.Hash, err = hashFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, err
			}
		}
		states[entry.Name()] = state
	}
	return states, nil
}

func copyLevelDBFiles(sourceDir, targetDir string, states map[string]levelDBFileState) error {
	for name, state := range states {
		sourcePath := filepath.Join(sourceDir, name)
		targetPath := filepath.Join(targetDir, name)
		if err := copyFile(sourcePath, targetPath, state.Mode); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(sourcePath, targetPath string, mode os.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	if err := target.Chmod(mode & 0o600); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readBSKYStorageFromLevelDB(dir string) ([]byte, error) {
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		return nil, fmt.Errorf("open Chrome local storage LevelDB snapshot: %w", err)
	}
	defer func() { _ = db.Close() }()

	iterator := db.NewIterator(nil, nil)
	defer iterator.Release()
	var state []byte
	for iterator.Next() {
		if !isBSKYStorageKey(string(iterator.Key())) {
			continue
		}
		value := normalizeBSKYStorageValue(iterator.Value())
		if state != nil && !bytes.Equal(state, value) {
			return nil, errors.New("multiple conflicting BSKY_STORAGE entries in Chrome local storage")
		}
		state = value
	}
	if err := iterator.Error(); err != nil {
		return nil, fmt.Errorf("iterate Chrome local storage LevelDB: %w", err)
	}
	if state == nil {
		return nil, errors.New("could not find BSKY_STORAGE in Chrome local storage")
	}
	return state, nil
}

func normalizeBSKYStorageValue(raw []byte) []byte {
	value := append([]byte(nil), raw...)
	if len(value) > 0 && value[0] == 0x01 {
		return value[1:]
	}
	return value
}

func removeSnapshot(path string) {
	if path != "" {
		_ = os.RemoveAll(path)
	}
}
