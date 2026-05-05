package mediadownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func downloadRef(ctx context.Context, client *http.Client, cfg config.Config, ref model.ItemMediaRef) (model.MediaDownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.RemoteURL, nil)
	if err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media request %q: %w", ref.RemoteURL, err)
	}
	req.Header.Set("user-agent", "dbrain/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return model.MediaDownloadResult{
			Status: "error",
			Error:  err.Error(),
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return model.MediaDownloadResult{
			Status: "gone",
			Error:  fmt.Sprintf("media returned status=%d", resp.StatusCode),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.MediaDownloadResult{
			Status: "error",
			Error:  fmt.Sprintf("media returned status=%d", resp.StatusCode),
		}, nil
	}

	contentType := resp.Header.Get("content-type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	if strings.HasPrefix(mediaType, "text/html") {
		return model.MediaDownloadResult{
			Status: "error",
			Error:  "media request returned HTML instead of media bytes",
		}, nil
	}

	tmpDir := filepath.Join(cfg.MediaDir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media temp dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(tmpDir, "download-*")
	if err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body)
	if copyErr != nil {
		return model.MediaDownloadResult{
			Status: "error",
			Error:  copyErr.Error(),
		}, nil
	}
	if err := tmpFile.Close(); err != nil {
		return model.MediaDownloadResult{
			Status: "error",
			Error:  err.Error(),
		}, nil
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	ext := mediaExtension(mediaType, ref.RemoteURL)
	relPath := filepath.ToSlash(filepath.Join("media", "x", normalizedMediaType(ref.MediaType), sum[:2], sum+ext))
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media target dir: %w", err)
	}

	if _, err := os.Stat(fullPath); err == nil {
		return model.MediaDownloadResult{
			MIMEType:     mediaType,
			ByteSize:     written,
			ContentHash:  "sha256:" + sum,
			LocalPath:    relPath,
			Status:       "downloaded",
			DownloadedAt: time.Now().UTC(),
		}, nil
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("move media into vault: %w", err)
	}

	return model.MediaDownloadResult{
		MIMEType:     mediaType,
		ByteSize:     written,
		ContentHash:  "sha256:" + sum,
		LocalPath:    relPath,
		Status:       "downloaded",
		DownloadedAt: time.Now().UTC(),
	}, nil
}

func normalizedMediaType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "photo", "video", "animated_gif":
		return value
	default:
		return "unknown"
	}
}

func mediaExtension(mediaType, rawURL string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	}

	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}

	parsed, err := neturl.Parse(rawURL)
	if err == nil {
		ext := strings.ToLower(filepath.Ext(parsed.Path))
		if ext != "" {
			return ext
		}
	}
	return ".bin"
}
