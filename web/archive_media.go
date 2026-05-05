package web

import (
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleMediaAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	asset, ok := s.loadArchivedMediaAsset(w, r)
	if !ok {
		return
	}
	if s.archive == nil {
		writeMessage(w, http.StatusServiceUnavailable, "archive media proxy is not configured")
		return
	}

	var (
		obj archiveObject
		err error
	)
	if r.Method == http.MethodHead {
		obj, err = s.archive.HeadObject(r.Context(), asset.ArchiveBucket, asset.ArchiveKey)
	} else {
		obj, err = s.archive.GetObject(r.Context(), asset.ArchiveBucket, asset.ArchiveKey, r.Header.Get("Range"))
	}
	if err != nil {
		s.writeArchiveProxyError(w, err)
		return
	}
	if obj.Body != nil {
		defer func() {
			_ = obj.Body.Close()
		}()
	}

	writeArchiveHeaders(w.Header(), asset, obj)
	status := http.StatusOK
	if strings.TrimSpace(r.Header.Get("Range")) != "" && strings.TrimSpace(obj.ContentRange) != "" {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead || obj.Body == nil {
		return
	}
	if _, err := io.Copy(w, obj.Body); err != nil {
		return
	}
}

func (s *server) handleMediaSignedURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	asset, ok := s.loadArchivedMediaAsset(w, r)
	if !ok {
		return
	}
	if s.archive == nil {
		writeMessage(w, http.StatusServiceUnavailable, "archive media proxy is not configured")
		return
	}

	ttl := parseSignedURLTTL(r.URL.Query().Get("ttl"))
	signedURL, expiresAt, err := s.archive.PresignGetObject(r.Context(), asset.ArchiveBucket, asset.ArchiveKey, ttl)
	if err != nil {
		s.writeArchiveProxyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, signedURLResponse{
		AssetID:   asset.ID,
		URL:       signedURL,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Bucket:    asset.ArchiveBucket,
		Key:       asset.ArchiveKey,
		ProxyURL:  s.mediaProxyURL(asset.ID),
		MediaType: asset.MediaType,
		Source:    asset.LocalPath,
	})
}
