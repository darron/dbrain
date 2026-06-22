package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

const (
	defaultWhatsNewLimit = 50
	maxWhatsNewLimit     = 500
)

func (s *server) handleWhatsNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	filter, err := parseWhatsNewFilter(r)
	if err != nil {
		writeMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	page, err := s.store.ListReviewEvents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseWhatsNewFilter(r *http.Request) (store.ReviewEventFilter, error) {
	query := r.URL.Query()
	since := strings.TrimSpace(query.Get("since"))
	cursorToken := strings.TrimSpace(query.Get("cursor"))
	if (since == "") == (cursorToken == "") {
		return store.ReviewEventFilter{}, fmt.Errorf("exactly one of since or cursor is required")
	}

	var (
		cursor store.ReviewCursor
		err    error
	)
	if cursorToken != "" {
		cursor, err = store.DecodeReviewCursor(cursorToken)
	} else {
		cursor, err = store.ParseReviewSince(since, time.Now().UTC())
	}
	if err != nil {
		return store.ReviewEventFilter{}, err
	}

	types := query["types"]
	if _, err := store.NormalizeReviewEventTypes(types); err != nil {
		return store.ReviewEventFilter{}, err
	}
	view := query.Get("view")
	if _, err := store.NormalizeReviewEventView(view); err != nil {
		return store.ReviewEventFilter{}, err
	}

	return store.ReviewEventFilter{
		Cursor: cursor,
		Limit:  clampLimit(parseIntDefault(query.Get("limit"), defaultWhatsNewLimit), 1, maxWhatsNewLimit),
		Types:  types,
		View:   view,
	}, nil
}
