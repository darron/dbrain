package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

func (s *server) handleBacklog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	backlog, err := s.backlog(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, backlog)
}

func (s *server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	window := defaultActivityWindow
	if raw := strings.TrimSpace(r.URL.Query().Get("window")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			writeMessage(w, http.StatusBadRequest, "window must be a valid duration")
			return
		}
		window = parsed
	}

	activity, err := s.activity(r.Context(), window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

func (s *server) handleSourceActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	filter := parseSourceActivityFilter(r)
	feed, err := s.sourceActivity(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *server) backlog(ctx context.Context) (store.BacklogStats, error) {
	return s.store.Backlog(ctx, "", "", "")
}

func (s *server) activity(ctx context.Context, window time.Duration) (store.ActivityStats, error) {
	return s.store.Activity(ctx, time.Now().UTC(), window)
}

func (s *server) sourceActivity(ctx context.Context, filter store.SourceActivityFilter) (store.SourceActivityFeed, error) {
	return s.store.SourceActivityFeedFiltered(ctx, filter)
}

func parseSourceActivityFilter(r *http.Request) store.SourceActivityFilter {
	window := defaultActivityWindow
	if raw := strings.TrimSpace(r.URL.Query().Get("window")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			window = parsed
		}
	}
	return store.SourceActivityFilter{
		Limit:         clampLimit(parseIntDefault(r.URL.Query().Get("limit"), defaultEventLimit), 1, maxEventLimit),
		FailureOffset: maxInt(parseIntDefault(r.URL.Query().Get("failure_offset"), 0), 0),
		FailureSort:   strings.TrimSpace(r.URL.Query().Get("failure_sort")),
		SourceType:    strings.TrimSpace(r.URL.Query().Get("source_type")),
		Domain:        strings.TrimSpace(r.URL.Query().Get("domain")),
		Status:        strings.TrimSpace(r.URL.Query().Get("status")),
		FailureKind:   strings.TrimSpace(r.URL.Query().Get("failure_kind")),
		Message:       strings.TrimSpace(r.URL.Query().Get("message")),
		Window:        window,
	}
}
