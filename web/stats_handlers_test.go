package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBacklogResponsesDescribeSourceProcessingScope(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{})
	if err != nil {
		t.Fatalf("NewHandlerOptions: %v", err)
	}

	for _, path := range []string{"/api/bootstrap", "/api/stats/backlog"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			backlogJSON := rec.Body.Bytes()
			if raw, ok := payload["backlog"]; ok {
				backlogJSON = raw
			}
			var backlog BacklogResponse
			if err := json.Unmarshal(backlogJSON, &backlog); err != nil {
				t.Fatalf("decode backlog: %v", err)
			}
			var flattened map[string]json.RawMessage
			if err := json.Unmarshal(backlogJSON, &flattened); err != nil {
				t.Fatalf("decode flattened backlog: %v", err)
			}
			for _, legacyField := range []string{"x_hydration_pending", "link_discovery_pending", "source_extraction_pending", "source_summary_pending", "source_extraction_pending_by_type", "source_summary_pending_by_type", "drained"} {
				if _, ok := flattened[legacyField]; !ok {
					t.Fatalf("legacy backlog field %q was not preserved: %s", legacyField, backlogJSON)
				}
			}
			if backlog.ScopeDescription != SourceBacklogScopeDescription {
				t.Fatalf("scope_description = %q, want %q", backlog.ScopeDescription, SourceBacklogScopeDescription)
			}
			if backlog.SourceExtractionPending < 0 || backlog.SourceSummaryPending < 0 {
				t.Fatalf("backlog counts must remain available: %+v", backlog)
			}
		})
	}
}
