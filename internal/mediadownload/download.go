package mediadownload

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
)

type progressOptions struct {
	Logger   *slog.Logger
	Interval time.Duration
	Bytes    int64
}

func downloadRef(ctx context.Context, client *http.Client, cfg config.Config, ref model.ItemMediaRef, namespace string, progress progressOptions) (model.MediaDownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.RemoteURL, nil)
	if err != nil {
		return model.MediaDownloadResult{}, fmt.Errorf("create media request %q: %w", ref.RemoteURL, err)
	}
	req.Header.Set("user-agent", "dbrain/1.0")

	resp, err := client.Do(req)
	if err != nil {
		status := model.MediaDownloadStatusError
		if safehttp.IsPolicyError(err) {
			status = model.MediaDownloadStatusBlocked
		}
		return model.MediaDownloadResult{
			Status: status,
			Error:  err.Error(),
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusGone,
			Error:  fmt.Sprintf("media returned status=%d", resp.StatusCode),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  fmt.Sprintf("media returned status=%d", resp.StatusCode),
		}, nil
	}

	contentLength := resp.ContentLength
	contentType := resp.Header.Get("content-type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	if strings.HasPrefix(mediaType, "text/html") {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  "media request returned HTML instead of media bytes",
		}, nil
	}
	bufferedBody := bufio.NewReader(resp.Body)
	playlistHeader, _ := bufferedBody.Peek(512)
	if isHLSPlaylist(mediaType, ref.RemoteURL, playlistHeader) {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  "media request returned an HLS playlist instead of media bytes",
		}, nil
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		detected := http.DetectContentType(playlistHeader)
		if detected != "application/octet-stream" {
			mediaType = detected
		} else if ref.MediaType == "video" {
			// Bluesky getBlob URLs have no extension and some PDSes omit the
			// content type. Bluesky video blobs are MP4, so retain a truthful
			// extension for the existing ffprobe/transcription path.
			mediaType = "video/mp4"
		}
	}
	resp.Body = io.NopCloser(bufferedBody)

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
	writer := io.MultiWriter(tmpFile, hasher)
	tracker := newDownloadProgressWriter(writer, progress, ref, contentLength)
	if tracker != nil {
		writer = tracker
	}
	written, copyErr := io.Copy(writer, resp.Body)
	if tracker != nil {
		tracker.finish()
	}
	if copyErr != nil {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  copyErr.Error(),
		}, nil
	}
	if err := tmpFile.Close(); err != nil {
		return model.MediaDownloadResult{
			Status: model.MediaDownloadStatusError,
			Error:  err.Error(),
		}, nil
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	ext := mediaExtension(mediaType, ref.RemoteURL)
	relPath := filepath.ToSlash(filepath.Join("media", namespace, normalizedMediaType(ref.MediaType), sum[:2], sum+ext))
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
			Status:       model.MediaDownloadStatusDownloaded,
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
		Status:       model.MediaDownloadStatusDownloaded,
		DownloadedAt: time.Now().UTC(),
	}, nil
}

func normalizedMediaNamespace(value string) string {
	if strings.TrimSpace(strings.ToLower(value)) == "bsky" {
		return "bsky"
	}
	return "x"
}

func isHLSPlaylist(mediaType, rawURL string, prefix []byte) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "application/vnd.apple.mpegurl" || mediaType == "application/x-mpegurl" || mediaType == "audio/mpegurl" {
		return true
	}
	if parsed, err := neturl.Parse(rawURL); err == nil && strings.EqualFold(filepath.Ext(parsed.Path), ".m3u8") {
		return true
	}
	return bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("#EXTM3U"))
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
