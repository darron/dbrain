package xphotoocr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vaultfs"
)

func comparePhotoInputPath(ctx context.Context, cfg config.Config, opts CompareOptions, ref model.ItemMediaRef) (string, string, func(), error) {
	absolutePath := ""
	var localCleanup func()
	root, err := vaultfs.Open(cfg.VaultDir)
	if err == nil {
		absolutePath, localCleanup, _ = materializeVaultPhoto(cfg, root, ref.LocalPath, "x-photo-ocr-compare-local")
		_ = root.Close()
	}
	if absolutePath != "" {
		return absolutePath, "local", localCleanup, nil
	}
	if !opts.DownloadMissing {
		return absolutePath, "missing", nil, fmt.Errorf("local image is unavailable; rerun with --download-missing to fetch a temp copy")
	}
	remoteURL := strings.TrimSpace(ref.RemoteURL)
	if remoteURL == "" {
		return absolutePath, "missing", nil, fmt.Errorf("local image is unavailable and media has no remote_url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return absolutePath, "missing", nil, fmt.Errorf("build image download request: %w", err)
	}
	if strings.TrimSpace(opts.UserAgent) != "" {
		req.Header.Set("User-Agent", strings.TrimSpace(opts.UserAgent))
	} else {
		req.Header.Set("User-Agent", "dbrain-ocr-compare")
	}
	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return absolutePath, "missing", nil, fmt.Errorf("download temp image: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return absolutePath, "missing", nil, fmt.Errorf("download temp image %s: %s", remoteURL, resp.Status)
	}

	ext := filepath.Ext(ref.LocalPath)
	if ext == "" {
		ext = ".img"
	}
	file, err := cfg.CreateTemp("x-photo-ocr-compare-image-*" + ext)
	if err != nil {
		return absolutePath, "missing", nil, err
	}
	tempPath := file.Name()
	cleanup := func() {
		_ = os.Remove(tempPath)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, compareDownloadMaxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		cleanup()
		return tempPath, "temp_download", nil, fmt.Errorf("write temp image: %w", copyErr)
	}
	if closeErr != nil {
		cleanup()
		return tempPath, "temp_download", nil, fmt.Errorf("close temp image: %w", closeErr)
	}
	if written > compareDownloadMaxBytes {
		cleanup()
		return tempPath, "temp_download", nil, fmt.Errorf("downloaded image exceeds %d bytes", compareDownloadMaxBytes)
	}
	return tempPath, "temp_download", cleanup, nil
}
