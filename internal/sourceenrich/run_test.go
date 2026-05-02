package sourceenrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/vault"
)

func TestSkipSummaryReasonSkipsTranscriptUnavailableYouTubeMetadataOnly(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "youtube"}
	extract := model.ExtractResult{
		Content: "Why I will NEVER surrender my guns.\nChannel business contact - ladner_chevy@hotmail.com",
		RawJSON: `{"extracted":{"transcriptSource":"unavailable","transcriptionProvider":null,"transcriptCharacters":null}}`,
	}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected summary to be skipped")
	}
	if reason == "" {
		t.Fatal("expected skip reason")
	}
}

func TestSkipSummaryReasonAllowsTranscriptBackedYouTubeExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "youtube"}
	extract := model.ExtractResult{
		Content: "Transcript:\nreal transcript content",
		RawJSON: `{"extracted":{"transcriptSource":"captionTracks","transcriptionProvider":null,"transcriptCharacters":2048}}`,
	}

	if reason, ok := skipSummaryReason(source, extract); ok {
		t.Fatalf("expected transcript-backed extract to summarize, got reason %q", reason)
	}
}

func TestSkipSummaryReasonSkipsPlaceholderRedirectExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "web"}
	extract := model.ExtractResult{
		Content: "Redirecting to latest/...",
	}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected placeholder extract to be skipped")
	}
	if !strings.Contains(reason, "placeholder boilerplate") {
		t.Fatalf("unexpected skip reason: %q", reason)
	}
}

func TestSkipSummaryReasonSkipsRepairLengthPlaceholderExtract(t *testing.T) {
	t.Parallel()

	content := repairLengthPlaceholderExtract()
	normalized := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if len(normalized) <= 160 || len(normalized) > 300 {
		t.Fatalf("test content should exercise the repair selector range, got %d chars", len(normalized))
	}

	source := model.SourceDocument{SourceType: "web"}
	extract := model.ExtractResult{Content: content}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected repair-length placeholder extract to be skipped")
	}
	if !strings.Contains(reason, "placeholder boilerplate") {
		t.Fatalf("unexpected skip reason: %q", reason)
	}
}

func TestSkipSummaryReasonSkipsShortWaybackExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "web"}
	extract := model.ExtractResult{
		Tool:    waybackToolName,
		Content: "The method to Mark Carney's madness\n\nBy Max Fawcett\n\nOpinion\n\nPolitics\n\nShare this article",
	}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected short wayback extract to be skipped")
	}
	if !strings.Contains(reason, "wayback extract is too short") {
		t.Fatalf("unexpected skip reason: %q", reason)
	}
}

func TestSkipSummaryReasonSkipsWaybackFrameShell(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "web"}
	extract := model.ExtractResult{
		Tool: waybackToolName,
		Content: `Your browser does not support frames. We recommend upgrading your browser.

Click here to enter the site.`,
	}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected wayback frame shell to be skipped")
	}
	if !strings.Contains(reason, "placeholder boilerplate") {
		t.Fatalf("unexpected skip reason: %q", reason)
	}
}

func TestSkipSummaryReasonAllowsShortSubstantiveExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "web"}
	extract := model.ExtractResult{
		Content: "An HTTP toolkit for security research.",
	}

	if reason, ok := skipSummaryReason(source, extract); ok {
		t.Fatalf("expected short substantive extract to summarize, got reason %q", reason)
	}
}

func TestSkipSummaryReasonAllowsSubstantiveSignupTeaser(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "web"}
	extract := model.ExtractResult{
		Content: "A Social Network for AI Agents Where AI agents share, discuss, and upvote. Humans welcome to observe. Send Your AI Agent to Moltbook. They sign up and send you a claim link. AI Agents Live Activity. Build for Agents. Let AI agents authenticate with your app using their Moltbook identity.",
	}

	if reason, ok := skipSummaryReason(source, extract); ok {
		t.Fatalf("expected substantive signup teaser to summarize, got reason %q", reason)
	}
}

func repairLengthPlaceholderExtract() string {
	return "Redirecting to latest documentation. If you are not redirected automatically, follow this link. " +
		strings.Repeat("placeholder text ", 10)
}

func TestBlockedSummaryReasonFlagsContextWindowErrors(t *testing.T) {
	t.Parallel()

	reason, ok := blockedSummaryReason(errors.New(`run openrouter summary: http 400: {"error":{"message":"This endpoint's maximum context length is 262144 tokens."}}`))
	if !ok {
		t.Fatal("expected context window error to be blocked")
	}
	if !strings.Contains(reason, "maximum context length") {
		t.Fatalf("unexpected blocked reason: %q", reason)
	}
}

func TestBlockedSummaryReasonFlagsTimeoutErrors(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"run ollama summary: Post \"http://127.0.0.1:11434/api/chat\": context deadline exceeded",
		"run summarize: signal: killed",
		"run summarize: request timed out",
	} {
		reason, ok := blockedSummaryReason(errors.New(message))
		if !ok {
			t.Fatalf("expected timeout-like summary error to be blocked: %q", message)
		}
		if reason == "" {
			t.Fatalf("expected blocked reason for %q", message)
		}
	}
}

func TestClassifyTerminalExtractErrorMarksRepeatedTLSFailuresDead(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		ExtractStatus:       "error",
		ExtractFailureKind:  "tls_certificate",
		ExtractFailureCount: 2,
	}

	status, errorText, terminal := classifyTerminalExtractError(source, errors.New("unable to verify the first certificate"))
	if !terminal {
		t.Fatal("expected repeated tls failures to become terminal")
	}
	if status != "dead" {
		t.Fatalf("expected dead status, got %q", status)
	}
	if !strings.Contains(errorText, "3 consecutive tls certificate failures") {
		t.Fatalf("unexpected terminal error text: %q", errorText)
	}
}

func TestClassifyTerminalExtractErrorKeepsEarlyConnectivityFailuresRetryable(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		ExtractStatus:       "error",
		ExtractFailureKind:  "connectivity",
		ExtractFailureCount: 1,
	}

	if status, errorText, terminal := classifyTerminalExtractError(source, errors.New("Unable to connect. Is the computer able to access the url?")); terminal {
		t.Fatalf("expected early connectivity failures to stay retryable, got status=%q error=%q", status, errorText)
	}
}

func TestClassifyTerminalExtractErrorMarksRepeatedAccessDeniedDead(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		ExtractStatus:       "error",
		ExtractFailureKind:  "unknown",
		ExtractFailureCount: 2,
	}

	status, errorText, terminal := classifyTerminalExtractError(source, errors.New("run summarize: Failed to fetch HTML document (status 403)"))
	if !terminal {
		t.Fatal("expected repeated access denied failures to become terminal")
	}
	if status != "dead" {
		t.Fatalf("expected dead status, got %q", status)
	}
	if !strings.Contains(errorText, "3 consecutive http access denied failures") {
		t.Fatalf("unexpected terminal error text: %q", errorText)
	}
}

func TestClassifyTerminalExtractErrorMarksUnsupportedFilesDeadImmediately(t *testing.T) {
	t.Parallel()

	status, errorText, terminal := classifyTerminalExtractError(model.SourceDocument{}, errors.New("Unsupported file type: install (application/x-install-instructions)"))
	if !terminal {
		t.Fatal("expected unsupported files to become terminal")
	}
	if status != "dead" {
		t.Fatalf("expected dead status, got %q", status)
	}
	if !strings.Contains(errorText, "unsupported file failures") {
		t.Fatalf("unexpected terminal error text: %q", errorText)
	}
}

func TestClassifyTerminalExtractErrorMarksUnknownFailuresDeadAfterFive(t *testing.T) {
	t.Parallel()

	for name, storedKind := range map[string]string{
		"unknown": "unknown",
		"legacy":  "",
	} {
		t.Run(name, func(t *testing.T) {
			source := model.SourceDocument{
				ExtractStatus:       "error",
				ExtractFailureKind:  storedKind,
				ExtractFailureCount: 4,
			}

			status, errorText, terminal := classifyTerminalExtractError(source, errors.New("run summarize: unexpected extractor failure"))
			if !terminal {
				t.Fatal("expected repeated unknown failures to become terminal")
			}
			if status != "dead" {
				t.Fatalf("expected dead status, got %q", status)
			}
			if !strings.Contains(errorText, "5 consecutive unclassified failures") {
				t.Fatalf("unexpected terminal error text: %q", errorText)
			}
		})
	}
}

func TestSaveSourceFailureKeepsTerminalSourceTerminal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-terminal-source",
		SourceType:   "x_bookmark",
		ExternalID:   "test-terminal-source",
		CanonicalURL: "https://x.com/example/status/test-terminal-source",
		Title:        "test terminal source",
		ContentHash:  "item-hash-terminal-source",
		NotePath:     "items/x/2026/test-terminal-source.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-terminal-source",
		OriginalURL:   "https://example.invalid/source",
		CanonicalURL:  "https://example.invalid/source",
		NormalizedURL: "https://example.invalid/source",
		SourceType:    "web",
		Domain:        "example.invalid",
		NotePath:      "sources/web/test-terminal-source.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		Status:      "dead",
		Error:       "host does not resolve: example.invalid",
		Tool:        "summarize",
		ToolVersion: "test",
	}, ""); err != nil {
		t.Fatalf("SaveSourceExtraction dead: %v", err)
	}
	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}

	if err := saveSourceFailure(context.Background(), st, source, model.ExtractResult{
		Status:      "error",
		Error:       "run summarize: fetch failed",
		Tool:        "summarize",
		ToolVersion: "test",
	}, Options{Summarize: false}, "test", ""); err != nil {
		t.Fatalf("saveSourceFailure: %v", err)
	}

	updated, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID updated: %v", err)
	}
	if updated.ExtractStatus != "dead" {
		t.Fatalf("expected terminal status to be preserved, got %q", updated.ExtractStatus)
	}
	if updated.ExtractFailureKind != "fetch_failed" {
		t.Fatalf("expected latest failure kind to be stored, got %q", updated.ExtractFailureKind)
	}
	if updated.ExtractFailureCount != 2 {
		t.Fatalf("expected consecutive failure count 2, got %d", updated.ExtractFailureCount)
	}
}

func TestRejectExtractFailureFlagsXArticleErrorShellAsRetryableError(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		CanonicalURL: "https://x.com/i/article/2044276671923249152",
	}
	extract := model.ExtractResult{
		Content: "Something went wrong, but don’t fret — let’s give it another shot. Try again Some privacy related extensions may cause issues on x.com. Please disable them and try again.",
	}

	failure, reject := rejectExtractFailure(source, extract)
	if !reject {
		t.Fatal("expected x article error shell to be rejected")
	}
	if failure.Status != "error" {
		t.Fatalf("expected error failure, got %+v", failure)
	}
	if !strings.Contains(failure.Error, "x article returned") {
		t.Fatalf("unexpected reject reason: %q", failure.Error)
	}
}

func TestRejectExtractFailureFlagsShortLocalXArticlePreviewAsRetryableError(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{
		CanonicalURL: "https://x.com/i/article/2044276671923249152",
	}
	extract := model.ExtractResult{
		Content:     strings.Repeat("a", minXArticlePreviewExtractChars-1),
		Tool:        "x-hydration",
		ToolVersion: "local-article-preview-cache",
	}

	failure, reject := rejectExtractFailure(source, extract)
	if !reject {
		t.Fatal("expected short x article preview to be rejected")
	}
	if failure.Status != "error" {
		t.Fatalf("expected error failure, got %+v", failure)
	}
	if !strings.Contains(failure.Error, "short preview snippet") {
		t.Fatalf("unexpected reject reason: %q", failure.Error)
	}
}

func TestRejectExtractFailureFlagsSubstackSubscriptionShellAsEmpty(t *testing.T) {
	t.Parallel()

	extract := model.ExtractResult{
		Content: "Irregular Republic Productions\n18 Series Army Veteran. Conservative. Geopolitics.\nBy subscribing, you agree Substack's Terms of Use, and acknowledge its Information Collection Notice and Privacy Policy.",
	}

	failure, reject := rejectExtractFailure(model.SourceDocument{}, extract)
	if !reject {
		t.Fatal("expected subscription boilerplate shell to be rejected")
	}
	if failure.Status != "empty" {
		t.Fatalf("expected empty failure, got %+v", failure)
	}
	if !strings.Contains(failure.Error, "subscription boilerplate") {
		t.Fatalf("unexpected reject reason: %q", failure.Error)
	}
}

func TestRejectExtractFailureFlagsSubstackInboxNavigationShellAsEmpty(t *testing.T) {
	t.Parallel()

	extract := model.ExtractResult{
		Content: "- Shanaka Anslem Perera\nHomeSubscriptionsChatActivityExploreProfileCreateAllListenPaidSavedHistorySort byPriorityRecentGet app",
	}

	failure, reject := rejectExtractFailure(model.SourceDocument{}, extract)
	if !reject {
		t.Fatal("expected inbox navigation shell to be rejected")
	}
	if failure.Status != "empty" {
		t.Fatalf("expected empty failure, got %+v", failure)
	}
	if !strings.Contains(failure.Error, "inbox/navigation chrome") {
		t.Fatalf("unexpected reject reason: %q", failure.Error)
	}
}

func TestNormalizeExtractStripsKnownPaywallNoise(t *testing.T) {
	t.Parallel()

	extract := model.ExtractResult{
		Content: "Lead sentence.\nSecond sentence.\nContinue reading this post for free, courtesy of Sam Cooper.\nOr purchase a paid subscription.",
	}

	normalized, changed := normalizeExtract(model.SourceDocument{}, extract)
	if !changed {
		t.Fatal("expected paywall noise to be stripped")
	}
	if normalized.Content != "Lead sentence.\nSecond sentence." {
		t.Fatalf("unexpected normalized content: %q", normalized.Content)
	}
}

func TestRunSourceIDsRejectsStoredXArticleErrorShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	candidate := model.SourceCandidate{
		SourceKey:     "src:test-x-article-shell",
		CanonicalURL:  "https://x.com/i/article/2044276671923249152",
		NormalizedURL: "https://x.com/i/article/2044276671923249152",
		SourceType:    "link",
		Domain:        "x.com",
		NotePath:      "sources/x/src-test-x-article-shell.md",
		OriginalURL:   "https://x.com/i/article/2044276671923249152",
	}
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-x-article-shell-item",
		SourceType:   "x_bookmark",
		ExternalID:   "2044276671923249152",
		CanonicalURL: "https://x.com/example/status/2044276671923249152",
		Title:        "test item",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2044276671923249152.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, candidate)
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: candidate.CanonicalURL,
		FinalURL:     candidate.CanonicalURL,
		Content:      "Something went wrong, but don’t fret — let’s give it another shot. Try again Some privacy related extensions may cause issues on x.com. Please disable them and try again.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, hashText("Something went wrong, but don’t fret — let’s give it another shot. Try again Some privacy related extensions may cause issues on x.com. Please disable them and try again.")); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Summarize: true,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 0 {
		t.Fatalf("expected no summary for rejected x article shell, got %+v", stats)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected one error to be recorded, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != "error" {
		t.Fatalf("expected extract status error, got %q", source.ExtractStatus)
	}
	if !strings.Contains(source.ExtractError, "x article returned an X error shell") {
		t.Fatalf("unexpected extract error: %q", source.ExtractError)
	}
}

func TestRunSourceIDsMarksRepeatedXArticleShellTerminal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	candidate := model.SourceCandidate{
		SourceKey:     "src:test-x-article-shell-terminal",
		CanonicalURL:  "https://x.com/i/article/2044276671923249152",
		NormalizedURL: "https://x.com/i/article/2044276671923249152",
		SourceType:    "x_article",
		Domain:        "x.com",
		NotePath:      "sources/x/article-shell-terminal.md",
		OriginalURL:   "https://x.com/i/article/2044276671923249152",
	}
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-x-article-shell-terminal-item",
		SourceType:   "x_quote",
		ExternalID:   "2044276671923249152",
		CanonicalURL: "https://x.com/example/status/2044276671923249152",
		Title:        "test item",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2044276671923249152.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, candidate)
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	shellText := "Something went wrong, but don’t fret — let’s give it another shot. Try again Some privacy related extensions may cause issues on x.com. Please disable them and try again."
	hydration := model.XHydration{
		FullText:  "quoted parent",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"source":"graphql","snapshot":{"id":"2044276671923249152","text":"quoted parent"},"raw":{"article":{"rest_id":"2044276671923249152","title":"Quoted article","preview_text":"` + shellText + `"}}}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID first: %v", err)
	}
	failure := model.ExtractResult{
		Status:      "error",
		Error:       "x article returned an X error shell instead of article content",
		Tool:        "summarize",
		ToolVersion: "test",
	}
	if err := saveSourceFailure(context.Background(), st, source, failure, Options{Summarize: true}, "test", "test"); err != nil {
		t.Fatalf("saveSourceFailure first: %v", err)
	}
	source, err = st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID second: %v", err)
	}
	if err := saveSourceFailure(context.Background(), st, source, failure, Options{Summarize: true}, "test", "test"); err != nil {
		t.Fatalf("saveSourceFailure second: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Summarize: true,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected one error to be recorded, got %+v", stats)
	}

	source, err = st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != "dead" {
		t.Fatalf("expected extract status dead after repeated x article shell failures, got %q", source.ExtractStatus)
	}
	if !strings.Contains(source.ExtractError, "marking source dead after 3 consecutive x article shell failures") {
		t.Fatalf("unexpected extract error: %q", source.ExtractError)
	}
	if source.SummaryStatus != "skipped" {
		t.Fatalf("expected summary status skipped, got %q", source.SummaryStatus)
	}
}

func TestRunSourceIDsFallsBackToRemoteFetchAfterShortLocalXArticlePreview(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	articleURL := "https://x.com/example/article/2044276671923249152"
	installSourceEnrichXArticleExtractFakeSummarize(t, root, articleURL, strings.Repeat("remote article body ", 40))

	now := time.Now().UTC()
	candidate := model.SourceCandidate{
		SourceKey:     "src:test-x-article-preview-fallback",
		CanonicalURL:  articleURL,
		NormalizedURL: articleURL,
		SourceType:    "x_article",
		Domain:        "x.com",
		NotePath:      "sources/x/article-preview-fallback.md",
		OriginalURL:   articleURL,
	}
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-x-article-preview-fallback-item",
		SourceType:   "x_quote",
		ExternalID:   "2044276671923249152",
		CanonicalURL: "https://x.com/example/status/2044276671923249152",
		Title:        "test item",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2044276671923249152.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, candidate)
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "quoted parent",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"source":"graphql","snapshot":{"id":"2044276671923249152","text":"quoted parent"},"raw":{"article":{"rest_id":"2044276671923249152","title":"Quoted article","preview_text":"` + strings.Repeat("x", minXArticlePreviewExtractChars-2) + `"}}}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Summarize: false,
		Timeout:   5 * time.Second,
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.Errors != 0 {
		source, sourceErr := st.GetSourceByID(context.Background(), link.SourceID)
		if sourceErr != nil {
			t.Fatalf("expected no errors, got %+v (and failed to load source: %v)", stats, sourceErr)
		}
		t.Fatalf("expected no errors, got %+v; source status=%q tool=%q tool_version=%q error=%q extracted=%q", stats, source.ExtractStatus, source.ExtractTool, source.ExtractToolVersion, source.ExtractError, source.ExtractedText)
	}
	if stats.SourcesExtracted != 1 {
		t.Fatalf("expected one extracted source, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != "ok" {
		t.Fatalf("expected extract status ok, got %q", source.ExtractStatus)
	}
	if source.ExtractTool != "summarize" {
		t.Fatalf("expected remote summarize extract tool, got %q", source.ExtractTool)
	}
	if len(strings.TrimSpace(source.ExtractedText)) < minXArticlePreviewExtractChars {
		t.Fatalf("expected recovered article body to exceed preview threshold, got %d chars", len(strings.TrimSpace(source.ExtractedText)))
	}
}

func TestRunSourceIDsBlocksEmptyExtractSummaryAndStopsRequeue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-empty-summary-item",
		SourceType:   "x_bookmark",
		ExternalID:   "2044276671923249999",
		CanonicalURL: "https://x.com/example/status/2044276671923249999",
		Title:        "test item",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2044276671923249999.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-empty-summary",
		CanonicalURL:  "https://youtube.com/live/example",
		NormalizedURL: "https://youtube.com/live/example",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      "sources/youtube/src-test-empty-summary.md",
		OriginalURL:   "https://youtube.com/live/example",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	emptyExtract := model.ExtractResult{
		CanonicalURL: "https://youtube.com/live/example",
		FinalURL:     "https://youtube.com/live/example",
		Status:       "empty",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, emptyExtract, ""); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Summarize: true,
		Model:     "openrouter/qwen/qwen3.5-27b",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 0 {
		t.Fatalf("expected no successful summary for empty extract, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.SummaryStatus != "blocked" {
		t.Fatalf("expected summary status blocked, got %q", source.SummaryStatus)
	}
	if source.SummaryError != "no extracted content available for summary" {
		t.Fatalf("unexpected summary error: %q", source.SummaryError)
	}

	pending, err := st.ListSourcesForEnrichment(context.Background(), 50, false, true, SummaryPromptVersion, summarizecli.SummaryToolName("openrouter/qwen/qwen3.5-27b"), summarizecli.SummaryToolVersion(context.Background(), "", "openrouter/qwen/qwen3.5-27b"))
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected blocked summary source to drop out of enrichment selection, got %d candidates", len(pending))
	}
}

func TestRunSourceIDsBlocksTimedOutStoredExtractSummaryAndStopsRequeue(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichSlowFakeSummarize(t, root)

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "manual:slow-summary-item",
		SourceType:   "manual_link",
		ExternalID:   "slow-summary-item",
		CanonicalURL: "https://example.com/slow-summary-item",
		Title:        "slow summary item",
		ContentHash:  "slow-summary-item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/manual/slow-summary-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:slow-summary",
		CanonicalURL:  "https://example.com/slow-summary",
		NormalizedURL: "https://example.com/slow-summary",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/src-slow-summary.md",
		OriginalURL:   "https://example.com/slow-summary",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/slow-summary",
		FinalURL:     "https://example.com/slow-summary",
		Title:        "Slow Summary",
		Content:      "stored extract content that should be summarized",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "slow-summary-hash"); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Summarize: true,
		Model:     "cli/test/sourceenrich",
		Timeout:   100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 0 {
		t.Fatalf("expected no successful summary for timed out extract, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.SummaryStatus != "blocked" {
		t.Fatalf("expected summary status blocked, got %q error=%q", source.SummaryStatus, source.SummaryError)
	}
	if !strings.Contains(strings.ToLower(source.SummaryError), "signal: killed") &&
		!strings.Contains(strings.ToLower(source.SummaryError), "context deadline") &&
		!strings.Contains(strings.ToLower(source.SummaryError), "timeout") {
		t.Fatalf("unexpected timeout summary error: %q", source.SummaryError)
	}

	pending, err := st.ListSourcesForEnrichment(context.Background(), 50, false, true, SummaryPromptVersion, summarizecli.SummaryToolName("cli/test/sourceenrich"), summarizecli.SummaryToolVersion(context.Background(), "", "cli/test/sourceenrich"))
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected blocked timeout summary source to drop out of enrichment selection, got %d candidates", len(pending))
	}
}

func TestRunPendingSkipsPlaceholderSummaryRepairAndStopsRequeue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "test:placeholder-summary-repair-item",
		SourceType:   "test",
		ExternalID:   "placeholder-summary-repair-item",
		CanonicalURL: "https://example.com/placeholder-summary-repair-item",
		Title:        "test item",
		ContentHash:  "item-hash",
		LinksJSON:    "[]",
		NotePath:     "items/test/placeholder-summary-repair-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-placeholder-summary-repair-loop",
		CanonicalURL:  "https://example.com/redirect",
		NormalizedURL: "https://example.com/redirect",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/src-test-placeholder-summary-repair-loop.md",
		OriginalURL:   "https://example.com/redirect",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	content := repairLengthPlaceholderExtract()
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/redirect",
		FinalURL:     "https://example.com/redirect",
		Status:       "ok",
		Content:      content,
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test-extract",
	}, hashText(content)); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "old placeholder summary",
		RawJSON:       `{"summary":"old placeholder summary"}`,
		Status:        "ok",
		Model:         "ollama/dbrain:test",
		PromptVersion: SummaryPromptVersion,
		Tool:          summarizecli.DirectSummaryToolName,
		ToolVersion:   "ollama-direct-v1",
		FetchedAt:     now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}

	pending, err := st.ListSourcesForEnrichment(context.Background(), 10, false, true, "", "", "")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment before repair: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected placeholder summary repair candidate, got %d candidates", len(pending))
	}

	stats, _, err := RunPending(context.Background(), cfg, st, Options{
		Limit:     10,
		Summarize: true,
		Model:     "ollama/dbrain:test",
		Binary:    "dbrain-test-missing-summarize",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunPending: %v", err)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no errors, got %+v", stats)
	}
	if stats.SourcesSummarized != 0 {
		t.Fatalf("expected placeholder repair to be skipped, not summarized, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.SummaryStatus != "skipped" {
		t.Fatalf("expected summary status skipped, got %q", source.SummaryStatus)
	}
	if !strings.Contains(source.SummaryError, "placeholder boilerplate") {
		t.Fatalf("unexpected summary error: %q", source.SummaryError)
	}

	pending, err = st.ListSourcesForEnrichment(context.Background(), 10, false, true, "", "", "")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment after repair: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected skipped placeholder summary repair to stop requeueing, got %d candidates", len(pending))
	}
}

func TestRunSourceIDsCancellationDoesNotPersistInterruptFailure(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichSlowFakeSummarize(t, root)

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-cancel-source-item",
		SourceType:   "x_bookmark",
		ExternalID:   "test-cancel-source-item",
		CanonicalURL: "https://x.com/example/status/test-cancel-source-item",
		Title:        "test cancel source item",
		ContentHash:  "item-hash-test-cancel-source-item",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/test-cancel-source-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-cancel-source",
		CanonicalURL:  "https://example.com/slow",
		NormalizedURL: "https://example.com/slow",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/src-test-cancel-source.md",
		OriginalURL:   "https://example.com/slow",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	stats, _, err := RunSourceIDs(ctx, cfg, st, []int64{link.SourceID}, Options{
		Summarize: false,
		Timeout:   5 * time.Second,
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no persisted extraction errors on cancel, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != "" || source.ExtractError != "" {
		t.Fatalf("expected no extract failure persisted on cancel, got status=%q error=%q", source.ExtractStatus, source.ExtractError)
	}
	if source.SummaryStatus != "" || source.SummaryError != "" {
		t.Fatalf("expected no summary failure persisted on cancel, got status=%q error=%q", source.SummaryStatus, source.SummaryError)
	}
}

func TestRunSourceIDsLogsProgressForSlowSources(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichSlowFakeSummarize(t, root)

	now := time.Now().UTC()
	sourceIDs := make([]int64, 0, 2)
	for i := 0; i < 2; i++ {
		itemResult, err := st.UpsertItem(context.Background(), model.Item{
			SourceKey:    fmt.Sprintf("x:test-progress-source-item-%d", i),
			SourceType:   "x_bookmark",
			ExternalID:   fmt.Sprintf("test-progress-source-item-%d", i),
			CanonicalURL: fmt.Sprintf("https://x.com/example/status/test-progress-source-item-%d", i),
			Title:        fmt.Sprintf("test progress source item %d", i),
			ContentHash:  fmt.Sprintf("item-hash-test-progress-source-item-%d", i),
			LinksJSON:    "[]",
			NotePath:     fmt.Sprintf("items/x/2026/test-progress-source-item-%d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		})
		if err != nil {
			t.Fatalf("UpsertItem(%d): %v", i, err)
		}

		link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
			SourceKey:     fmt.Sprintf("src:test-progress-source-%d", i),
			CanonicalURL:  fmt.Sprintf("https://example.com/slow/%d", i),
			NormalizedURL: fmt.Sprintf("https://example.com/slow/%d", i),
			SourceType:    "web",
			Domain:        "example.com",
			NotePath:      fmt.Sprintf("sources/web/src-test-progress-source-%d.md", i),
			OriginalURL:   fmt.Sprintf("https://example.com/slow/%d", i),
		})
		if err != nil {
			t.Fatalf("UpsertSourceLink(%d): %v", i, err)
		}
		sourceIDs = append(sourceIDs, link.SourceID)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, sourceIDs, Options{
		Summarize:        false,
		Concurrency:      2,
		Timeout:          200 * time.Millisecond,
		ProgressInterval: 20 * time.Millisecond,
		Logger:           logger,
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.Errors != 2 {
		t.Fatalf("expected 2 extraction errors from timed out slow sources, got %+v", stats)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, `msg="source enrichment progress"`) {
		t.Fatalf("expected source enrichment progress log, got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "oldest_source_key=src:test-progress-source-0") &&
		!strings.Contains(logOutput, "oldest_source_key=src:test-progress-source-1") {
		t.Fatalf("expected progress log to include an active source key, got:\n%s", logOutput)
	}
}

func TestSelectSourceDocumentsHonorsLimit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ordered := []int64{1, 2, 3}
	byID := map[int64]model.SourceDocument{
		1: {
			ID:                   1,
			SourceKey:            "src:one",
			ExtractStatus:        "ok",
			SummaryStatus:        "error",
			ContentHash:          "hash-1",
			SummaryContentHash:   "hash-0",
			SummaryPromptVersion: "old",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
		2: {
			ID:                   2,
			SourceKey:            "src:two",
			ExtractStatus:        "ok",
			SummaryStatus:        "error",
			ContentHash:          "hash-2",
			SummaryContentHash:   "hash-1",
			SummaryPromptVersion: "old",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
		3: {
			ID:                   3,
			SourceKey:            "src:three",
			ExtractStatus:        "ok",
			SummaryStatus:        "error",
			ContentHash:          "hash-3",
			SummaryContentHash:   "hash-2",
			SummaryPromptVersion: "old",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
	}

	selected := selectSourceDocuments(ordered, byID, Options{
		Limit:     2,
		Summarize: true,
	}, "", summarizecli.ToolName, "0.13.0")

	if len(selected) != 2 {
		t.Fatalf("expected 2 selected sources, got %d", len(selected))
	}
	if selected[0].SourceKey != "src:one" || selected[1].SourceKey != "src:two" {
		t.Fatalf("unexpected selected sources: %s, %s", selected[0].SourceKey, selected[1].SourceKey)
	}
}

func TestSelectSourceDocumentsAcceptsCurrentSummaryCoverage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ordered := []int64{1}
	byID := map[int64]model.SourceDocument{
		1: {
			ID:                   1,
			SourceKey:            "src:one",
			ExtractStatus:        "ok",
			SummaryStatus:        "ok",
			ContentHash:          "hash-1",
			SummaryContentHash:   "hash-1",
			SummaryPromptVersion: "old-version",
			SummaryTool:          "summarize",
			SummaryToolVersion:   "0.10.0",
			UpdatedAt:            now,
		},
	}

	selected := selectSourceDocuments(ordered, byID, Options{
		Limit:                1,
		Summarize:            true,
		AcceptCurrentSummary: true,
	}, "", summarizecli.DirectOpenRouterToolName, "openrouter-direct-v1")

	if len(selected) != 0 {
		t.Fatalf("expected current covered source to be skipped, got %+v", selected)
	}
}

func TestSummaryInputFileUsesTempDirAndCleansUp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	path, cleanup, err := summaryInputFile(cfg, model.ExtractResult{
		Title:   "Example",
		Content: "Body",
	})
	if err != nil {
		t.Fatalf("summaryInputFile: %v", err)
	}
	if path == "" {
		t.Fatal("expected summary input path")
	}

	rel, err := filepath.Rel(cfg.TempDir, path)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("expected summary input file under %s, got %s", cfg.TempDir, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected summary input file to exist before cleanup: %v", err)
	}

	cleanup()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected summary input file to be removed after cleanup, got %v", err)
	}
}

func TestMaybeTranscribeYouTubeAudioFallbackUsesMacWhisperWhenAvailable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	ytDLPPath := installSourceEnrichFallbackFakeYTDLP(t, root, "chrome:Default")
	macWhisperPath := installSourceEnrichFallbackFakeMacWhisper(t, root, "", "transcript from fake macwhisper")

	source := model.SourceDocument{
		SourceType:   "youtube",
		CanonicalURL: "https://www.youtube.com/watch?v=test123",
		Title:        "Fallback title",
		Description:  "Fallback description",
	}
	extract := model.ExtractResult{
		Title:       "- YouTube",
		Description: "",
		SiteName:    "youtube.com",
		Content:     "Enjoy the videos and music you love...",
		RawJSON:     `{"extracted":{"transcriptSource":"unavailable","transcriptionProvider":null,"transcriptCharacters":null}}`,
	}

	fallback, changed, err := MaybeTranscribeYouTubeAudioFallback(context.Background(), cfg, source, extract, Options{
		YouTubeBrowser:     "chrome",
		YouTubeProfile:     "Default",
		YouTubeTranscriber: "auto",
		YTDLPBinary:        ytDLPPath,
		MacWhisperBinary:   macWhisperPath,
		Timeout:            5 * time.Second,
	})
	if err != nil {
		t.Fatalf("MaybeTranscribeYouTubeAudioFallback: %v", err)
	}
	if !changed {
		t.Fatal("expected audio transcription fallback to run")
	}
	if !strings.Contains(fallback.Content, "transcript from fake macwhisper") {
		t.Fatalf("unexpected fallback transcript: %q", fallback.Content)
	}
	if fallback.Tool != "macwhisper" {
		t.Fatalf("unexpected fallback tool: %q", fallback.Tool)
	}
	if !strings.Contains(fallback.RawJSON, `"transcriptionProvider":"macwhisper"`) {
		t.Fatalf("unexpected fallback raw json: %q", fallback.RawJSON)
	}
}

func TestRunSourceIDsUsesStoredExtractForStaleSummary(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichFakeSummarize(t, root)

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "github_star:test/repo",
		SourceType:   "github_star",
		ExternalID:   "test/repo",
		CanonicalURL: "https://github.com/test/repo",
		Title:        "test/repo",
		ContentHash:  "item-hash",
		NotePath:     vault.NoteRelativePath("github", "2026", "test__repo"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:github-test-repo",
		OriginalURL:   "https://github.com/test/repo",
		CanonicalURL:  "https://github.com/test/repo",
		NormalizedURL: "https://github.com/test/repo",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      vault.SourceNoteRelativePath("github", "test-repo"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/repo",
		FinalURL:     "https://github.com/test/repo",
		Title:        "test/repo",
		Description:  "A useful repo",
		SiteName:     "GitHub",
		Content:      "README CONTENT FROM GITHUB API",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "github-source-hash"); err != nil {
		t.Fatalf("save extraction: %v", err)
	}

	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "old summary",
		RawJSON:       `{"summary":"old summary"}`,
		Model:         "test/model",
		PromptVersion: "old-version",
		Status:        "ok",
		FetchedAt:     now.Add(-time.Hour),
		Tool:          "summarize",
		ToolVersion:   "0.10.0",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:                 10,
		Summarize:             true,
		Model:                 "cli/test/sourceenrich",
		Length:                "short",
		Timeout:               5 * time.Second,
		ExactSummaryFreshness: true,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d", stats.SourcesSummarized)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "summary from stored extract" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func TestRunSourceIDsUsesPreferredCLIProviderForGenericSummary(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, ".summarize"), 0o755); err != nil {
		t.Fatalf("create summarize home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".summarize", "cli-state.json"), []byte(`{"lastSuccessfulProvider":"claude"}`), 0o644); err != nil {
		t.Fatalf("write cli-state: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("DBRAIN_SUMMARY_MODEL", "")
	t.Setenv("SUMMARIZE_MODEL", "")

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichGenericFakeSummarize(t, root)

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-generic",
		SourceType:   "x_bookmark",
		ExternalID:   "test-generic",
		CanonicalURL: "https://x.com/example/status/test-generic",
		Title:        "test generic",
		ContentHash:  "item-hash-generic",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-generic"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:generic-test",
		OriginalURL:   "https://example.com/post",
		CanonicalURL:  "https://example.com/post",
		NormalizedURL: "https://example.com/post",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "generic-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Length:    "short",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d", stats.SourcesSummarized)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.SummaryText != "summary from generic path" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func TestRunSourceIDsRetriesRedirectingSourceWithResolvedURL(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichRedirectFakeSummarize(t, root)

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-redirect",
		SourceType:   "x_bookmark",
		ExternalID:   "test-redirect",
		CanonicalURL: "https://x.com/example/status/test-redirect",
		Title:        "test redirect",
		ContentHash:  "item-hash-redirect",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-redirect"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:redirect-test",
		OriginalURL:   "https://example.com/original",
		CanonicalURL:  "https://example.com/original",
		NormalizedURL: "https://example.com/original",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "redirect-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Model:     "cli/test/sourceenrich",
		Length:    "short",
		Timeout:   5 * time.Second,
		ResolveRedirectURL: func(context.Context, string) (string, error) {
			return "https://example.com/redirected", nil
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d", stats.SourcesSummarized)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no errors, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.CanonicalURL != "https://example.com/redirected" {
		t.Fatalf("expected canonical url to update to redirected target, got %q", source.CanonicalURL)
	}
	if source.ExtractStatus != "ok" {
		t.Fatalf("expected extract status ok, got %q", source.ExtractStatus)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "summary from redirected path" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func TestRunSourceIDsUsesDirectOllamaSummaryAfterExtraction(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichDirectOllamaExtractFakeSummarize(t, root)

	var captured summarizecliTestRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: sourceEnrichRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode ollama request: %v", err)
		}
		if captured.Think == nil || *captured.Think {
			t.Fatalf("expected direct ollama request to disable thinking, got %#v", captured.Think)
		}
		respBody := `{"model":"qwen3.6:35b","message":{"role":"assistant","content":"summary from direct ollama"},"done":true}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	})}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", "http://ollama.test")

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-direct-ollama",
		SourceType:   "x_bookmark",
		ExternalID:   "test-direct-ollama",
		CanonicalURL: "https://x.com/example/status/test-direct-ollama",
		Title:        "test direct ollama",
		ContentHash:  "item-hash-direct-ollama",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-direct-ollama"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:direct-ollama-test",
		OriginalURL:   "https://example.com/direct-ollama",
		CanonicalURL:  "https://example.com/direct-ollama",
		NormalizedURL: "https://example.com/direct-ollama",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "direct-ollama-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Model:     "ollama/qwen3.6:35b",
		Length:    "medium",
		Timeout:   5 * time.Second,
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d (summary_status=%q summary_error=%q summary_text=%q)", stats.SourcesSummarized, source.SummaryStatus, source.SummaryError, source.SummaryText)
	}
	if source.ExtractStatus != "ok" {
		t.Fatalf("expected extract status ok, got %q", source.ExtractStatus)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "summary from direct ollama" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
	if source.SummaryModel != "ollama/qwen3.6:35b" {
		t.Fatalf("unexpected summary model: %q", source.SummaryModel)
	}
	if source.SummaryTool != summarizecli.DirectSummaryToolName {
		t.Fatalf("unexpected summary tool: %q", source.SummaryTool)
	}
	if source.SummaryToolVersion != summarizecli.SummaryToolVersion(context.Background(), "summarize", "ollama/qwen3.6:35b") {
		t.Fatalf("unexpected summary tool version: %q", source.SummaryToolVersion)
	}
	if captured.Model != "qwen3.6:35b" {
		t.Fatalf("unexpected ollama model: %q", captured.Model)
	}
	if len(captured.Messages) < 2 {
		t.Fatalf("expected prompt and user messages, got %+v", captured.Messages)
	}
	if !strings.Contains(captured.Messages[len(captured.Messages)-1].Content, "extracted body for direct ollama") {
		t.Fatalf("expected extracted body in ollama user message, got %+v", captured.Messages[len(captured.Messages)-1])
	}
}

func TestRunSourceIDsUsesDirectOpenRouterSummaryAfterExtraction(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichDirectOllamaExtractFakeSummarize(t, root)

	var captured summarizecliTestRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: sourceEnrichRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected openrouter path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-openrouter-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode openrouter request: %v", err)
		}
		respBody := `{"model":"qwen/qwen3.5-27b","choices":[{"message":{"role":"assistant","content":"summary from direct openrouter"}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	})}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})
	t.Setenv("DBRAIN_OPENROUTER_BASE_URL", "https://openrouter.test")
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "test-openrouter-key")

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-direct-openrouter",
		SourceType:   "x_bookmark",
		ExternalID:   "test-direct-openrouter",
		CanonicalURL: "https://x.com/example/status/test-direct-openrouter",
		Title:        "test direct openrouter",
		ContentHash:  "item-hash-direct-openrouter",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-direct-openrouter"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:direct-openrouter-test",
		OriginalURL:   "https://example.com/direct-openrouter",
		CanonicalURL:  "https://example.com/direct-openrouter",
		NormalizedURL: "https://example.com/direct-openrouter",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "direct-openrouter-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Model:     "openrouter/qwen/qwen3.5-27b",
		Length:    "medium",
		Timeout:   5 * time.Second,
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d (summary_status=%q summary_error=%q summary_text=%q)", stats.SourcesSummarized, source.SummaryStatus, source.SummaryError, source.SummaryText)
	}
	if source.ExtractStatus != "ok" {
		t.Fatalf("expected extract status ok, got %q", source.ExtractStatus)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "summary from direct openrouter" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
	if source.SummaryModel != "openrouter/qwen/qwen3.5-27b" {
		t.Fatalf("unexpected summary model: %q", source.SummaryModel)
	}
	if source.SummaryTool != summarizecli.DirectOpenRouterToolName {
		t.Fatalf("unexpected summary tool: %q", source.SummaryTool)
	}
	if source.SummaryToolVersion != summarizecli.SummaryToolVersion(context.Background(), "summarize", "openrouter/qwen/qwen3.5-27b") {
		t.Fatalf("unexpected summary tool version: %q", source.SummaryToolVersion)
	}
	if captured.Model != "qwen/qwen3.5-27b" {
		t.Fatalf("unexpected openrouter model: %q", captured.Model)
	}
	if len(captured.Messages) < 2 {
		t.Fatalf("expected prompt and user messages, got %+v", captured.Messages)
	}
	if !strings.Contains(captured.Messages[len(captured.Messages)-1].Content, "extracted body for direct ollama") {
		t.Fatalf("expected extracted body in openrouter user message, got %+v", captured.Messages[len(captured.Messages)-1])
	}
}

func TestProcessSingleSourceRendersSourceNoteImmediately(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	installSourceEnrichDirectOllamaExtractFakeSummarize(t, root)

	var captured summarizecliTestRequest
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: sourceEnrichRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode ollama request: %v", err)
		}
		if captured.Think == nil || *captured.Think {
			t.Fatalf("expected direct ollama request to disable thinking, got %#v", captured.Think)
		}
		respBody := `{"model":"qwen3.6:35b","message":{"role":"assistant","content":"summary from direct ollama"},"done":true}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	})}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", "http://ollama.test")

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-direct-ollama-note",
		SourceType:   "x_bookmark",
		ExternalID:   "test-direct-ollama-note",
		CanonicalURL: "https://x.com/example/status/test-direct-ollama-note",
		Title:        "test direct ollama note",
		ContentHash:  "item-hash-direct-ollama-note",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-direct-ollama-note"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:direct-ollama-note-test",
		OriginalURL:   "https://example.com/direct-ollama",
		CanonicalURL:  "https://example.com/direct-ollama",
		NormalizedURL: "https://example.com/direct-ollama",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      vault.SourceNoteRelativePath("web", "direct-ollama-note-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	notePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir note dir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("stale note body"), 0o644); err != nil {
		t.Fatalf("write stale note: %v", err)
	}

	result := processSingleSource(context.Background(), cfg, st, source, Options{
		Limit:     10,
		Summarize: true,
		Model:     "ollama/qwen3.6:35b",
		Length:    "medium",
		Timeout:   5 * time.Second,
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	}, summarizecli.Version(context.Background(), ""), summarizecli.SummaryToolVersion(context.Background(), "", "ollama/qwen3.6:35b"))
	if result.Err != nil {
		t.Fatalf("processSingleSource: %v", result.Err)
	}
	if result.Stats.SourcesRendered != 1 {
		t.Fatalf("expected 1 rendered source note, got %+v", result.Stats)
	}

	noteBytes, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	noteText := string(noteBytes)
	if strings.Contains(noteText, "stale note body") {
		t.Fatalf("expected stale note to be rewritten, got %q", noteText)
	}
	if !strings.Contains(noteText, "summary from direct ollama") {
		t.Fatalf("expected rendered note to include summary, got %q", noteText)
	}
	if !strings.Contains(noteText, "extracted body for direct ollama") {
		t.Fatalf("expected rendered note to include extract, got %q", noteText)
	}
	if captured.Model != "qwen3.6:35b" {
		t.Fatalf("unexpected ollama model: %q", captured.Model)
	}
}

func TestRunSourceIDsMarksDeadHostsTerminal(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-dead-host",
		SourceType:   "x_bookmark",
		ExternalID:   "dead-host",
		CanonicalURL: "https://x.com/example/status/dead-host",
		Title:        "dead host",
		ContentHash:  "item-hash-dead-host",
		NotePath:     vault.NoteRelativePath("x", "2026", "dead-host"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:dead-host-test",
		OriginalURL:   "https://dead-host.invalid/post",
		CanonicalURL:  "https://dead-host.invalid/post",
		NormalizedURL: "https://dead-host.invalid/post",
		SourceType:    "web",
		Domain:        "dead-host.invalid",
		NotePath:      vault.SourceNoteRelativePath("web", "dead-host-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Timeout:   5 * time.Second,
		ResolveHost: func(context.Context, string) error {
			return &net.DNSError{Err: "no such host", Name: "dead-host.invalid", IsNotFound: true}
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected 1 error, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.ExtractStatus != "dead" {
		t.Fatalf("expected extract status dead, got %q", source.ExtractStatus)
	}
	if !strings.Contains(source.ExtractError, "host does not resolve") {
		t.Fatalf("unexpected extract error: %q", source.ExtractError)
	}
	if source.SummaryStatus != "skipped" {
		t.Fatalf("expected summary status skipped, got %q", source.SummaryStatus)
	}

	backlog, err := st.Backlog(context.Background(), SummaryPromptVersion, summarizecli.ToolName, "")
	if err != nil {
		t.Fatalf("backlog: %v", err)
	}
	if backlog.SourceExtractionPending != 0 || backlog.SourceSummaryPending != 0 {
		t.Fatalf("expected dead host to drop out of backlog, got %+v", backlog)
	}
}

func TestRunSourceIDsRejectsStoredSubstackSubscriptionShell(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	candidate := model.SourceCandidate{
		SourceKey:     "src:test-substack-shell",
		CanonicalURL:  "https://gbnt1952.substack.com/p/civil-war-20",
		NormalizedURL: "https://open.substack.com/pub/gbnt1952/p/civil-war-20",
		SourceType:    "web",
		Domain:        "open.substack.com",
		NotePath:      "sources/web/src-test-substack-shell.md",
		OriginalURL:   "https://gbnt1952.substack.com/p/civil-war-20",
	}
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-substack-shell-item",
		SourceType:   "x_bookmark",
		ExternalID:   "test-substack-shell-item",
		CanonicalURL: "https://x.com/example/status/test-substack-shell-item",
		Title:        "test substack shell item",
		ContentHash:  "item-hash-test-substack-shell-item",
		NotePath:     "items/x/2026/test-substack-shell-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, candidate)
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	content := "Irregular Republic Productions\n18 Series Army Veteran. Conservative. Geopolitics.\nBy subscribing, you agree Substack's Terms of Use, and acknowledge its Information Collection Notice and Privacy Policy."
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: candidate.CanonicalURL,
		FinalURL:     candidate.CanonicalURL,
		Content:      content,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, hashText(content)); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Summarize: true,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 0 {
		t.Fatalf("expected no summary for rejected substack shell, got %+v", stats)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected one rejection to be recorded, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != "empty" {
		t.Fatalf("expected extract status empty, got %q", source.ExtractStatus)
	}
	if source.SummaryStatus != "skipped" {
		t.Fatalf("expected summary status skipped, got %q", source.SummaryStatus)
	}
	if !strings.Contains(source.ExtractError, "subscription boilerplate") {
		t.Fatalf("unexpected extract error: %q", source.ExtractError)
	}
}

func TestRunPendingRepairsShortWaybackSummaryToSkipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-short-wayback-item",
		SourceType:   "x_bookmark",
		ExternalID:   "test-short-wayback-item",
		CanonicalURL: "https://x.com/example/status/test-short-wayback-item",
		Title:        "test short wayback item",
		ContentHash:  "item-hash-test-short-wayback-item",
		NotePath:     "items/x/2026/test-short-wayback-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	candidate := model.SourceCandidate{
		SourceKey:     "src:test-short-wayback",
		OriginalURL:   "https://example.com/title-only",
		CanonicalURL:  "https://example.com/title-only",
		NormalizedURL: "https://example.com/title-only",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-short-wayback.md",
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, candidate)
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	content := "The method to Mark Carney's madness\n\nBy Max Fawcett\n\nOpinion\n\nPolitics\n\nShare this article"
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: candidate.CanonicalURL,
		FinalURL:     "http://web.archive.org/web/20260112031415id_/https://example.com/title-only",
		Content:      content,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         waybackToolName,
		ToolVersion:  waybackToolVersion,
	}, hashText(content)); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "old plausible summary",
		Status:        "ok",
		Model:         "test-model",
		PromptVersion: SummaryPromptVersion,
		Tool:          summarizecli.ToolName,
		ToolVersion:   "test",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}

	stats, _, err := RunPending(context.Background(), cfg, st, Options{
		Limit:     10,
		Summarize: true,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunPending: %v", err)
	}
	if stats.SourcesQueued != 1 {
		t.Fatalf("expected one source queued for repair, got %+v", stats)
	}
	if stats.SourcesSummarized != 0 || stats.Errors != 0 {
		t.Fatalf("expected short wayback summary to be skipped without summarization/error, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.SummaryStatus != "skipped" {
		t.Fatalf("expected summary status skipped, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "" {
		t.Fatalf("expected summary text cleared, got %q", source.SummaryText)
	}
	if !strings.Contains(source.SummaryError, "wayback extract is too short") {
		t.Fatalf("unexpected summary error: %q", source.SummaryError)
	}
}

func TestRunSourceIDsProcessesStoredExtractsWithConcurrency(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	installSourceEnrichFakeSummarize(t, root)

	now := time.Now().UTC()
	sourceIDs := make([]int64, 0, 2)
	for _, value := range []struct {
		itemKey   string
		itemTitle string
		sourceKey string
		url       string
		content   string
	}{
		{
			itemKey:   "github_star:test/repo-one",
			itemTitle: "test/repo-one",
			sourceKey: "src:github-test-repo-one",
			url:       "https://github.com/test/repo-one",
			content:   "README CONTENT FROM GITHUB API",
		},
		{
			itemKey:   "github_star:test/repo-two",
			itemTitle: "test/repo-two",
			sourceKey: "src:github-test-repo-two",
			url:       "https://github.com/test/repo-two",
			content:   "README CONTENT FROM GITHUB API",
		},
	} {
		item := model.Item{
			SourceKey:    value.itemKey,
			SourceType:   "github_star",
			ExternalID:   value.itemTitle,
			CanonicalURL: value.url,
			Title:        value.itemTitle,
			ContentHash:  value.itemTitle + "-item-hash",
			NotePath:     vault.NoteRelativePath("github", "2026", strings.ReplaceAll(value.itemTitle, "/", "__")),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}
		upserted, err := st.UpsertItem(context.Background(), item)
		if err != nil {
			t.Fatalf("upsert item: %v", err)
		}

		link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
			SourceKey:     value.sourceKey,
			OriginalURL:   value.url,
			CanonicalURL:  value.url,
			NormalizedURL: value.url,
			SourceType:    "github",
			Domain:        "github.com",
			NotePath:      vault.SourceNoteRelativePath("github", strings.ReplaceAll(value.itemTitle, "/", "-")),
		})
		if err != nil {
			t.Fatalf("upsert source link: %v", err)
		}
		if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
			CanonicalURL: value.url,
			FinalURL:     value.url,
			Title:        value.itemTitle,
			Description:  "A useful repo",
			SiteName:     "GitHub",
			Content:      value.content,
			Status:       "ok",
			FetchedAt:    now,
			Tool:         "github-api",
			ToolVersion:  "2022-11-28",
		}, value.itemTitle+"-source-hash"); err != nil {
			t.Fatalf("save extraction: %v", err)
		}
		if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
			Text:          "old summary",
			RawJSON:       `{"summary":"old summary"}`,
			Model:         "test/model",
			PromptVersion: "old-version",
			Status:        "ok",
			FetchedAt:     now.Add(-time.Hour),
			Tool:          "summarize",
			ToolVersion:   "0.10.0",
		}); err != nil {
			t.Fatalf("save summary: %v", err)
		}
		sourceIDs = append(sourceIDs, link.SourceID)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, sourceIDs, Options{
		Limit:                 10,
		Concurrency:           2,
		Summarize:             true,
		Model:                 "cli/test/sourceenrich",
		Length:                "short",
		Timeout:               5 * time.Second,
		ExactSummaryFreshness: true,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 2 {
		t.Fatalf("expected 2 summarized sources, got %d", stats.SourcesSummarized)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no errors, got %+v", stats)
	}
}

func TestRunSourceIDsUsesYouTubeTranscriptPathForGenericSources(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	installSourceEnrichYouTubeFakeSummarize(t, root)

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-youtube",
		SourceType:   "x_bookmark",
		ExternalID:   "test-youtube",
		CanonicalURL: "https://x.com/example/status/test-youtube",
		Title:        "test youtube",
		ContentHash:  "item-hash-youtube",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-youtube"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:youtube-test",
		OriginalURL:   "https://youtube.com/watch?v=test123",
		CanonicalURL:  "https://youtube.com/watch?v=test123",
		NormalizedURL: "https://youtube.com/watch?v=test123",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      vault.SourceNoteRelativePath("youtube", "test123"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Model:     "cli/test/sourceenrich-youtube",
		Length:    "short",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesExtracted != 1 {
		t.Fatalf("expected 1 extracted source, got %d", stats.SourcesExtracted)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %d", stats.SourcesSummarized)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.ExtractStatus != "ok" {
		t.Fatalf("expected extract status ok, got %q", source.ExtractStatus)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if !strings.Contains(source.ExtractedText, "real transcript content") {
		t.Fatalf("expected transcript-backed extract, got %q", source.ExtractedText)
	}
	if source.SummaryText != "summary from youtube path" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func installSourceEnrichFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
for arg in "$@"; do
  last="$arg"
done
if [ ! -f "$last" ]; then
  echo "expected local summary file input" >&2
  exit 1
fi
input="$(cat "$last")"
case "$input" in
  *"README CONTENT FROM GITHUB API"*) ;;
  *)
    echo "expected stored extract in summary file" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"README CONTENT FROM GITHUB API"},"summary":"summary from stored extract"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichGenericFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
prev=""
cli=""
for arg in "$@"; do
  if [ "$prev" = "--cli" ]; then
    cli="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ "$cli" != "claude" ]; then
  echo "expected preferred cli provider, got $cli" >&2
  exit 1
fi
if [ "$last" != "https://example.com/post" ]; then
  echo "unexpected summarize input: $last" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"https://example.com/post","title":"Example","description":"desc","siteName":"Example","content":"body"},"summary":"summary from generic path"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichXArticleExtractFakeSummarize(t *testing.T, root string, wantURL string, body string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
for arg in "$@"; do
  last="$arg"
done
if [ "$last" != "` + wantURL + `" ]; then
  echo "unexpected summarize input: $last" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"` + wantURL + `","title":"Recovered article","description":"","siteName":"x.com","content":"` + body + `"},"summary":null}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichSlowFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
sleep 10
printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"https://example.com/slow","title":"Slow","description":"","siteName":"Example","content":"body"},"summary":null}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type summarizecliTestRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Think *bool `json:"think"`
}

type sourceEnrichRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn sourceEnrichRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func installSourceEnrichDirectOllamaExtractFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
extract_mode=0
model=""
last=""
prev=""
for arg in "$@"; do
  if [ "$arg" = "--extract" ]; then
    extract_mode=1
  fi
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  last="$arg"
  prev="$arg"
done
if [ "$extract_mode" = "1" ]; then
  if [ "$model" != "" ]; then
    echo "did not expect --model during extract-only pass" >&2
    exit 1
  fi
  case "$last" in
    "https://example.com/direct-ollama"|"https://example.com/direct-openrouter") ;;
    *)
    echo "unexpected extract input: $last" >&2
    exit 1
    ;;
  esac
  printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"https://example.com/direct-ollama","title":"Direct Ollama","description":"desc","siteName":"Example","content":"extracted body for direct ollama"},"summary":null}'
  exit 0
fi
echo "unexpected summarize invocation for direct ollama summary" >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichRedirectFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
for arg in "$@"; do
  last="$arg"
done
if [ "$last" = "https://example.com/original" ]; then
  echo "Failed to fetch HTML document (status 307)" >&2
  exit 1
fi
if [ "$last" != "https://example.com/redirected" ]; then
  echo "unexpected summarize input: $last" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"https://example.com/redirected","title":"Redirected","description":"desc","siteName":"Example","content":"redirected body"},"summary":"summary from redirected path"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichYouTubeFakeSummarize(t *testing.T, root string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi

extract_mode=0
last=""
prev=""
want_youtube=0
want_auto=0
want_video_mode=0
want_transcript=0
want_transcriber=0
want_transcriber_auto=0
for arg in "$@"; do
  if [ "$arg" = "--extract" ]; then
    extract_mode=1
  fi
  if [ "$arg" = "--youtube" ]; then
    want_youtube=1
  elif [ "$prev" = "--youtube" ] && [ "$arg" = "auto" ]; then
    want_auto=1
  fi
  if [ "$arg" = "--video-mode" ]; then
    want_video_mode=1
  elif [ "$prev" = "--video-mode" ] && [ "$arg" = "transcript" ]; then
    want_transcript=1
  fi
  if [ "$arg" = "--transcriber" ]; then
    want_transcriber=1
  elif [ "$prev" = "--transcriber" ] && [ "$arg" = "auto" ]; then
    want_transcriber_auto=1
  fi
  last="$arg"
  prev="$arg"
done

if [ "$extract_mode" = "1" ]; then
  if [ "$SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER" != "chrome" ]; then
    echo "expected chrome youtube cookies env, got $SUMMARIZE_YT_DLP_COOKIES_FROM_BROWSER" >&2
    exit 1
  fi
  if [ "$want_youtube" != "1" ] || [ "$want_auto" != "1" ] || [ "$want_video_mode" != "1" ] || [ "$want_transcript" != "1" ] || [ "$want_transcriber" != "1" ] || [ "$want_transcriber_auto" != "1" ]; then
    echo "expected youtube transcript args" >&2
    exit 1
  fi
  if [ "$last" != "https://youtube.com/watch?v=test123" ]; then
    echo "unexpected youtube input: $last" >&2
    exit 1
  fi
  printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"https://youtube.com/watch?v=test123","title":"Video","description":"desc","siteName":"YouTube","content":"Transcript:\nreal transcript content"},"summary":null}'
  exit 0
fi

if [ ! -f "$last" ]; then
  echo "expected local summary file input" >&2
  exit 1
fi
input="$(cat "$last")"
case "$input" in
  *"real transcript content"*) ;;
  *)
    echo "expected transcript content in summary input" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/model"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"Transcript:\nreal transcript content"},"summary":"summary from youtube path"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSourceEnrichFallbackFakeYTDLP(t *testing.T, root string, wantCookies string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-yt-dlp-sourceenrich-fallback")
	script := `#!/bin/sh
out=""
cookies=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    out="$arg"
  fi
  if [ "$prev" = "--cookies-from-browser" ]; then
    cookies="$arg"
  fi
  prev="$arg"
done
if [ "$cookies" != "` + wantCookies + `" ]; then
  echo "unexpected cookies source: $cookies" >&2
  exit 1
fi
audio="${out%\.*}.mp3"
printf '%s\n' "fake audio" > "$audio"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp fallback: %v", err)
	}
	return scriptPath
}

func installSourceEnrichFallbackFakeMacWhisper(t *testing.T, root string, wantModel string, transcript string) string {
	t.Helper()

	scriptPath := filepath.Join(root, "fake-mw-sourceenrich")
	script := `#!/bin/sh
model=""
file=""
prev=""
first="$1"
for arg in "$@"; do
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  file="$arg"
  prev="$arg"
done
if [ "$first" != "transcribe" ]; then
  echo "expected transcribe subcommand" >&2
  exit 1
fi
if [ "` + wantModel + `" = "" ]; then
  if [ "$model" != "" ]; then
    echo "unexpected model: $model" >&2
    exit 1
  fi
else
  if [ "$model" != "` + wantModel + `" ]; then
    echo "unexpected model: $model" >&2
    exit 1
  fi
fi
if [ ! -f "$file" ]; then
  echo "expected audio file argument" >&2
  exit 1
fi
printf '%s\n' "` + transcript + `"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mw: %v", err)
	}
	return scriptPath
}
