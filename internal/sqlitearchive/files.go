package sqlitearchive

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

func gzipFile(srcPath string, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer func() {
		_ = src.Close()
	}()
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	defer func() {
		_ = dst.Close()
	}()
	gw := gzip.NewWriter(dst)
	if _, err := io.Copy(gw, src); err != nil {
		_ = gw.Close()
		return fmt.Errorf("compress %s: %w", srcPath, err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("finish gzip %s: %w", dstPath, err)
	}
	return nil
}

func gunzipToFile(src io.Reader, dstPath string) error {
	gr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() {
		_ = gr.Close()
	}()
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	defer func() {
		_ = dst.Close()
	}()
	if _, err := io.Copy(dst, gr); err != nil {
		return fmt.Errorf("write decompressed sqlite database %s: %w", dstPath, err)
	}
	return nil
}

func copyToFile(src io.Reader, dstPath string) error {
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dstPath, err)
	}
	defer func() {
		_ = dst.Close()
	}()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	return nil
}

func moveExistingSQLiteFiles(dbPath string, now time.Time) ([]string, error) {
	suffix := ".pre-restore-" + now.UTC().Format(timestampLayout)
	var backups []string
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return backups, fmt.Errorf("stat existing sqlite file %s: %w", path, err)
		}
		backupPath := path + suffix
		if err := os.Rename(path, backupPath); err != nil {
			return backups, fmt.Errorf("move existing sqlite file %s to %s: %w", path, backupPath, err)
		}
		backups = append(backups, backupPath)
	}
	return backups, nil
}
