package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darron/dbrain/internal/store"
)

const testExtensionOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
const testOriginGuardItemKey = "item:test-agent-memory"

func TestLocalHandlerOriginGuardRejectsCrossOriginSimpleTagRequest(t *testing.T) {
	cfg, st := openTestStore(t)
	seedTestData(t, t.Context(), cfg, st)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8742/api/tag", bytes.NewBufferString(`{"lookup":"`+testOriginGuardItemKey+`","tags":"attacker-controlled"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 body=%q", rec.Code, rec.Body.String())
	}
	item, err := st.GetItem(t.Context(), testOriginGuardItemKey)
	if err != nil {
		t.Fatalf("GetItem after hostile request: %v", err)
	}
	if item.UserTags != "agent-memory, retrieval" {
		t.Errorf("hostile request changed persisted tags to %q", item.UserTags)
	}
}

func TestLocalHandlerOriginGuardAllowsAuthorizedClients(t *testing.T) {
	t.Run("same-origin tag", func(t *testing.T) {
		cfg, st := openTestStore(t)
		seedTestData(t, t.Context(), cfg, st)
		handler, err := NewHandler(cfg, st)
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}

		req := newTagRequest(testOriginGuardItemKey, "same-origin", "http://127.0.0.1:8742")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%q", rec.Code, rec.Body.String())
		}
		assertItemTags(t, st, testOriginGuardItemKey, "same-origin")
	})

	t.Run("no-Origin CLI tag", func(t *testing.T) {
		cfg, st := openTestStore(t)
		seedTestData(t, t.Context(), cfg, st)
		handler, err := NewHandler(cfg, st)
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}

		req := newTagRequest(testOriginGuardItemKey, "cli", "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%q", rec.Code, rec.Body.String())
		}
		assertItemTags(t, st, testOriginGuardItemKey, "cli")
	})

	t.Run("extension link add", func(t *testing.T) {
		cfg, st := openTestStore(t)
		handler, err := NewHandler(cfg, st)
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8742/api/links", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", testExtensionOrigin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want application-level 400 body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("extension tag denied", func(t *testing.T) {
		cfg, st := openTestStore(t)
		seedTestData(t, t.Context(), cfg, st)
		handler, err := NewHandler(cfg, st)
		if err != nil {
			t.Fatalf("NewHandler: %v", err)
		}

		req := newTagRequest(testOriginGuardItemKey, "extension-controlled", testExtensionOrigin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 body=%q", rec.Code, rec.Body.String())
		}
		assertItemTags(t, st, testOriginGuardItemKey, "agent-memory, retrieval")
	})
}

func newTagRequest(sourceKey string, tags string, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8742/api/tag", bytes.NewBufferString(`{"lookup":"`+sourceKey+`","tags":"`+tags+`"}`))
	req.Header.Set("Content-Type", "text/plain")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func assertItemTags(t *testing.T, st *store.Store, sourceKey string, want string) {
	t.Helper()

	item, err := st.GetItem(t.Context(), sourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.UserTags != want {
		t.Fatalf("item tags = %q, want %q", item.UserTags, want)
	}
}
