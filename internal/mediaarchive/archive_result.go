package mediaarchive

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func archiveResultForAsset(asset model.MediaAsset, opts Options) model.MediaArchiveResult {
	archivedAt := time.Now().UTC()
	if asset.ArchiveStatus == model.MediaArchiveStatusArchived && !asset.ArchivedAt.IsZero() {
		archivedAt = asset.ArchivedAt
	}
	key := filepath.ToSlash(strings.TrimSpace(asset.LocalPath))
	url := buildArchiveURL(strings.TrimSpace(opts.PublicBaseURL), key)
	if url == "" {
		url = strings.TrimSpace(asset.ArchiveURL)
	}
	return model.MediaArchiveResult{
		Provider:   strings.TrimSpace(opts.Provider),
		Bucket:     strings.TrimSpace(opts.Bucket),
		Key:        key,
		URL:        url,
		Status:     model.MediaArchiveStatusArchived,
		Error:      "",
		ArchivedAt: archivedAt,
	}
}

func buildArchiveURL(baseURL, key string) string {
	baseURL = strings.TrimSpace(baseURL)
	key = strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(key)), "/")
	if baseURL == "" || key == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + key
}
