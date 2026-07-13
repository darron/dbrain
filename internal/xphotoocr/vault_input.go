package xphotoocr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/vaultfs"
)

func materializeVaultPhoto(cfg config.Config, root *vaultfs.Root, localPath string, prefix string) (string, func(), error) {
	source, err := root.Open(localPath)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("vault media %q is not a regular file", localPath)
	}

	ext := filepath.Ext(localPath)
	if ext == "" {
		ext = ".img"
	}
	temp, err := cfg.CreateTemp(prefix + "-*" + ext)
	if err != nil {
		return "", nil, err
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if _, err := io.Copy(temp, source); err != nil {
		_ = temp.Close()
		cleanup()
		return "", nil, fmt.Errorf("copy vault media %q: %w", localPath, err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temporary vault media %q: %w", localPath, err)
	}
	return tempPath, cleanup, nil
}
