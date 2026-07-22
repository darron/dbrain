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
				SourceKey: "src:used",
				Title:     "A global workspace in language models",
				URL:       "https://www.anthropic.com/research/global-workspace",
				Kind:      "source",
			},
			{SourceKey: "src:unrelated", URL: "https://www.cncf.io/blog/2026/04/30/ai-sandboxing-is-having-its-kubernetes-moment/"},
			{SourceKey: "src:also-unrelated", URL: "https://cnbc.com/2026/06/16/spacex-spcx-cursor-acquisition-ipo.html"},
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
	want := "toronto-police,barcelona,criminal-law"
	if strings.Join(got, ",") != want {
		t.Fatalf("categorizeSharedContent() = %q, want %q", strings.Join(got, ","), want)
	}
	for _, forbidden := range []string{"research", "software", "media", "infrastructure"} {
		if containsString(got, forbidden) {
			t.Fatalf("share categories should not include generic %q: %#v", forbidden, got)
		}
	}
}

func TestCategorizeSharedContentFallsBackToQueryTags(t *testing.T) {
	turn := ChatTranscriptTurn{
		Question: "summarize ai harness",
		Answer:   "No evidence rows carried tags.",
		ResearchPack: brainresearch.Pack{
			QueryPlan: brainresearch.QueryPlan{TagQueries: []string{"large-language-models", "ai-agents"}},
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
			want: "https://boereport.com/2026/05/01/alberta-oil-pipeline?utm_medium=twitter&utm_source=dlvr.it",
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

func TestPublicExternalURLRejectsUserinfoAndStripsSensitiveQuery(t *testing.T) {
	t.Run("rejects userinfo", func(t *testing.T) {
		got, ok := publicExternalURL("https://sec011-user:sec011-pass@example.com/article?section=ordinary")
		if ok || got != "" {
			t.Fatalf("publicExternalURL retained userinfo: got %q, %v", got, ok)
		}
	})

	t.Run("strips sensitive query keys and preserves ordinary parameters", func(t *testing.T) {
		raw := "https://example.com/article?x=1&token=TOKEN_SENTINEL&key=KEY_SENTINEL&signature=SIGNATURE_SENTINEL&access_token=ACCESS_TOKEN_SENTINEL&api_key=API_KEY_SENTINEL&apikey=APIKEY_SENTINEL&secret=SECRET_SENTINEL&sig=SIG_SENTINEL&X-Amz-Credential=AMZ_CREDENTIAL_SENTINEL&x-amz-signature=AMZ_SIGNATURE_SENTINEL&X-AMZ-Security-Token=AMZ_TOKEN_SENTINEL&section=ordinary"
		want := "https://example.com/article?section=ordinary&x=1"
		got, ok := publicExternalURL(raw)
		if !ok || got != want {
			t.Fatalf("publicExternalURL(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	})

	t.Run("strips structured sensitive query keys", func(t *testing.T) {
		raw := "https://example.com/article?section=ordinary&token[]=BRACKETED_TOKEN_SENTINEL&token[value]=BRACKETED_OBJECT_SENTINEL&auth[to%6ben]=NESTED_KEY_SENTINEL&credentials[access_token]=NESTED_ACCESS_TOKEN_SENTINEL"
		want := "https://example.com/article?section=ordinary"
		got, ok := publicExternalURL(raw)
		if !ok || got != want {
			t.Fatalf("publicExternalURL(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	})

	t.Run("drops ordinary parameters containing sensitive nested URLs", func(t *testing.T) {
		raw := "https://example.com/redirect?section=ordinary&next=https://nested-user:NESTED_URL_SENTINEL@example.org/private"
		want := "https://example.com/redirect?section=ordinary"
		got, ok := publicExternalURL(raw)
		if !ok || got != want {
			t.Fatalf("publicExternalURL(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	})

	t.Run("drops encoded nested keys and URLs", func(t *testing.T) {
		tests := []string{
			"https://example.com/redirect?section=ordinary&next=https%253A%252F%252Fnested-user%253ADOUBLE_ENCODED_URL_SENTINEL%2540example.org%252Fprivate",
			"https://example.com/redirect?section=ordinary&next=h%26%23116%3Btps%3A%2F%2Fnested-user%3AENTITY_NESTED_URL_SENTINEL%40example.org%2Fprivate",
			"https://example.com/redirect?section=ordinary&auth%255Btoken%255D=DOUBLE_ENCODED_KEY_SENTINEL",
			"https://example.com/redirect?section=ordinary&params=token%3DNESTED_QUERY_VALUE_SENTINEL",
			"https://example.com/redirect?section=ordinary&params=auth%255Btoken%255D%253DNESTED_STRUCT_VALUE_SENTINEL",
			"https://example.com/redirect?section=ordinary&next=%2Fcallback%3Ftoken%3DRELATIVE_URL_TOKEN_SENTINEL",
			"https://example.com/redirect?section=ordinary&next=%2Fcallback%23token%3DRELATIVE_FRAGMENT_TOKEN_SENTINEL",
			"https://example.com/redirect?section=ordinary&next=ht%09tps%3A%2F%2Fnested-user%3ATAB_NESTED_URL_SENTINEL%40example.org%2Fprivate",
			"https://example.com/redirect?section=ordinary&next=htt%0Aps%3A%2F%2Fnested-user%3ANEWLINE_NESTED_URL_SENTINEL%40example.org%2Fprivate",
			"https://example.com/redirect?section=ordinary&next=%5C%5Cnested-user%3ABACKSLASH_NESTED_URL_SENTINEL%40example.org%2Fprivate",
			"https://example.com/redirect?section=ordinary&next=http%3Anested-user%3ANESTED_ZERO_SLASH_URL_SENTINEL%40example.org%2Fprivate",
		}
		want := "https://example.com/redirect?section=ordinary"
		for _, raw := range tests {
			got, ok := publicExternalURL(raw)
			if !ok || got != want {
				t.Errorf("publicExternalURL(%q) = %q, %v; want %q, true", raw, got, ok, want)
			}
		}
	})
}

func TestPublicShareRedactsSensitiveURLs(t *testing.T) {
	const (
		userinfoURL           = "https://sec011-user:sec011-pass@example.com/userinfo?section=ordinary"
		sensitiveURL          = "https://example.com/article?section=ordinary&token=TOKEN_SENTINEL&key=KEY_SENTINEL&signature=SIGNATURE_SENTINEL&access_token=ACCESS_TOKEN_SENTINEL&api_key=API_KEY_SENTINEL&apikey=APIKEY_SENTINEL&secret=SECRET_SENTINEL&sig=SIG_SENTINEL&X-Amz-Credential=AMZ_CREDENTIAL_SENTINEL&x-amz-signature=AMZ_SIGNATURE_SENTINEL&X-AMZ-Security-Token=AMZ_TOKEN_SENTINEL&x=1"
		sanitizedURL          = "https://example.com/article?section=ordinary&x=1"
		normalURL             = "https://example.net/reference?section=ordinary"
		questionURL           = "HtTpS://question-user:question-pass@example.edu/question"
		evidenceTitleURL      = "HTTPs://title-user:title-pass@example.edu/title"
		evidenceSummaryURL    = "HTTPS://example.edu/summary?token=SUMMARY_TOKEN_SENTINEL"
		evidenceExcerptURL    = "hTTps://example.edu/excerpt?api_key=EXCERPT_API_KEY_SENTINEL"
		citationTitleURL      = "HTtPs://citation-user:citation-pass@example.edu/title"
		escapedSchemeURL      = `https\://escape-user:BACKSLASHPASS@example.com/path`
		decimalEntityURL      = "https&#58;//entity-user:ENTITYPASS@example.com/path"
		hexEntityURL          = "https&#x3a;//hex-user:HEXPASS@example.com/path"
		namedEntityURL        = "https&colon;//named-user:NAMEDPASS@example.com/path"
		splitNodeURL          = "http**s**://split-user:SPLITNODEPASS@example.com/path"
		imageReference        = "![pixel][secret-image]\n\n[secret-image]: h&#116;tps://img-user:IMAGEATTRPASS@example.com/pixel.png"
		bareUserinfoURL       = "https://sec011-bare-user:sec011-bare-pass@example.org/bare-userinfo"
		malformedSensitiveURL = "https://example.org/malformed?token=MALFORMED_QUERY_SENTINEL;bad=1&section=ordinary"
		rawHTML               = "<script>SEC011_RAW_HTML_SENTINEL</script>"
	)

	cfg, st := openTestStore(t)
	turn := ChatTranscriptTurn{
		ID:       "chat:sec011-regression",
		Question: "What do the cited sources say about " + questionURL + "?",
		Status:   "ready",
		Answer: strings.Join([]string{
			"Userinfo source [src:userinfo].",
			"Sensitive query source [src:sensitive].",
			"Normal source [src:normal].",
			"Excerpt source [src:excerpt].",
			"Citation title source [src:citation-title].",
			"Bare userinfo URL: " + bareUserinfoURL,
			"Malformed sensitive URL: " + malformedSensitiveURL,
			"Escaped scheme URL: " + escapedSchemeURL,
			"Decimal entity URL: " + decimalEntityURL,
			"Hex entity URL: " + hexEntityURL,
			"Named entity URL: " + namedEntityURL,
			"Split-node URL: " + splitNodeURL,
			imageReference,
			rawHTML,
		}, "\n"),
		Citations: []brainresearch.Citation{
			{SourceKey: "src:userinfo", Title: "Userinfo source", URL: userinfoURL, Kind: "source"},
			{SourceKey: "src:sensitive", Title: "Sensitive source", URL: sensitiveURL, Kind: "source"},
			{SourceKey: "src:normal", Title: "Normal source", URL: normalURL, Kind: "source"},
			{SourceKey: "src:citation-title", Title: "Citation title " + citationTitleURL, URL: "https://example.net/citation-title", Kind: "source"},
		},
		ResearchPack: brainresearch.Pack{Evidence: []ask.Evidence{
			{
				SourceKey: "src:normal",
				Title:     "Normal source title " + evidenceTitleURL,
				URL:       normalURL,
				Summary:   "Evidence summary " + evidenceSummaryURL,
			},
			{
				SourceKey: "src:excerpt",
				Title:     "Excerpt source",
				URL:       "https://example.net/excerpt-source",
				Excerpt:   "Evidence excerpt " + evidenceExcerptURL,
			},
		}},
	}

	input := buildPublicChatShareInput(chatShareOwner{
		Provider: "local",
		Subject:  "local",
		Username: "local",
	}, turn, categoryvocab.Vocab{})
	assertSEC011SecretsAbsent(t, input.OriginalURLs, input.Title, input.Summary, input.SanitizedContent, input.MetadataJSON)
	for _, want := range []string{sanitizedURL, normalURL} {
		if !containsString(input.OriginalURLs, want) {
			t.Fatalf("share input omitted authorized URL %q: %v", want, input.OriginalURLs)
		}
	}

	share, err := st.SavePublicChatShare(t.Context(), input)
	if err != nil {
		t.Fatalf("SavePublicChatShare: %v", err)
	}
	persisted, found, err := st.GetPublicChatShareBySlug(t.Context(), share.Slug)
	if err != nil || !found {
		t.Fatalf("GetPublicChatShareBySlug: found=%v err=%v", found, err)
	}
	assertSEC011SecretsAbsent(t, persisted.OriginalURLs, persisted.MetadataJSON)
	assertSEC011SecretsAbsent(t, persisted.OriginalURLs, persisted.Title, persisted.Summary, persisted.SanitizedContent, persisted.MetadataJSON)
	for _, want := range []string{sanitizedURL, normalURL} {
		if !containsString(persisted.OriginalURLs, want) {
			t.Fatalf("persisted share omitted authorized URL %q: %v", want, persisted.OriginalURLs)
		}
	}

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/share/"+share.Slug, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("anonymous share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	assertSEC011SecretsAbsent(t, nil, body)
	for _, want := range []string{
		`href="https://example.com/article?section=ordinary&amp;x=1"`,
		`href="https://example.net/reference?section=ordinary"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("anonymous share omitted authorized URL %q: %s", want, body)
		}
	}
	csp := recorder.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP omitted %q: %q", want, csp)
		}
	}
	if strings.Contains(body, rawHTML) || strings.Contains(body, "SEC011_RAW_HTML_SENTINEL") || strings.Contains(body, "<script>") {
		t.Fatalf("raw HTML was rendered: %s", body)
	}
}

func TestPublicShareRedactsLegacySensitiveURLs(t *testing.T) {
	const (
		userinfoURL            = "https://legacy-user:legacy-pass@example.com/userinfo?section=ordinary"
		sensitiveURL           = "https://example.com/legacy?section=ordinary&token=LEGACY_TOKEN_SENTINEL&X-Amz-Signature=LEGACY_AMZ_SENTINEL&x=1"
		bareUserinfoURL        = "https://legacy-bare-user:legacy-bare-pass@example.org/bare-userinfo"
		malformedSensitiveURL  = "https://example.org/legacy-malformed?token=LEGACY_MALFORMED_QUERY_SENTINEL;bad=1"
		legacySanitizedContent = "Legacy public share body.\nBare userinfo URL: " + bareUserinfoURL + "\nMalformed sensitive URL: " + malformedSensitiveURL
		legacyTitleURL         = "HTtPS://legacy-title-user:legacy-title-pass@example.net/title"
		legacySummaryURL       = "hTTps://example.net/summary?token=LEGACY_SUMMARY_TOKEN_SENTINEL"
		legacyEscapedURL       = `https\://legacy-escape-user:LEGACYBACKSLASSPASS@example.com/path`
		legacyEntityURL        = "https&#x3A;//legacy-entity-user:LEGACYENTITYPASS@example.com/path"
		legacyCategoryURL      = "HTTPS://category-user:LEGACYCATEGORYPASS@example.com/category"
		legacyImageReference   = "![legacy pixel][legacy-secret-image]\n\n[legacy-secret-image]: h&#x74;tps://legacy-img-user:LEGACYIMAGEATTRPASS@example.com/pixel.png"
	)

	cfg, st := openTestStore(t)
	metadataJSON, err := json.Marshal(publicChatShareMetadata{Sources: []publicShareOriginalSource{
		{URL: userinfoURL, Title: "Legacy userinfo " + legacyTitleURL},
		{URL: sensitiveURL, Title: "Legacy sensitive query", Summary: "Legacy summary " + legacySummaryURL},
	}})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	share, err := st.SavePublicChatShare(t.Context(), store.PublicChatShareInput{
		OwnerProvider:    "local",
		OwnerSubject:     "local",
		OwnerUsername:    "local",
		Title:            "Legacy public share " + legacyTitleURL,
		Summary:          "Legacy public share summary " + legacySummaryURL,
		Categories:       []string{"general", legacyCategoryURL},
		SanitizedContent: legacySanitizedContent + "\nEscaped URL: " + legacyEscapedURL + "\nEntity URL: " + legacyEntityURL + "\n" + legacyImageReference,
		OriginalURLs:     []string{userinfoURL, sensitiveURL},
		MetadataJSON:     string(metadataJSON),
	})
	if err != nil {
		t.Fatalf("SavePublicChatShare: %v", err)
	}

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/share/"+share.Slug, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("anonymous legacy share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	assertSEC011SecretsAbsent(t, nil, body)
	for _, want := range []string{`href="https://example.com/legacy?section=ordinary&amp;x=1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("anonymous legacy share omitted authorized URL %q: %s", want, body)
		}
	}
}

func TestShareTitleRedactsSensitiveAnswerFallback(t *testing.T) {
	const sensitiveLeadingURL = "HtTpS://fallback-user:fallback-pass@example.com/answer"
	content := sanitizeSharedChatContent(sensitiveLeadingURL+" explains the result.", nil)
	title := shareTitle("", summarizeSharedContent(content))
	if strings.Contains(title, "fallback-user") || strings.Contains(title, "fallback-pass") {
		t.Fatalf("answer fallback title retained URL credentials: %q", title)
	}
	if !strings.Contains(title, "[redacted URL]") {
		t.Fatalf("answer fallback title omitted redaction marker: %q", title)
	}
}

func TestSanitizeSharedChatContentRedactsUnsafeReferenceImageAttributes(t *testing.T) {
	tests := []struct {
		name      string
		markdown  string
		forbidden string
	}{
		{
			name:      "userinfo",
			markdown:  "![pixel][secret]\n\n[secret]: h&#116;tps://image-user:IMAGEPASS@example.com/pixel.png",
			forbidden: "IMAGEPASS",
		},
		{
			name:      "sensitive query",
			markdown:  "![pixel][secret]\n\n[secret]: h&#116;tps://example.com/pixel.png?token=IMAGE_QUERY_TOKEN",
			forbidden: "IMAGE_QUERY_TOKEN",
		},
		{
			name:      "link title userinfo",
			markdown:  "[safe][secret]\n\n[secret]: h&#116;tps://example.com/path \"h&#116;tps://title-user:TITLEATTRPASS@example.net/hidden\"",
			forbidden: "TITLEATTRPASS",
		},
		{
			name:      "image title sensitive query",
			markdown:  "![pixel][secret]\n\n[secret]: h&#116;tps://example.com/pixel.png \"h&#x74;tps://example.net/hidden?token=TITLEQUERYATTRPASS\"",
			forbidden: "TITLEQUERYATTRPASS",
		},
		{
			name:      "adjacent attribute URLs",
			markdown:  "[safe][secret]\n\n[secret]: h&#116;tps://example.com/path \"h&#116;tps://safe.example/a;h&#116;tps://adjacent-user:ADJACENTPASS@example.net/hidden\"",
			forbidden: "ADJACENTPASS",
		},
		{
			name:      "adjacent visible URLs",
			markdown:  "h&#116;tps://safe.example/a;h&#116;tps://visible-user:VISIBLEADJACENTPASS@example.net/hidden",
			forbidden: "VISIBLEADJACENTPASS",
		},
		{
			name:      "protocol-relative title userinfo",
			markdown:  "[safe][secret]\n\n[secret]: h&#116;tps://example.com/path \"See &#47;&#47;proto-user:PROTOCOLRELATIVEPASS@example.net/hidden\"",
			forbidden: "PROTOCOLRELATIVEPASS",
		},
		{
			name:      "zero-slash HTTP userinfo",
			markdown:  "[safe][secret]\n\n[secret]: h&#116;tp:zero-user:ZEROSLASHPASS@example.net/hidden",
			forbidden: "ZEROSLASHPASS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeSharedChatContent(tt.markdown, nil)
			if got != "[redacted URL]" || strings.Contains(got, tt.forbidden) {
				t.Fatalf("sanitizeSharedChatContent retained unsafe rendered image attribute: %q", got)
			}
		})
	}
}

func TestSanitizeRenderedPublicShareHTMLSanitizesURLsInTextAndAttributes(t *testing.T) {
	rendered := `<a href="https://example.com/path" title="https://title-user:TITLEATTRPASS@example.net/hidden">safe</a>` +
		`<img srcset="https://example.com/a,https://srcset-user:SRCSETPASS@example.net/b" alt="pixel">` +
		`<img srcset="https://example.com/safe.png,//srcset-proto-user:PROTOCOLSRCSETPASS@example.net/hidden.png" alt="protocol pixel">` +
		`<span title="https://safe.example/a;https://adjacent-user:ADJACENTPASS@example.net/hidden">text</span>` +
		`<p>https://safe.example/a;https://rendered-visible-user:RENDEREDVISIBLEPASS@example.net/hidden</p>` +
		`<span title="See //proto-user:PROTOCOLRELATIVEPASS@example.net/hidden">protocol</span>`
	got, err := sanitizeRenderedPublicShareHTML(rendered)
	if err != nil {
		t.Fatalf("sanitize rendered HTML: %v", err)
	}
	for _, forbidden := range []string{"title-user", "TITLEATTRPASS", "srcset-user", "SRCSETPASS", "srcset-proto-user", "PROTOCOLSRCSETPASS", "adjacent-user", "ADJACENTPASS", "rendered-visible-user", "RENDEREDVISIBLEPASS", "proto-user", "PROTOCOLRELATIVEPASS"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("rendered HTML retained unsafe attribute sentinel %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, `href="https://example.com/path"`) {
		t.Fatalf("rendered HTML removed authorized href: %s", got)
	}
}

func TestPublicShareMarkdownURLLinkPreservesNonURLText(t *testing.T) {
	const text = "ordinary non-URL text"
	if got := publicShareMarkdownURLLink(text); got != text {
		t.Fatalf("publicShareMarkdownURLLink(%q) = %q", text, got)
	}
}

func TestPublicShareMarkdownURLLinkRedactsRejectedHTTPURLs(t *testing.T) {
	for _, rawURL := range []string{
		"https://bare-user:bare-pass@example.com/article",
		"https://example.com/article?token=REJECTED_QUERY_SENTINEL;bad=1",
	} {
		if got := publicShareMarkdownURLLink(rawURL); got != "[redacted URL]" {
			t.Fatalf("publicShareMarkdownURLLink(%q) = %q", rawURL, got)
		}
	}
}

func assertSEC011SecretsAbsent(t *testing.T, urls []string, values ...string) {
	t.Helper()
	combined := strings.Join(append(append([]string{}, urls...), values...), "\n")
	for _, forbidden := range []string{
		"sec011-user", "sec011-pass",
		"TOKEN_SENTINEL", "KEY_SENTINEL", "SIGNATURE_SENTINEL",
		"ACCESS_TOKEN_SENTINEL", "API_KEY_SENTINEL", "APIKEY_SENTINEL",
		"SECRET_SENTINEL", "SIG_SENTINEL", "AMZ_CREDENTIAL_SENTINEL",
		"AMZ_SIGNATURE_SENTINEL", "AMZ_TOKEN_SENTINEL",
		"legacy-user", "legacy-pass", "LEGACY_TOKEN_SENTINEL", "LEGACY_AMZ_SENTINEL",
		"sec011-bare-user", "sec011-bare-pass", "MALFORMED_QUERY_SENTINEL",
		"legacy-bare-user", "legacy-bare-pass", "LEGACY_MALFORMED_QUERY_SENTINEL",
		"question-user", "question-pass", "SUMMARY_TOKEN_SENTINEL",
		"title-user", "title-pass", "EXCERPT_API_KEY_SENTINEL",
		"citation-user", "citation-pass", "legacy-title-user", "legacy-title-pass",
		"LEGACY_SUMMARY_TOKEN_SENTINEL", "fallback-user", "fallback-pass",
		"escape-user", "BACKSLASHPASS", "entity-user", "ENTITYPASS",
		"hex-user", "HEXPASS", "named-user", "NAMEDPASS",
		"legacy-escape-user", "LEGACYBACKSLASSPASS", "legacy-entity-user", "LEGACYENTITYPASS",
		"split-user", "SPLITNODEPASS", "category-user", "LEGACYCATEGORYPASS",
		"img-user", "IMAGEATTRPASS", "legacy-img-user", "LEGACYIMAGEATTRPASS",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("public share retained sensitive sentinel %q: %s", forbidden, combined)
		}
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
