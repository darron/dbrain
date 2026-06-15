package okf

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func archivedMediaURL(ref model.ItemMediaRef, opts ExportOptions) string {
	if url := strings.TrimSpace(ref.ArchiveURL); url != "" {
		return url
	}
	if strings.TrimSpace(ref.ArchiveStatus) != model.MediaArchiveStatusArchived {
		return ""
	}
	if url := archivedMediaPublicURL(ref, opts.MediaPublicBaseURL); url != "" {
		return url
	}
	return archivedMediaProxyURL(ref, opts.MediaProxyBaseURL)
}

func archivedMediaPublicURL(ref model.ItemMediaRef, publicBaseURL string) string {
	base := strings.TrimSpace(publicBaseURL)
	key := strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(ref.ArchiveKey)), "/")
	if base == "" || key == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + key
}

func archivedMediaProxyURL(ref model.ItemMediaRef, proxyBaseURL string) string {
	base := strings.TrimSpace(proxyBaseURL)
	if base == "" || ref.MediaAssetID <= 0 {
		return ""
	}
	return strings.TrimRight(base, "/") + "/media/asset/" + strconv.FormatInt(ref.MediaAssetID, 10)
}
