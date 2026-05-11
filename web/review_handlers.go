package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

func (s *server) handleWhatsNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	query := r.URL.Query()
	cursor, err := store.ParseReviewCursorInput(time.Now(), query.Get("since"), query.Get("cursor"))
	if err != nil {
		writeMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 100
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			writeMessage(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	feed, err := s.store.ListReviewEvents(r.Context(), store.ReviewEventFilter{
		Cursor: cursor,
		Limit:  limit,
		Types:  query["types"],
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}
