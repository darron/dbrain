package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/linkadd"
)

func (s *server) handleTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req TagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Lookup == "" {
		writeMessage(w, http.StatusBadRequest, "lookup is required")
		return
	}
	item, err := s.store.GetItem(r.Context(), req.Lookup)
	if err == nil {
		if err := s.store.SaveItemUserTags(r.Context(), item.ID, req.Tags); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		item.UserTags = req.Tags
		writeJSON(w, http.StatusOK, item)
		return
	}

	source, err := s.store.GetSource(r.Context(), req.Lookup)
	if err != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("item or source not found: %s", req.Lookup))
		return
	}
	if err := s.store.SaveSourceUserTags(r.Context(), source.ID, req.Tags); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	source.UserTags = req.Tags
	writeJSON(w, http.StatusOK, source)
}

func (s *server) handleLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req LinkAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	urls := append([]string{}, req.URLs...)
	if strings.TrimSpace(req.URL) != "" {
		urls = append(urls, req.URL)
	}
	if len(urls) == 0 {
		writeMessage(w, http.StatusBadRequest, "url is required")
		return
	}

	stats, err := linkadd.Run(r.Context(), s.cfg, s.store, urls, linkadd.Options{
		Enrich:    req.Enrich,
		Summarize: true,
		CLI:       defaultWebCLI,
		Length:    "medium",
		Timeout:   2 * time.Minute,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if stats.Errors > 0 && stats.Queued == 0 {
		writeJSON(w, http.StatusBadRequest, stats)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
