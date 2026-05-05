package sourceenrich

import (
	"fmt"
	neturl "net/url"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func mediaURLSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	rawURL := firstNonEmpty(extract.FinalURL, extract.CanonicalURL)
	if strings.TrimSpace(rawURL) == "" {
		return "", false
	}
	ext := sourceURLPathExtension(rawURL)
	if ext == "" {
		return "", false
	}
	if !isUnsupportedTextSummaryMediaExtension(ext) {
		return "", false
	}
	return fmt.Sprintf("source URL points to %s content (%s); text summarization skipped", unsupportedTextSummaryMediaKind(ext), ext), true
}

func sourceURLPathExtension(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	path := parsed.EscapedPath()
	if path == "" {
		return ""
	}
	return strings.ToLower(filepath.Ext(path))
}

func isUnsupportedTextSummaryMediaExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".ico", ".svg":
		return true
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi", ".mpeg", ".mpg":
		return true
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus":
		return true
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".dmg", ".pkg":
		return true
	default:
		return false
	}
}

func unsupportedTextSummaryMediaKind(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".ico", ".svg":
		return "image/media"
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi", ".mpeg", ".mpg":
		return "video/media"
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus":
		return "audio/media"
	default:
		return "binary/media"
	}
}
