package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const defaultSignedURLTTL = 5 * time.Minute

type signedURLResponse struct {
	AssetID   int64  `json:"asset_id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	ProxyURL  string `json:"proxy_url"`
	MediaType string `json:"media_type"`
}

func (s *server) loadArchivedMediaAsset(w http.ResponseWriter, r *http.Request) (model.MediaAsset, bool) {
	assetID, err := parseMediaAssetID(r)
	if err != nil {
		writeMessage(w, http.StatusBadRequest, err.Error())
		return model.MediaAsset{}, false
	}
	asset, err := s.store.GetMediaAsset(r.Context(), assetID)
	if err != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("media asset not found: %d", assetID))
		return model.MediaAsset{}, false
	}
	if strings.TrimSpace(asset.ArchiveStatus) != "archived" || strings.TrimSpace(asset.ArchiveBucket) == "" || strings.TrimSpace(asset.ArchiveKey) == "" {
		writeMessage(w, http.StatusConflict, fmt.Sprintf("media asset %d is not archived", assetID))
		return model.MediaAsset{}, false
	}
	return asset, true
}

func parseMediaAssetID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/media/asset/"))
	if raw == "" || raw == r.URL.Path {
		raw = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return 0, fmt.Errorf("media asset id is required")
	}
	assetID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || assetID <= 0 {
		return 0, fmt.Errorf("invalid media asset id: %q", raw)
	}
	return assetID, nil
}

func parseSignedURLTTL(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSignedURLTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return defaultSignedURLTTL
	}
	if ttl < time.Minute {
		return time.Minute
	}
	if ttl > time.Hour {
		return time.Hour
	}
	return ttl
}

func (s *server) mediaProxyURL(assetID int64) string {
	baseURL := strings.TrimSpace(s.proxyBase)
	if baseURL == "" {
		baseURL = mediaProxyBaseURL(s.cfg)
	}
	return strings.TrimRight(baseURL, "/") + "/media/asset/" + url.PathEscape(strconv.FormatInt(assetID, 10))
}

func mediaProxyBaseURL(cfg config.Config) string {
	baseURL := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_MEDIA_PROXY_BASE_URL", "DBRAIN_WEB_BASE_URL"))
	switch strings.ToLower(baseURL) {
	case "off", "none", "disabled":
		return ""
	}
	if baseURL == "" {
		return "http://" + defaultAddr
	}
	return baseURL
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
