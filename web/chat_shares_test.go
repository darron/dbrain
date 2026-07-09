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
	"github.com/darron/dbrain/internal/categoryvocab"
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
				"Sources sometimes arrive as code-wrapped autolinks: `<https://example.net/code-source%60>`.",
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
						UserTags:   "agent-memory, durable-retrieval, research",
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
	if createResponse.Summary == "" {
		t.Fatalf("expected summary/categories in response: %+v", createResponse)
	}
	if got, want := strings.Join(createResponse.Categories, ","), "agent-memory,durable-retrieval"; got != want {
		t.Fatalf("expected evidence-derived share categories %q, got %q", want, got)
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
	for _, want := range []string{"https://example.com/agent-memory", ">https://example.com/agent-memory</a>: Summary about durable retrieval.", "Agent memory systems", "Original URLs", "Summary about durable retrieval.", "<span class=\"chip\">agent-memory</span>", "<span class=\"chip\">durable-retrieval</span>", "<h3 id=\"markdown-heading\">Markdown Heading</h3>", "<strong>bold</strong>", "<code>code</code>", "href=\"https://example.com/with-backtick\"", "href=\"https://example.com/with-colon\"", "href=\"https://example.com/bracketed\">example.com</a>", "href=\"https://www.example.org/angled\">example.org</a>", "href=\"https://example.net/code-source\">example.net</a>"} {
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
		"class=\"summary\"",
		"&lt;https://",
		"[<a href",
		"]</a>",
		"<span class=\"chip\">research</span>",
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

func TestBuildPublicChatShareInputIncludesOnlyCitedOriginalURLs(t *testing.T) {
	turn := ChatTranscriptTurn{
		ID:       "chat:turn-cited",
		Question: "Summarize j-space",
		Status:   "ready",
		Answer: strings.Join([]string{
			"J-space is described in the Anthropic research note [src:used].",
			"Additional context is available at https://example.net/manual.",
		}, "\n"),
		Citations: []brainresearch.Citation{
			{
				SourceKey: "x:cited",
				Title:     "Anthropic status",
				URL:       "https://x.com/AnthropicAI/status/2074185348142280912",
				Kind:      "item",
			},
		},
		ResearchPack: brainresearch.Pack{
			Evidence: []ask.Evidence{
				{
					SourceKey: "src:used",
					Kind:      "source",
					Title:     "A global workspace in language models",
					URL:       "https://www.anthropic.com/research/global-workspace",
					Summary:   "Used summary.",
				},
				{
					SourceKey: "src:unrelated",
					Kind:      "source",
					Title:     "AI sandboxing is having its Kubernetes moment",
					URL:       "https://www.cncf.io/blog/2026/04/30/ai-sandboxing-is-having-its-kubernetes-moment/",
					Summary:   "Unrelated retrieval candidate.",
				},
			},
			ExactTagEvidence: []ask.Evidence{
				{
					SourceKey: "src:also-unrelated",
					Kind:      "source",
					Title:     "SpaceX acquires Cursor",
					URL:       "https://cnbc.com/2026/06/16/spacex-spcx-cursor-acquisition-ipo.html",
					Summary:   "Another unrelated retrieval candidate.",
				},
			},
		},
	}

	input := buildPublicChatShareInput(chatShareOwner{
		Provider: "local",
		Subject:  "local",
		Username: "local",
	}, turn, categoryvocab.Vocab{})

	wantURLs := []string{
		"https://example.net/manual",
		"https://www.anthropic.com/research/global-workspace",
		"https://x.com/AnthropicAI/status/2074185348142280912",
	}
	if got := strings.Join(input.OriginalURLs, "\n"); got != strings.Join(wantURLs, "\n") {
		t.Fatalf("OriginalURLs =\n%s\nwant\n%s", got, strings.Join(wantURLs, "\n"))
	}
	for _, forbidden := range []string{
		"https://www.cncf.io/blog/2026/04/30/ai-sandboxing-is-having-its-kubernetes-moment/",
		"https://cnbc.com/2026/06/16/spacex-spcx-cursor-acquisition-ipo.html",
		"Unrelated retrieval candidate",
	} {
		if strings.Contains(input.MetadataJSON, forbidden) || containsString(input.OriginalURLs, forbidden) {
			t.Fatalf("share input included unrelated source %q: urls=%v metadata=%s", forbidden, input.OriginalURLs, input.MetadataJSON)
		}
	}
}

func TestCategorizeSharedContentWeightsAnswerCitedEvidenceTags(t *testing.T) {
	turn := ChatTranscriptTurn{
		Question: "barcelona toronto police",
		Answer:   "The key incident involved Toronto police in Barcelona [src:cited]. This answer mentions github, youtube, evidence, source, citation, and software.",
		Citations: []brainresearch.Citation{
			{SourceKey: "src:cited", URL: "https://example.com/cited"},
			{SourceKey: "src:included", URL: "https://example.com/included"},
		},
		ResearchPack: brainresearch.Pack{
			Evidence: []ask.Evidence{
				{
					SourceKey:  "src:included",
					SourceType: "github",
					UserTags:   "software, infrastructure, included-only",
				},
				{
					SourceKey:  "src:cited",
					SourceType: "web",
					UserTags:   "toronto-police, barcelona, criminal-law, research",
				},
			},
		},
	}

	got := categorizeSharedContent("old keyword fallback should not matter", turn, categoryvocab.Vocab{})
	want := "toronto-police,barcelona,criminal-law,included-only"
	if strings.Join(got, ",") != want {
		t.Fatalf("categorizeSharedContent() = %q, want %q", strings.Join(got, ","), want)
	}
	for _, forbidden := range []string{"research", "software", "media", "infrastructure"} {
		if containsString(got, forbidden) {
			t.Fatalf("share categories should not include generic %q: %#v", forbidden, got)
		}
	}
}

func TestCategorizeSharedContentFallsBackToCoverageTopUserTags(t *testing.T) {
	turn := ChatTranscriptTurn{
		Question: "summarize ai harness",
		Answer:   "No evidence rows carried tags.",
		ResearchPack: brainresearch.Pack{
			Coverage: brainresearch.Coverage{
				TopUserTags: []brainresearch.Bucket{
					{Key: "research", Count: 50},
					{Key: "large-language-models", Count: 4},
					{Key: "ai-agents", Count: 3},
				},
			},
		},
	}

	got := categorizeSharedContent("", turn, categoryvocab.Vocab{})
	want := "large-language-models,ai-agents"
	if strings.Join(got, ",") != want {
		t.Fatalf("categorizeSharedContent() = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestCategorizeSharedContentAppliesCategoryVocabulary(t *testing.T) {
	vocab, err := categoryvocab.Parse([]byte(strings.Join([]string{
		"aliases:",
		"  llm: large-language-models",
		"  ai-agent: ai-agents",
		"drop:",
		"  - github-repository",
	}, "\n")))
	if err != nil {
		t.Fatalf("parse vocab: %v", err)
	}
	turn := ChatTranscriptTurn{
		Answer: "Evidence citation [src:vocab].",
		ResearchPack: brainresearch.Pack{
			Evidence: []ask.Evidence{{
				SourceKey: "src:vocab",
				UserTags:  "LLM, AI Agent, github-repository",
			}},
		},
	}

	got := categorizeSharedContent("", turn, vocab)
	want := "large-language-models,ai-agents"
	if strings.Join(got, ",") != want {
		t.Fatalf("categorizeSharedContent() = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestChatShareRejectsVerificationFailedTurn(t *testing.T) {
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := bytes.NewBufferString(`{"turn":{"status":"verification_failed","question":"What changed?","answer":"Unverified answer."}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/shares", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "completed chat answers") {
		t.Fatalf("expected completed-answer diagnostic, got %s", rec.Body.String())
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func TestPublicExternalURLCleansEncodedBackticksAndPunctuation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "encoded backtick in path",
			raw:  "https://calgaryherald.com/opinion/story%60",
			want: "https://calgaryherald.com/opinion/story",
		},
		{
			name: "encoded backtick in query",
			raw:  "https://boereport.com/2026/05/01/alberta-oil-pipeline?utm_source=dlvr.it&utm_medium=twitter%60",
			want: "https://boereport.com/2026/05/01/alberta-oil-pipeline?utm_source=dlvr.it&utm_medium=twitter",
		},
		{
			name: "trailing colon",
			raw:  "https://example.com/source:",
			want: "https://example.com/source",
		},
		{
			name: "encoded adjacent markdown URL",
			raw:  "https://x.com/i/article/2048484969333526528%5D%5Bhttps://www.youtube.com/watch?v=nWzXyjXCoCE",
			want: "https://x.com/i/article/2048484969333526528",
		},
		{
			name: "raw adjacent markdown URL",
			raw:  "https://x.com/i/article/2048484969333526528][https://www.youtube.com/watch?v=nWzXyjXCoCE",
			want: "https://x.com/i/article/2048484969333526528",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := publicExternalURL(tt.raw)
			if !ok || got != tt.want {
				t.Fatalf("publicExternalURL(%q) = %q, %v; want %q, true", tt.raw, got, ok, tt.want)
			}
		})
	}
}

func TestLinkPublicShareURLsProducesRealLinks(t *testing.T) {
	markdown := strings.Join([]string{
		"Source: `<https://www.thestar.com/opinion/article.html%60>`: summary.",
		"Bare URL: https://calgaryherald.com/news/story%60.",
		"Bracketed URL: [https://nationalpost.com/news/story%60]",
	}, "\n")
	linked := linkPublicShareURLs(markdown)
	for _, want := range []string{
		"[thestar.com](https://www.thestar.com/opinion/article.html): summary.",
		"[calgaryherald.com](https://calgaryherald.com/news/story).",
		"[nationalpost.com](https://nationalpost.com/news/story)",
	} {
		if !strings.Contains(linked, want) {
			t.Fatalf("expected linked markdown to contain %q:\n%s", want, linked)
		}
	}
	for _, forbidden := range []string{"%60", "`[", "<https://"} {
		if strings.Contains(linked, forbidden) {
			t.Fatalf("linked markdown still contains %q:\n%s", forbidden, linked)
		}
	}

	html := string(renderPublicShareMarkdown(markdown))
	for _, want := range []string{
		"href=\"https://www.thestar.com/opinion/article.html\">thestar.com</a>: summary.",
		"href=\"https://calgaryherald.com/news/story\">calgaryherald.com</a>.",
		"href=\"https://nationalpost.com/news/story\">nationalpost.com</a>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<code>") {
		t.Fatalf("expected source URLs to render as links, not code spans:\n%s", html)
	}
}

func TestPublicShareOriginalSourcesUsesFullURLText(t *testing.T) {
	metadata := publicChatShareMetadata{Sources: []publicShareOriginalSource{{
		URL:     "https://www.thestar.com/opinion/article.html%60",
		Title:   "Toronto `Star` **opinion**",
		Summary: "### What It Is\nSummary beside the **full** URL with `code`.",
	}}}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	sources := publicShareOriginalSources([]string{"https://www.thestar.com/opinion/article.html%60"}, string(metadataJSON))
	if len(sources) != 1 {
		t.Fatalf("expected one original source, got %+v", sources)
	}
	if sources[0].URL != "https://www.thestar.com/opinion/article.html" || sources[0].Host != "thestar.com" || sources[0].Summary != "Summary beside the full URL with code." || sources[0].Title != "Toronto Star opinion" {
		t.Fatalf("unexpected source normalization: %+v", sources[0])
	}
	for _, forbidden := range []string{"###", "**", "`", "%60", "What It Is"} {
		if strings.Contains(sources[0].Title, forbidden) || strings.Contains(sources[0].Summary, forbidden) || strings.Contains(sources[0].URL, forbidden) {
			t.Fatalf("source still contains markdown/url artifact %q: %+v", forbidden, sources[0])
		}
	}
}

func TestPublicShareOriginalSourcesCleansStoredMarkdownJoinedURL(t *testing.T) {
	metadata := publicChatShareMetadata{Sources: []publicShareOriginalSource{{
		URL:     "https://x.com/i/article/2048484969333526528",
		Summary: "### What It Is\nThis source covers **harnesses** and `agents`.",
	}}}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	sources := publicShareOriginalSources([]string{
		"https://x.com/i/article/2048484969333526528%5D%5Bhttps://www.youtube.com/watch?v=nWzXyjXCoCE",
		"https://x.com/i/article/2048484969333526528",
	}, string(metadataJSON))
	if len(sources) != 1 {
		t.Fatalf("expected duplicate joined URL to collapse to one source, got %+v", sources)
	}
	if sources[0].URL != "https://x.com/i/article/2048484969333526528" {
		t.Fatalf("unexpected cleaned URL: %+v", sources[0])
	}
	if sources[0].Summary != "This source covers harnesses and agents." {
		t.Fatalf("expected plain-text summary, got %+v", sources[0])
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
