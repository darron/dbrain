package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/store"
)

func TestChatShareCreateListAndPublicPageRedactsInternals(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	_, sourceKey := seedTestData(t, ctx, cfg, st)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	request := ChatShareCreateRequest{
		Turn: ChatTranscriptTurn{
			ID:        "chat:turn-1",
			Question:  "What should I remember about agent memory?",
			Status:    "ready",
			CreatedAt: "2026-05-15T12:00:00Z",
			Answer: strings.Join([]string{
				"Agent memory systems need durable retrieval and citations [" + sourceKey + "].",
				"### Markdown Heading",
				"- Render **bold** and `code` as HTML.",
				"Clean URL punctuation: https://example.com/with-backtick` and https://example.com/with-colon:",
				"Bracketed/angled URLs should become hostname links: [https://example.com/bracketed%60] and <https://www.example.org/angled%60>",
				"source_key: " + sourceKey,
				"Local path: /Users/darron/src/dbrain/data/brain.db",
				"Internal route: /api/get?lookup=" + url.QueryEscape(sourceKey),
				"Raw HTML should stay inert: <script>alert(1)</script>",
			}, "\n"),
			Citations: []brainresearch.Citation{
				{SourceKey: sourceKey, Title: "Agent Memory Article", URL: "https://example.com/agent-memory", NotePath: "sources/test-agent-memory.md", Kind: "source"},
			},
			ResearchPack: brainresearch.Pack{
				SchemaVersion: brainresearch.SchemaVersion,
				Evidence: []ask.Evidence{
					{
						SourceKey:  sourceKey,
						Kind:       "source",
						Title:      "Agent Memory Article",
						URL:        "https://example.com/agent-memory",
						NotePath:   "sources/test-agent-memory.md",
						Summary:    "Summary about durable retrieval.",
						SourceType: "web",
					},
				},
			},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal share request: %v", err)
	}
	create := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/chat/shares", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, createReq)
	if create.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d: %s", create.Code, create.Body.String())
	}
	var createResponse ChatShareResponse
	if err := json.Unmarshal(create.Body.Bytes(), &createResponse); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResponse.URL == "" || !strings.HasPrefix(createResponse.URL, "/share/") || createResponse.Slug == "" {
		t.Fatalf("unexpected create response: %+v", createResponse)
	}
	if len(createResponse.Categories) == 0 || createResponse.Summary == "" {
		t.Fatalf("expected summary/categories in response: %+v", createResponse)
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/chat/shares", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d: %s", list.Code, list.Body.String())
	}
	var listResponse ChatShareListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResponse.Shares) != 1 || listResponse.Shares[0].Slug != createResponse.Slug {
		t.Fatalf("expected created share in list, got %+v", listResponse)
	}

	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, createResponse.URL, nil))
	if public.Code != http.StatusOK {
		t.Fatalf("expected public page 200, got %d: %s", public.Code, public.Body.String())
	}
	page := public.Body.String()
	for _, want := range []string{"https://example.com/agent-memory", "Agent memory systems", "Original URLs", "Summary about durable retrieval.", "<h3 id=\"markdown-heading\">Markdown Heading</h3>", "<strong>bold</strong>", "<code>code</code>", "href=\"https://example.com/with-backtick\"", "href=\"https://example.com/with-colon\"", "href=\"https://example.com/bracketed\">example.com</a>", "href=\"https://www.example.org/angled\">example.org</a>"} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected public page to contain %q:\n%s", want, page)
		}
	}
	for _, forbidden := range []string{
		sourceKey,
		"source_key",
		"/Users/darron",
		"brain.db",
		"/api/get",
		"data-lookup",
		"note_path",
		"sources/test-agent-memory.md",
		"<script>alert",
		"%60",
		"&lt;https://",
		"[<a href",
		"]</a>",
	} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("public page leaked %q:\n%s", forbidden, page)
		}
	}
	if !strings.Contains(page, "raw HTML omitted") {
		t.Fatalf("expected stored HTML to be inert, got:\n%s", page)
	}

	recreate := httptest.NewRecorder()
	recreateReq := httptest.NewRequest(http.MethodPost, "/api/chat/shares", bytes.NewReader(body))
	recreateReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recreate, recreateReq)
	if recreate.Code != http.StatusOK {
		t.Fatalf("expected recreate 200, got %d: %s", recreate.Code, recreate.Body.String())
	}
	var recreateResponse ChatShareResponse
	if err := json.Unmarshal(recreate.Body.Bytes(), &recreateResponse); err != nil {
		t.Fatalf("decode recreate response: %v", err)
	}
	if recreateResponse.Slug != createResponse.Slug {
		t.Fatalf("expected identical owner/content to reuse slug, got %q then %q", createResponse.Slug, recreateResponse.Slug)
	}
}

func TestPublicShareBypassesAuthOnlyForSharePages(t *testing.T) {
	cfg, st := openTestStore(t)
	share, err := st.SavePublicChatShare(t.Context(), store.PublicChatShareInput{
		OwnerProvider:    "github",
		OwnerSubject:     "12345",
		OwnerUsername:    "darron",
		Title:            "Public share",
		Summary:          "A public share summary.",
		Categories:       []string{"general"},
		SanitizedContent: "A public share body.",
		OriginalURLs:     []string{"https://example.com/public"},
	})
	if err != nil {
		t.Fatalf("SavePublicChatShare: %v", err)
	}
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/share/"+share.Slug, nil))
	if public.Code != http.StatusOK {
		t.Fatalf("expected public share 200 without auth, got %d: %s", public.Code, public.Body.String())
	}
	publicBody := public.Body.String()
	for _, forbidden := range []string{"/api/bootstrap", "/api/chat/shares", "assets/index", "Search", "Research", "Chat"} {
		if strings.Contains(publicBody, forbidden) {
			t.Fatalf("public share booted or linked protected app surface %q:\n%s", forbidden, publicBody)
		}
	}

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/share/"+share.Slug, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("expected public share HEAD 200 with empty body, got %d body=%q", head.Code, head.Body.String())
	}

	publicMiss := httptest.NewRecorder()
	handler.ServeHTTP(publicMiss, httptest.NewRequest(http.MethodGet, "/share/"+share.Slug+"/extra", nil))
	if publicMiss.Code != http.StatusNotFound {
		t.Fatalf("expected non-slug share path 404, got %d: %s", publicMiss.Code, publicMiss.Body.String())
	}

	apiRoutes := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/bootstrap"},
		{method: http.MethodGet, path: "/api/chat/shares"},
		{method: http.MethodPost, path: "/api/chat/shares", body: `{"turn":{"status":"ready","answer":"hello"}}`},
	}
	for _, tt := range apiRoutes {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Accept", "application/json")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected protected API route 401, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	browserRoutes := []string{"/", "/assets/index.js", "/media/asset/1", "/admin"}
	for _, target := range browserRoutes {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Header.Set("Accept", "text/html")
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("expected protected browser route redirect, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
