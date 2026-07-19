package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/feedimport"
	installer "github.com/darron/dbrain/internal/install"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/prunedmediarepair"
	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/serviceauth"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/version"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
)

func TestRepairPrunedMediaCommandDryRunUsesReadOnlyStore(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}
	before := listRepairPrunedMediaDirectories(t, root)

	called := 0
	cmd := newRepairPrunedMediaCommandWithRun(&rootOptions{root: root, noCaffeinate: true, noDebug: true}, func(ctx context.Context, _ config.Config, st *store.Store, opts prunedmediarepair.Options) (prunedmediarepair.Stats, error) {
		called++
		if opts.Apply {
			t.Fatalf("expected dry-run options, got %+v", opts)
		}
		if err := st.SaveXMediaTranscriptionState(ctx, 1, model.XMediaTranscriptStatusError, "must fail", time.Now()); err == nil {
			t.Fatal("dry-run store unexpectedly allowed a write")
		}
		return prunedmediarepair.Stats{Apply: false}, nil
	})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected coordinator call, got %d", called)
	}
	var stats prunedmediarepair.Stats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal output %q: %v", stdout.String(), err)
	}
	if stats.Apply {
		t.Fatalf("expected apply=false, got %+v", stats)
	}
	after := listRepairPrunedMediaDirectories(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run mutated filesystem paths: before=%v after=%v", before, after)
	}
}

func TestRepairPrunedMediaCommandApplyDefaultsAndCategorySelection(t *testing.T) {
	root := t.TempDir()
	var got []prunedmediarepair.Options
	run := func(_ context.Context, _ config.Config, _ *store.Store, opts prunedmediarepair.Options) (prunedmediarepair.Stats, error) {
		got = append(got, opts)
		return prunedmediarepair.Stats{Apply: opts.Apply}, nil
	}

	for _, args := range [][]string{{"--apply", "--json"}, {"--apply", "--ocr", "--json"}, {"--apply", "--transcripts", "--json"}} {
		cmd := newRepairPrunedMediaCommandWithRun(&rootOptions{root: root, noCaffeinate: true, noDebug: true}, run)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext %v: %v", args, err)
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected three coordinator calls, got %d", len(got))
	}
	if !got[0].Apply || !got[0].OCR || !got[0].Transcripts || got[0].Limit != 5000 || got[0].Timeout != 45*time.Second {
		t.Fatalf("unexpected defaults: %+v", got[0])
	}
	if !got[1].OCR || got[1].Transcripts {
		t.Fatalf("--ocr selected wrong categories: %+v", got[1])
	}
	if got[2].OCR || !got[2].Transcripts {
		t.Fatalf("--transcripts selected wrong categories: %+v", got[2])
	}
}

func TestRepairPrunedMediaCommandValidatesBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"zero limit", []string{"--limit", "0"}, "limit must be positive"},
		{"over limit", []string{"--limit", "5001"}, "limit must not exceed 5000"},
		{"zero timeout", []string{"--timeout", "0s"}, "timeout must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRepairPrunedMediaCommandWithRun(&rootOptions{root: t.TempDir()}, func(context.Context, config.Config, *store.Store, prunedmediarepair.Options) (prunedmediarepair.Stats, error) {
				t.Fatal("coordinator called after invalid flags")
				return prunedmediarepair.Stats{}, nil
			})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func listRepairPrunedMediaDirectories(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return paths
}

func TestRootCommandHelpIncludesCoreCommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"auth", "import", "sync", "sqlite", "tsnet", "install", "config", "doctor", "eval", "entity", "topic", "worker", "link", "launchd", "extract", "hydrate", "transcribe", "ocr", "repair", "serve", "stats", "research", "search", "get", "categorize", "okf", "whats-new", "version"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
	}
	for _, value := range []string{"DBRAIN_ROOT", "dbrain config env"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
	}
}

func runRootCommand(t *testing.T, root string, args ...string) string {
	t.Helper()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	fullArgs := append([]string{"--root", root, "--no-caffeinate", "--no-debug"}, args...)
	cmd.SetArgs(fullArgs)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext %v: %v (stderr=%q)", fullArgs, err, stderr.String())
	}
	return stdout.String()
}

func runRootCommandErr(t *testing.T, root string, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	fullArgs := append([]string{"--root", root, "--no-caffeinate", "--no-debug"}, args...)
	cmd.SetArgs(fullArgs)
	err := cmd.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestWhatsNewCommandOutputsJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)
	seedWhatsNewCLIItem(t, root, "x:cli-json", "CLI JSON item", now)

	stdout := runRootCommand(t, root, "whats-new", "--since", now.Add(-time.Minute).Format(time.RFC3339), "--json")
	var page store.ReviewEventPage
	if err := json.Unmarshal([]byte(stdout), &page); err != nil {
		t.Fatalf("unmarshal whats-new JSON: %v output=%q", err, stdout)
	}
	if len(page.Events) != 1 || page.Events[0].EventKind != store.ReviewEventKindItemImported || page.Events[0].EntityKey != "x:cli-json" {
		t.Fatalf("unexpected whats-new JSON page: %+v", page)
	}
	if page.Events == nil || page.Counts == nil {
		t.Fatalf("expected non-nil JSON arrays, got %+v", page)
	}
}

func TestWhatsNewCommandOutputsEntityJSONView(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)
	seedWhatsNewCLIItem(t, root, "x:cli-entity", "CLI Entity item", now)

	stdout := runRootCommand(t, root, "whats-new", "--since", now.Add(-time.Minute).Format(time.RFC3339), "--view", "entities", "--json")
	var page store.ReviewEventPage
	if err := json.Unmarshal([]byte(stdout), &page); err != nil {
		t.Fatalf("unmarshal whats-new entity JSON: %v output=%q", err, stdout)
	}
	if len(page.Events) != 0 {
		t.Fatalf("entity JSON view should suppress raw events, got %+v", page.Events)
	}
	if len(page.Entities) != 1 || page.Entities[0].EntityKey != "x:cli-entity" {
		t.Fatalf("unexpected entity JSON page: %+v", page)
	}
}

func TestWhatsNewCommandOutputsHumanDigest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)
	seedWhatsNewCLIItem(t, root, "x:cli-human", "CLI Human item", now)

	stdout := runRootCommand(t, root, "whats-new", "--since", now.Add(-time.Minute).Format(time.RFC3339))
	for _, want := range []string{"What's new since", "Counts", "Review", "[item_imported]", "CLI Human item", "Next cursor:", "High watermark:", "Truncated: false"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected human output to contain %q, got %q", want, stdout)
		}
	}
}

func TestWhatsNewCommandOutputsHumanEntityDigest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)
	seedWhatsNewCLIItem(t, root, "x:cli-entity-human", "CLI Entity Human item", now)

	stdout := runRootCommand(t, root, "whats-new", "--since", now.Add(-time.Minute).Format(time.RFC3339), "--view", "entities")
	for _, want := range []string{"What's new since", "Counts", "Entities", "CLI Entity Human item", "events: item_imported", "Next cursor:", "High watermark:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected entity human output to contain %q, got %q", want, stdout)
		}
	}
}

func TestWhatsNewCommandRejectsCursorAndSinceTogether(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cursor, err := store.EncodeReviewCursor(store.ReviewCursor{EventAt: time.Date(2026, 6, 21, 15, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	_, _, err = runRootCommandErr(t, root, "whats-new", "--since", "24h", "--cursor", cursor)
	if err == nil || !strings.Contains(err.Error(), "exactly one of --since or --cursor is required") {
		t.Fatalf("expected since/cursor exclusivity error, got %v", err)
	}
}

func TestWhatsNewCommandRejectsUnknownTypeFilter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, _, err := runRootCommandErr(t, root, "whats-new", "--since", "24h", "--types", "imports,bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown review event type") {
		t.Fatalf("expected unknown type filter error, got %v", err)
	}
}

func TestTruncateWhatsNewTextPreservesUTF8(t *testing.T) {
	t.Parallel()

	got := truncateWhatsNewText("abc éfghij", 8)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated text to end with ellipsis marker, got %q", got)
	}
	if !strings.Contains(got, "é") {
		t.Fatalf("expected truncation to preserve complete UTF-8 runes, got %q", got)
	}
	if strings.ToValidUTF8(got, "\uFFFD") != got {
		t.Fatalf("expected valid UTF-8, got %q", got)
	}
}

func seedWhatsNewCLIItem(t *testing.T, root string, sourceKey string, title string, at time.Time) {
	t.Helper()

	cfg, err := loadConfig(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   sourceKey,
		CanonicalURL: "https://example.com/" + sourceKey,
		Title:        title,
		ContentHash:  sourceKey + "-hash",
		NotePath:     "items/cli/" + sourceKey + ".md",
		RawJSON:      `{}`,
		UpdatedAt:    at,
		LastSeenAt:   at,
	}); err != nil {
		t.Fatalf("seed whats-new item: %v", err)
	}
}

func TestAuthMCPTokenAddCreatesDBBackedBearerToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stdout := runRootCommand(t, root, "auth", "mcp", "token", "add", "phone", "--json")
	var result store.MCPBearerTokenCreateResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("unmarshal token result: %v output=%q", err, stdout)
	}
	if !strings.HasPrefix(result.Token, "dbrain_mcp_") || result.Record.Name != "phone" {
		t.Fatalf("unexpected token result: %#v", result)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if ok, err := st.ValidateMCPBearerToken(context.Background(), result.Token); err != nil {
		t.Fatalf("ValidateMCPBearerToken: %v", err)
	} else if !ok {
		t.Fatalf("expected CLI-created token to validate")
	}
}

func TestAuthGitHubListAndRemoveApprovedUsers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runRootCommand(t, root, "auth", "github", "approve", "Darron")

	listOut := runRootCommand(t, root, "auth", "github", "list", "--json")
	var users []store.AuthUser
	if err := json.Unmarshal([]byte(listOut), &users); err != nil {
		t.Fatalf("unmarshal users: %v output=%q", err, listOut)
	}
	if len(users) != 1 || users[0].GitHubUsernameNormalized != "darron" {
		t.Fatalf("unexpected listed users: %#v", users)
	}

	removeOut := runRootCommand(t, root, "auth", "github", "remove", "@DARRON", "--json")
	var removed struct {
		Removed bool           `json:"removed"`
		User    store.AuthUser `json:"user"`
	}
	if err := json.Unmarshal([]byte(removeOut), &removed); err != nil {
		t.Fatalf("unmarshal removal: %v output=%q", err, removeOut)
	}
	if !removed.Removed || removed.User.GitHubUsernameNormalized != "darron" {
		t.Fatalf("unexpected removal result: %#v", removed)
	}

	listOut = runRootCommand(t, root, "auth", "github", "list", "--json")
	users = nil
	if err := json.Unmarshal([]byte(listOut), &users); err != nil {
		t.Fatalf("unmarshal users after removal: %v output=%q", err, listOut)
	}
	if len(users) != 0 {
		t.Fatalf("expected no approved users after removal, got %#v", users)
	}
}

func TestAuthMCPTokenListAndRevokeDoNotExposeSecret(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	createOut := runRootCommand(t, root, "auth", "mcp", "token", "add", "phone", "--json")
	var created store.MCPBearerTokenCreateResult
	if err := json.Unmarshal([]byte(createOut), &created); err != nil {
		t.Fatalf("unmarshal created token: %v output=%q", err, createOut)
	}
	if created.Token == "" || created.Record.TokenFingerprint == "" {
		t.Fatalf("unexpected created token result: %#v", created)
	}

	listOut := runRootCommand(t, root, "auth", "mcp", "token", "list", "--json")
	if strings.Contains(listOut, created.Token) {
		t.Fatalf("token list exposed raw token: %q", listOut)
	}
	var records []store.MCPBearerToken
	if err := json.Unmarshal([]byte(listOut), &records); err != nil {
		t.Fatalf("unmarshal token list: %v output=%q", err, listOut)
	}
	if len(records) != 1 || records[0].TokenFingerprint != created.Record.TokenFingerprint {
		t.Fatalf("unexpected token list: %#v", records)
	}
	humanListOut := runRootCommand(t, root, "auth", "mcp", "token", "list")
	if strings.Contains(humanListOut, created.Token) {
		t.Fatalf("human token list exposed raw token: %q", humanListOut)
	}

	revokeOut := runRootCommand(t, root, "auth", "mcp", "token", "revoke", created.Record.TokenFingerprint, "--json")
	if strings.Contains(revokeOut, created.Token) {
		t.Fatalf("token revoke exposed raw token: %q", revokeOut)
	}
	var revoked struct {
		Revoked bool                 `json:"revoked"`
		Token   store.MCPBearerToken `json:"token"`
	}
	if err := json.Unmarshal([]byte(revokeOut), &revoked); err != nil {
		t.Fatalf("unmarshal token revoke: %v output=%q", err, revokeOut)
	}
	if !revoked.Revoked || revoked.Token.RevokedAt == "" {
		t.Fatalf("unexpected revoke result: %#v", revoked)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if ok, err := st.ValidateMCPBearerToken(context.Background(), created.Token); err != nil {
		t.Fatalf("ValidateMCPBearerToken after revoke: %v", err)
	} else if ok {
		t.Fatalf("expected revoked token to be rejected")
	}
}

func TestFeedAddCheckFlagDefaultsToVerifyOnly(t *testing.T) {
	t.Parallel()

	cmd := newFeedAddCommand(&rootOptions{})
	flag := cmd.Flags().Lookup("check")
	if flag == nil {
		t.Fatal("missing --check flag")
	}
	if flag.DefValue != "false" {
		t.Fatalf("--check default = %q, want false", flag.DefValue)
	}
}

func TestFeedRefreshCommandDefinesForceSummarizeAndSourceFlags(t *testing.T) {
	t.Parallel()

	cmd := newFeedRefreshCommand(&rootOptions{})
	for _, name := range []string{"force", "summarize", "model", "cli", "length", "timeout", "concurrency", "allow-private-network", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag", name)
		}
	}
	if got := cmd.Flags().Lookup("summarize").DefValue; got != "true" {
		t.Fatalf("--summarize default = %q, want true", got)
	}
	if got := cmd.Flags().Lookup("concurrency").DefValue; got != "4" {
		t.Fatalf("--concurrency default = %q, want 4", got)
	}
}

func TestFeedAllowPrivateNetworkRuntimeAcceptsSingularAndPluralEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORK", "")
	t.Setenv("DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORKS", "true")
	if !feedAllowPrivateNetworkFromRuntime(root) {
		t.Fatal("expected plural feed private-network env alias to be accepted")
	}

	t.Setenv("DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORK", "true")
	t.Setenv("DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORKS", "")
	if !feedAllowPrivateNetworkFromRuntime(root) {
		t.Fatal("expected singular feed private-network env to be accepted")
	}
}

func TestFeedStatusFormattingShowsScheduleState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 9, 18, 0, 0, 0, time.UTC)
	feed := store.Feed{
		Enabled:        true,
		HealthStatus:   store.FeedHealthOK,
		LastCheckedAt:  now.Add(-time.Hour),
		NextFetchAfter: now.Add(time.Minute),
	}
	if got := feedDueStatus(feed, now); got != "not_due" {
		t.Fatalf("feedDueStatus = %q, want not_due", got)
	}
	feed.NextFetchAfter = now.Add(-time.Minute)
	if got := feedDueStatus(feed, now); got != "due" {
		t.Fatalf("feedDueStatus = %q, want due", got)
	}
	feed.Enabled = false
	if got := feedDueStatus(feed, now); got != "disabled" {
		t.Fatalf("feedDueStatus = %q, want disabled", got)
	}
	if got := formatFeedCLITime(time.Time{}); got != "-" {
		t.Fatalf("formatFeedCLITime zero = %q, want -", got)
	}
}

func TestFeedOutputRedactsBasicAuthPassword(t *testing.T) {
	t.Parallel()

	raw := "https://feed:secret@example.com/feed.atom"
	if got := redactURLUserInfo(raw); got != "https://feed:REDACTED@example.com/feed.atom" {
		t.Fatalf("redactURLUserInfo = %q", got)
	}
	feed := redactFeedForOutput(store.Feed{
		URL:           raw,
		NormalizedURL: raw,
		ResolvedURL:   raw,
		LastError:     "fetch " + raw + ": unauthorized",
	})
	for _, got := range []string{feed.URL, feed.NormalizedURL, feed.ResolvedURL, feed.LastError} {
		if strings.Contains(got, "secret") {
			t.Fatalf("expected redacted feed output, got %q", got)
		}
	}
	stats := redactFeedStatsForOutput(feedimport.Stats{Results: []feedimport.Result{{
		URL:   raw,
		Error: "fetch " + raw,
	}}})
	if strings.Contains(stats.Results[0].URL, "secret") || strings.Contains(stats.Results[0].Error, "secret") {
		t.Fatalf("expected redacted stats output, got %+v", stats.Results[0])
	}
}

func TestCategorizeVocabSuggestionMergePreservesCategoriesLayout(t *testing.T) {
	t.Parallel()

	input := []byte(`# categories.yaml
aliases:
  go: golang

drop:
  - bookmark
`)
	suggestion := categorizeVocabSuggestion{
		Aliases: map[string]string{
			"go-lang": "golang",
			"go":      "golang",
		},
		Drop: []string{"external-link", "bookmark"},
	}
	got, stats, err := mergeCategorizeVocabSuggestion(input, suggestion, time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("mergeCategorizeVocabSuggestion: %v", err)
	}
	if stats.AliasesAdded != 1 || stats.DropAdded != 1 {
		t.Fatalf("stats = %+v, want aliases=1 drop=1", stats)
	}
	output := string(got)
	for _, expected := range []string{
		"  # === LLM-suggested cleanup (2026-05-10) ===",
		"  go-lang: golang",
		"drop:\n  - bookmark",
		"  # LLM-suggested cleanup (2026-05-10)",
		"  - external-link",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected merged output to contain %q, got:\n%s", expected, output)
		}
	}
	if strings.Count(output, "  go: golang") != 1 {
		t.Fatalf("existing alias should not be re-added, got:\n%s", output)
	}
}

func TestCategorizeVocabSuggestionMergeKeepsApprovedDeterministicAliases(t *testing.T) {
	t.Parallel()

	input := []byte("aliases:\n  go: golang\n\ndrop:\n  - bookmark\n")
	suggestion := categorizeVocabSuggestion{Aliases: map[string]string{
		"llm": "large-language-models",
	}}
	got, stats, err := mergeCategorizeVocabSuggestion(input, suggestion, time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("mergeCategorizeVocabSuggestion: %v", err)
	}
	if stats.AliasesAdded != 1 {
		t.Fatalf("stats = %+v, want aliases=1", stats)
	}
	if !strings.Contains(string(got), "  llm: large-language-models") {
		t.Fatalf("expected approved deterministic alias to be merged, got:\n%s", string(got))
	}
}

func TestParseCategorizeVocabSuggestionAcceptsJSONFence(t *testing.T) {
	t.Parallel()

	got, err := parseCategorizeVocabSuggestion("```json\n{\"aliases\":{\"go-lang\":\"golang\"},\"drop\":[\"bookmark\"],\"notes\":[\"ok\"]}\n```")
	if err != nil {
		t.Fatalf("parseCategorizeVocabSuggestion: %v", err)
	}
	if got.Aliases["go-lang"] != "golang" {
		t.Fatalf("aliases = %#v", got.Aliases)
	}
	if !reflect.DeepEqual(got.Drop, []string{"bookmark"}) {
		t.Fatalf("drop = %#v", got.Drop)
	}
}

func TestFilterCategorizeVocabSuggestionRejectsBroadTopicCollapses(t *testing.T) {
	t.Parallel()

	got := filterCategorizeVocabSuggestion(categorizeVocabSuggestion{
		Aliases: map[string]string{
			"software-development": "software-engineering",
			"libraries":            "library",
		},
		Drop: []string{"python", "external-link", "open-source-projects"},
	}, categoryvocab.Vocab{}, []categorizeVocabToken{
		{Token: "software-development", Count: 100},
		{Token: "software-engineering", Count: 80},
		{Token: "libraries", Count: 20},
		{Token: "library", Count: 30},
		{Token: "python", Count: 100},
		{Token: "external-link", Count: 10},
		{Token: "open-source-projects", Count: 50},
	})

	if _, ok := got.Aliases["software-development"]; ok {
		t.Fatalf("expected broad semantic alias to be rejected, got %#v", got.Aliases)
	}
	if got.Aliases["libraries"] != "library" {
		t.Fatalf("expected plural alias to survive, got %#v", got.Aliases)
	}
	if !reflect.DeepEqual(got.Drop, []string{"external-link"}) {
		t.Fatalf("drop = %#v, want only external-link", got.Drop)
	}
}

func TestDeterministicCategorizeVocabSuggestsKnownCorpusCleanup(t *testing.T) {
	t.Parallel()

	got := suggestDeterministicCategorizeVocab(map[string]int{
		"large-language-models":        100,
		"llm":                          10,
		"kubernetes":                   100,
		"k8s":                          5,
		"site-reliability-engineering": 20,
		"sre":                          6,
		"twitter":                      20,
		"x-twitter":                    6,
		"social-media-post":            12,
		"github-repo":                  7,
	}, categoryvocab.Vocab{}, 2)

	wantAliases := map[string]string{
		"llm":       "large-language-models",
		"k8s":       "kubernetes",
		"sre":       "site-reliability-engineering",
		"x-twitter": "twitter",
	}
	if !reflect.DeepEqual(got.Aliases, wantAliases) {
		t.Fatalf("aliases = %#v, want %#v", got.Aliases, wantAliases)
	}
	if !reflect.DeepEqual(got.Drop, []string{"github-repo", "social-media-post"}) {
		t.Fatalf("drop = %#v", got.Drop)
	}
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	current := version.Current()
	for _, expected := range []string{
		"commit: " + current.Commit,
		"short: " + current.Short,
		"build_time: " + current.BuildTime,
		"git_status: " + current.GitStatus,
		"go_version: " + current.GoVersion,
		"release_version: " + current.ReleaseVersion,
		"build_platform: " + current.BuildPlatform,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout.String())
		}
	}
	if current.ModulePath != "" && !strings.Contains(stdout.String(), "module_path: "+current.ModulePath) {
		t.Fatalf("expected module_path in output, got %q", stdout.String())
	}
	if current.ModuleVersion != "" && !strings.Contains(stdout.String(), "module_version: "+current.ModuleVersion) {
		t.Fatalf("expected module_version in output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestLaunchdPlistCommandUsesDefaultLayoutWithoutRootArg(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, ".config")
	dataHome := filepath.Join(home, ".local", "share")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "launchd", "plist", "--bin", "/opt/homebrew/bin/dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"<string>/opt/homebrew/bin/dbrain</string>",
		"<string>serve</string>",
		"<string>remote</string>",
		"<key>EnvironmentVariables</key>",
		"<key>PATH</key>",
		"<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>",
		filepath.Join(dataHome, "dbrain", "logs", "launchd.out.log"),
		filepath.Join(dataHome, "dbrain", "logs", "launchd.err.log"),
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected plist to contain %q, got %q", expected, output)
		}
	}
	if strings.Contains(output, "<string>--root</string>") {
		t.Fatalf("default launchd plist should not include --root, got %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestLaunchdPlistCommandIncludesExplicitRootArg(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "launchd", "plist", "--label", "com.darron.dbrain-dev", "--bin", "/tmp/dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"<string>com.darron.dbrain-dev</string>",
		"<string>/tmp/dbrain</string>",
		"<string>--root</string>",
		"<string>" + root + "</string>",
		"<string>serve</string>",
		"<string>remote</string>",
		filepath.Join(root, "logs", "launchd.out.log"),
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected plist to contain %q, got %q", expected, output)
		}
	}
}

func TestLaunchdInstallNoStartWritesPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--no-debug", "launchd", "install", "--no-start", "--bin", "/opt/homebrew/bin/dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", defaultLaunchdLabel+".plist")
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	if !strings.Contains(string(data), "<string>/opt/homebrew/bin/dbrain</string>") {
		t.Fatalf("unexpected plist: %s", data)
	}
	if !strings.Contains(string(data), "<key>PATH</key>") || !strings.Contains(string(data), "/opt/homebrew/bin:/usr/local/bin") {
		t.Fatalf("expected launchd plist to include Homebrew PATH, got: %s", data)
	}
	if !strings.Contains(stdout.String(), "Not loaded because --no-start was set.") {
		t.Fatalf("expected no-start message, got %q", stdout.String())
	}
}

func TestLaunchdRestartKickstartsLoadedService(t *testing.T) {
	originalRunLaunchctl := runLaunchctl
	defer func() {
		runLaunchctl = originalRunLaunchctl
	}()

	var calls [][]string
	runLaunchctl = func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--no-debug", "launchd", "restart", "--label", "com.darron.dbrain-dev", "--check-full-disk-access=false"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected one launchctl call, got %#v", calls)
	}
	expected := []string{"kickstart", "-k", launchdDomain() + "/com.darron.dbrain-dev"}
	if !reflect.DeepEqual(calls[0], expected) {
		t.Fatalf("unexpected launchctl args: got %#v want %#v", calls[0], expected)
	}
	if !strings.Contains(stdout.String(), "Restarted launchd service: "+launchdDomain()+"/com.darron.dbrain-dev") {
		t.Fatalf("expected restart message, got %q", stdout.String())
	}
}

func TestLaunchdRestartOpensFullDiskAccessWhenProbeFails(t *testing.T) {
	originalRunLaunchctl := runLaunchctl
	originalProbe := runFullDiskAccessProbeBinary
	originalOpen := openFullDiskAccessSettings
	originalWaitService := waitForLaunchdServiceFullDiskAccessFunc
	defer func() {
		runLaunchctl = originalRunLaunchctl
		runFullDiskAccessProbeBinary = originalProbe
		openFullDiskAccessSettings = originalOpen
		waitForLaunchdServiceFullDiskAccessFunc = originalWaitService
	}()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	install := NewRootCommand()
	install.SetOut(io.Discard)
	install.SetErr(io.Discard)
	install.SetArgs([]string{"--no-debug", "launchd", "install", "--no-start", "--bin", "/opt/homebrew/bin/dbrain"})
	if err := install.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install launchd plist: %v", err)
	}

	var launchctlCalls [][]string
	runLaunchctl = func(_ context.Context, args ...string) error {
		launchctlCalls = append(launchctlCalls, append([]string(nil), args...))
		return nil
	}
	var probedBin string
	runFullDiskAccessProbeBinary = func(_ context.Context, binPath string, _ string) ([]byte, error) {
		probedBin = binPath
		return []byte("probe denied\n"), fmt.Errorf("exit status 1")
	}
	waitForLaunchdServiceFullDiskAccessFunc = func(context.Context, *rootOptions, string) (serviceFullDiskAccessResponse, error) {
		return serviceFullDiskAccessResponse{
			OK:         false,
			Readable:   false,
			Path:       "/Users/example/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite",
			Executable: "/opt/homebrew/bin/dbrain",
			PID:        1234,
			Error:      "operation not permitted",
		}, nil
	}
	var opened bool
	openFullDiskAccessSettings = func(context.Context) error {
		opened = true
		return nil
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--no-debug", "launchd", "restart"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	if len(launchctlCalls) != 1 {
		t.Fatalf("expected one launchctl call, got %#v", launchctlCalls)
	}
	if probedBin != "" {
		t.Fatalf("foreground binary should not run when service check responds, probed %q", probedBin)
	}
	if !opened {
		t.Fatal("expected Full Disk Access settings to open")
	}
	output := stdout.String()
	for _, expected := range []string{
		"Restarted launchd service:",
		"Full Disk Access check",
		"Target binary: /opt/homebrew/bin/dbrain",
		"Service process: pid=1234 executable=/opt/homebrew/bin/dbrain",
		"Full Disk Access check: failed (operation not permitted)",
		"Opening Full Disk Access settings",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
}

func TestFetchServiceFullDiskAccessUsesSignedServiceAuth(t *testing.T) {
	t.Setenv("DBRAIN_AUTH_ENABLED", "")
	t.Setenv("DBRAIN_AUTH_SESSION_KEY", "")

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	const sessionKey = "test-session-key-32-characters-long"
	if err := os.WriteFile(cfg.ConfigPath, []byte("auth:\n  enabled: true\n  session_key: "+sessionKey+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	authHeader, err := serviceAuthHeader(context.Background(), cfg, http.MethodGet, serviceFullDiskAccessPath)
	if err != nil {
		t.Fatalf("serviceAuthHeader: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !serviceauth.VerifyHeader(r.Method, r.URL.Path, sessionKey, r.Header.Get(serviceauth.HeaderName), time.Now()) {
			http.Error(w, "missing or invalid service auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(serviceFullDiskAccessResponse{
			OK:         true,
			Readable:   true,
			Path:       "/tmp/NoteStore.sqlite",
			Executable: "/opt/homebrew/bin/dbrain",
			PID:        1234,
		})
	}))
	defer server.Close()

	response, err := fetchServiceFullDiskAccess(context.Background(), server.URL+serviceFullDiskAccessPath, "", authHeader)
	if err != nil {
		t.Fatalf("fetchServiceFullDiskAccess: %v", err)
	}
	if !response.OK || !response.Readable || response.PID != 1234 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestDoctorFullDiskAccessReportsLaunchdTargetWithoutSideEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	install := NewRootCommand()
	install.SetOut(io.Discard)
	install.SetErr(io.Discard)
	install.SetArgs([]string{"--no-debug", "launchd", "install", "--no-start", "--bin", "/opt/homebrew/bin/dbrain"})
	if err := install.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install launchd plist: %v", err)
	}

	doctor := NewRootCommand()
	var stdout bytes.Buffer
	doctor.SetOut(&stdout)
	doctor.SetErr(io.Discard)
	doctor.SetArgs([]string{"--no-debug", "doctor", "full-disk-access", "--open=false", "--probe=false"})
	if err := doctor.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("doctor full-disk-access: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"Full Disk Access",
		"Target binary: /opt/homebrew/bin/dbrain",
		"Target source: launchd plist",
		"Apple Notes probe:",
		"It cannot grant Full Disk Access automatically",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected doctor output to contain %q, got %q", expected, output)
		}
	}
}

func TestReadLaunchdProgramArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "com.darron.dbrain.plist")
	data, err := renderLaunchdPlist(launchdPlistOptions{
		Label:   "com.darron.dbrain",
		BinPath: "/opt/homebrew/bin/dbrain",
		Args:    []string{"--config-file", "/Users/example/.config/dbrain/config.yaml"},
		OutPath: "/tmp/dbrain.out",
		ErrPath: "/tmp/dbrain.err",
	})
	if err != nil {
		t.Fatalf("renderLaunchdPlist: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := readLaunchdProgramArguments(path)
	if err != nil {
		t.Fatalf("readLaunchdProgramArguments: %v", err)
	}
	want := []string{"/opt/homebrew/bin/dbrain", "--config-file", "/Users/example/.config/dbrain/config.yaml", "serve", "remote"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("program arguments = %#v, want %#v", got, want)
	}
}

func TestLaunchdPlistCommandPreservesConfigFileSelector(t *testing.T) {
	home := t.TempDir()
	devRoot := t.TempDir()
	configDir := filepath.Join(home, ".config", "dbrain")
	dataHome := filepath.Join(home, ".local", "share")
	configPath := filepath.Join(configDir, "stable.yaml")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("tsnet:\n  hostname: dbrain\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("DBRAIN_ROOT", devRoot)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--config-file", configPath, "--no-debug", "launchd", "plist", "--bin", "/opt/homebrew/bin/dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"<string>--config-file</string>",
		"<string>" + configPath + "</string>",
		filepath.Join(dataHome, "dbrain", "logs", "launchd.out.log"),
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected plist to contain %q, got %q", expected, output)
		}
	}
	if strings.Contains(output, "<string>--root</string>") || strings.Contains(output, devRoot) {
		t.Fatalf("config-file launchd plist should not include dev root, got %q", output)
	}
}

func TestInstallCommandWritesBasePathConfigAndReportsDetectedTools(t *testing.T) {
	home := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "brain")
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{"mw", "ffprobe", "yt-dlp", "tesseract", "summarize"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}

	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--no-debug",
		"install",
		"--yes",
		"--base-path", basePath,
		"--force",
		"--no-keychain",
		"--enable-x-bookmarks",
		"--enable-github-stars",
		"--enable-youtube-watch-later",
		"--enable-youtube-liked",
		"--enable-feeds",
		"--enable-apple-notes",
		"--enable-safari-tabs",
		"--safari-tabs-device", "test-phone",
		"--enable-scheduler",
		"--enable-tailscale",
		"--tsnet-hostname", "dbrain-test",
		"--enable-github-login",
		"--auth-base-url", "https://dbrain-test.example.ts.net",
		"--github-client-id", "Iv1.test",
		"--local-model-profile", "none",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install command: %v", err)
	}

	configPath := filepath.Join(basePath, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	configText := string(raw)
	for _, expected := range []string{
		"sync_all:",
		"x_bookmarks: true",
		"github_stars: true",
		"youtube_watch_later: true",
		"youtube_liked: true",
		"feeds: true",
		"apple_notes:",
		"enabled: true",
		"safari_tabs:",
		`device: "test-phone"`,
		"scheduler:",
		"tsnet:",
		`hostname: "dbrain-test"`,
		"auth:",
		`base_url: "https://dbrain-test.example.ts.net"`,
		`client_id: "Iv1.test"`,
		`tesseract_binary: "` + filepath.Join(binDir, "tesseract") + `"`,
	} {
		if !strings.Contains(configText, expected) {
			t.Fatalf("expected config to contain %q:\n%s", expected, configText)
		}
	}
	sessionKeyLine := ""
	for _, line := range strings.Split(configText, "\n") {
		if strings.Contains(line, "session_key:") {
			sessionKeyLine = line
			break
		}
	}
	if sessionKeyLine == "" || strings.Contains(sessionKeyLine, `session_key: ""`) || strings.Contains(sessionKeyLine, "keychain://") {
		t.Fatalf("expected non-empty plaintext session_key for --no-keychain GitHub login, got %q in:\n%s", sessionKeyLine, configText)
	}
	for _, unexpected := range []string{`model: "ollama/`, `model: "lmstudio/`, `model: "omlx/`} {
		if strings.Contains(configText, unexpected) {
			t.Fatalf("unattended install should not select detected LLM model %q:\n%s", unexpected, configText)
		}
	}

	output := stdout.String()
	for _, expected := range []string{
		"dbrain install complete",
		configPath,
		"MacWhisper CLI",
		"Tesseract",
		"summarize",
		"Scheduler enabled",
		"GitHub login enabled",
		"Tailscale enabled",
		"auth.session_key was written directly",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected install output to contain %q, got %q", expected, output)
		}
	}
}

func TestInstallCommandWritesExplicitDisabledImportPolicy(t *testing.T) {
	clearSyncEnvForTest(t)
	home := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "brain")
	t.Setenv("HOME", home)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--no-debug",
		"install",
		"--yes",
		"--base-path", basePath,
		"--force",
		"--no-detect",
		"--no-keychain",
		"--local-model-profile", "none",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install command: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(basePath, "config.yaml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	configText := string(raw)
	for _, expected := range []string{
		"x_bookmarks: false",
		"github_stars: false",
		"youtube_watch_later: false",
		"youtube_liked: false",
		"feeds: false",
		"apple_notes: false",
		"safari_tabs: false",
		`browser: "chrome"`,
	} {
		if !strings.Contains(configText, expected) {
			t.Fatalf("expected generated config to contain %q:\n%s", expected, configText)
		}
	}

	resolved, err := resolveSyncAllFlags(basePath, syncAllFlags{watchLater: true, liked: true})
	if err != nil {
		t.Fatalf("resolve generated import policy: %v", err)
	}
	if !resolved.skipXBookmarks || !resolved.skipX || !resolved.skipXMedia || !resolved.skipXPhotoOCR || !resolved.skipGitHub || !resolved.skipYouTube || !resolved.skipFeeds {
		t.Fatalf("fresh disabled policy did not suppress import stages: %+v", resolved)
	}
	if resolved.appleNotes || resolved.safariTabs || resolved.watchLater || resolved.liked {
		t.Fatalf("fresh disabled policy unexpectedly enabled an importer: %+v", resolved)
	}
	if output := stdout.String(); !strings.Contains(output, "No import sources were selected") || !strings.Contains(output, "X bookmarks") || !strings.Contains(output, "disabled") {
		t.Fatalf("install summary did not explain disabled imports: %q", output)
	}
}

func TestInstallCommandReinstallPreservesImportSelectionsAndBrowser(t *testing.T) {
	clearSyncEnvForTest(t)
	home := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "brain")
	t.Setenv("HOME", home)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	runInstall := func(args ...string) {
		t.Helper()
		cmd := NewRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{
			"--no-debug",
			"install",
			"--yes",
			"--base-path", basePath,
			"--no-detect",
			"--no-keychain",
			"--local-model-profile", "none",
		}, args...))
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("install command: %v", err)
		}
	}

	runInstall(
		"--force",
		"--enable-x-bookmarks",
		"--enable-youtube-watch-later",
		"--enable-feeds",
		"--enable-apple-notes",
		"--enable-safari-tabs",
		"--safari-tabs-device", "phone",
		"--enable-scheduler",
		"--browser", "firefox",
		"--profile", "research",
	)
	runInstall(
		"--enable-github-stars",
	)

	raw, err := os.ReadFile(filepath.Join(basePath, "config.yaml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	configText := string(raw)
	for _, expected := range []string{
		"x_bookmarks: true",
		"github_stars: true",
		"youtube_watch_later: true",
		"youtube_liked: false",
		"feeds: true",
		"apple_notes: true",
		"safari_tabs: true",
		`device: "phone"`,
		`browser: "firefox"`,
		`profile: "research"`,
	} {
		if !strings.Contains(configText, expected) {
			t.Fatalf("expected reinstall to preserve/write %q:\n%s", expected, configText)
		}
	}
	schedulerConfig, err := schedulerSyncConfigFromRuntime(basePath)
	if err != nil {
		t.Fatalf("read scheduler config: %v", err)
	}
	if !schedulerConfig.Enabled {
		t.Fatal("expected reinstall to preserve scheduler.sync_all.enabled")
	}
}

func TestNormalizeInstallPromptErrorTreatsAbortAsCleanExit(t *testing.T) {
	if err := normalizeInstallPromptError(huh.ErrUserAborted); err != nil {
		t.Fatalf("user abort should be a clean exit, got %v", err)
	}
	other := errors.New("other prompt failure")
	if err := normalizeInstallPromptError(other); !errors.Is(err, other) {
		t.Fatalf("unexpected prompt error normalization: %v", err)
	}
}

func TestDefaultInstallSelectionsUsesDetectedLocalModelDefaults(t *testing.T) {
	tools := []installer.Tool{
		{ID: installer.ToolOllama, Name: "Ollama CLI", Available: true, Path: "/opt/homebrew/bin/ollama"},
		{ID: installer.ToolOllamaAPI, Name: "Ollama API", Available: true, Models: []string{"qwen-test:latest"}},
		{ID: installer.ToolLMStudioAPI, Name: "LM Studio API", Available: true, Models: []string{"qwen/lmstudio-test"}},
		{ID: installer.ToolTesseract, Name: "Tesseract", Available: true, Path: "/opt/homebrew/bin/tesseract"},
	}

	selections := defaultInstallSelections(installFlags{}, tools)
	if selections.SummaryModel != installDbrainOllamaModel || selections.CategorizeModel != selections.SummaryModel {
		t.Fatalf("detected Ollama CLI should select dbrain model by default: %#v", selections)
	}
	if selections.OCRModel != "tesseract" {
		t.Fatalf("detected tesseract should set OCR model, got %#v", selections)
	}
	if selections.SkipXPhotoOCR || selections.SkipCategorize {
		t.Fatalf("local defaults should keep OCR/categorization enabled, got %#v", selections)
	}
}

func TestWhisperModelDownloadFreshInstallDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	selections := installer.Selections{
		WhisperModelPath:    filepath.Join(dir, "ggml-base.bin"),
		WhisperVADModelPath: filepath.Join(dir, "ggml-silero-v6.2.0.bin"),
	}
	tools := []installer.Tool{{ID: installer.ToolWhisperCPP, Available: true}}

	if !shouldOfferWhisperModelDownload(tools, selections) {
		t.Fatal("fresh install with whisper-cli should offer missing models")
	}
	if !defaultWhisperModelDownload(false, true, true) {
		t.Fatal("--yes should accept the missing whisper model default")
	}
	if defaultWhisperModelDownload(false, false, true) {
		t.Fatal("interactive install should wait for the user's confirmation")
	}

	for _, path := range []string{selections.WhisperModelPath, selections.WhisperVADModelPath} {
		if err := os.WriteFile(path, []byte("cached"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if shouldOfferWhisperModelDownload(tools, selections) {
		t.Fatal("existing regular model files should suppress the offer")
	}
	if shouldOfferWhisperModelDownload(nil, installer.Selections{}) {
		t.Fatal("missing whisper-cli should suppress automatic model setup")
	}
}

func TestInstallWhisperDownloadUsesCLIProgressOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	ui := newCLIProgressUI(&output)
	path := "/cache/whisper-cpp/ggml-base.bin"
	handleInstallDownloadProgress(ui, installer.DownloadProgress{Kind: installer.DownloadProgressStart, Path: path, Total: 1024})
	handleInstallDownloadProgress(ui, installer.DownloadProgress{Kind: installer.DownloadProgressUpdate, Path: path, Current: 1024, Total: 1024})
	handleInstallDownloadProgress(ui, installer.DownloadProgress{Kind: installer.DownloadProgressDone, Path: path, Current: 1024, Total: 1024})

	for _, want := range []string{"Downloading whisper.cpp model ggml-base.bin", "1.0 KiB/1.0 KiB", "Downloaded whisper.cpp model ggml-base.bin"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in progress output:\n%s", want, output.String())
		}
	}
}

func TestDefaultInstallSelectionsSkipsHostedStagesWithoutModelsOrOpenRouter(t *testing.T) {
	t.Setenv("DBRAIN_OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	selections := defaultInstallSelections(installFlags{}, nil)
	if !selections.SkipXPhotoOCR || !selections.SkipCategorize {
		t.Fatalf("expected hosted-only stages to be skipped without local models or OpenRouter key, got %#v", selections)
	}
}

func TestDefaultInstallSelectionsUsesLocalModelProfileWhenRequested(t *testing.T) {
	t.Parallel()

	selections := defaultInstallSelections(installFlags{localModelProfile: installModelProfileSmallOllama}, nil)
	if selections.SummaryModel != installSmallOllamaModel || selections.CategorizeModel != installSmallOllamaModel {
		t.Fatalf("small profile selections = %#v", selections)
	}

	selections = defaultInstallSelections(installFlags{localModelProfile: installModelProfileDbrain}, nil)
	if selections.SummaryModel != installDbrainOllamaModel || selections.CategorizeModel != installDbrainOllamaModel {
		t.Fatalf("dbrain profile selections = %#v", selections)
	}

	selections = defaultInstallSelections(installFlags{localModelProfile: installModelProfileDbrainOllama}, nil)
	if selections.SummaryModel != installDbrainOllamaModel || selections.CategorizeModel != installDbrainOllamaModel {
		t.Fatalf("dbrain Ollama profile selections = %#v", selections)
	}

	selections = defaultInstallSelections(installFlags{localModelProfile: installModelProfileDbrainOMLX}, nil)
	if selections.SummaryModel != installDbrainOMLXModel || selections.CategorizeModel != installDbrainOMLXModel {
		t.Fatalf("dbrain oMLX profile selections = %#v", selections)
	}

	selections = defaultInstallSelections(installFlags{
		localModelProfile: installModelProfileDbrain,
		summaryModel:      "ollama/custom:latest",
	}, nil)
	if selections.SummaryModel != "ollama/custom:latest" || selections.CategorizeModel != "ollama/custom:latest" {
		t.Fatalf("explicit summary model should win over profile, got %#v", selections)
	}

	selections = defaultInstallSelections(installFlags{localModelProfile: installModelProfileNone}, []installer.Tool{{
		ID:        installer.ToolOllamaAPI,
		Name:      "Ollama API",
		Available: true,
		Models:    []string{"qwen-test:latest"},
	}})
	if selections.SummaryModel != "" || selections.CategorizeModel != "" || !selections.SkipCategorize {
		t.Fatalf("none profile should opt out of detected model defaults, got %#v", selections)
	}
}

func TestSuggestedInstallModelPrefersDbrainProfilesThenSmallOllama(t *testing.T) {
	t.Parallel()

	if got := suggestedInstallModel([]installer.Tool{{
		ID:        installer.ToolOllamaAPI,
		Name:      "Ollama API",
		Available: true,
		Models:    []string{installSmallOllamaAPIModel, installDbrainOllamaAPIModel},
	}}); got != installDbrainOllamaModel {
		t.Fatalf("suggested dbrain model = %q, want %q", got, installDbrainOllamaModel)
	}

	if got := suggestedInstallModel([]installer.Tool{
		{
			ID:        installer.ToolOllamaAPI,
			Name:      "Ollama API",
			Available: true,
			Models:    []string{installSmallOllamaAPIModel},
		},
		{
			ID:        installer.ToolOMLXAPI,
			Name:      "oMLX API",
			Available: true,
			Models:    []string{installDbrainOMLXAPIModel},
		},
	}); got != installSmallOllamaModel {
		t.Fatalf("suggested Ollama model should beat oMLX when both are available, got %q", got)
	}

	if got := suggestedInstallModel([]installer.Tool{{
		ID:        installer.ToolOllamaAPI,
		Name:      "Ollama API",
		Available: true,
		Models:    []string{installSmallOllamaAPIModel},
	}}); got != installSmallOllamaModel {
		t.Fatalf("suggested small model = %q, want %q", got, installSmallOllamaModel)
	}

	if got := suggestedInstallModel([]installer.Tool{
		{
			ID:        installer.ToolOllamaAPI,
			Name:      "Ollama API",
			Available: true,
			Models:    []string{"unrelated:latest"},
		},
		{
			ID:        installer.ToolLMStudioAPI,
			Name:      "LM Studio API",
			Available: true,
			Models:    []string{"qwen/test"},
		},
	}); got != "ollama/unrelated:latest" {
		t.Fatalf("Ollama should beat LM Studio fallback, got %q", got)
	}

	if got := suggestedInstallModel([]installer.Tool{
		{
			ID:        installer.ToolOllamaAPI,
			Name:      "Ollama API",
			Available: true,
			Models:    []string{"qwen-test:latest"},
		},
		{
			ID:        installer.ToolOMLXAPI,
			Name:      "oMLX API",
			Available: true,
			Models:    []string{installDbrainOMLXAPIModel},
		},
	}); got != "ollama/qwen-test:latest" {
		t.Fatalf("generic Ollama should beat exact oMLX fallback, got %q", got)
	}
}

func TestInstallOllamaModelSetupsFollowSelectedProfile(t *testing.T) {
	t.Parallel()

	dbrainSelections := installer.Selections{
		SummaryModel:    installDbrainOllamaModel,
		CategorizeModel: installDbrainOllamaModel,
	}
	setups := installOllamaModelSetups(installFlags{localModelProfile: installModelProfileDbrain}, dbrainSelections)
	if len(setups) != 1 {
		t.Fatalf("dbrain setup count = %d, want 1", len(setups))
	}
	if setups[0].Model != installDbrainOllamaAPIModel || setups[0].PullModel != installDbrainOllamaBase || setups[0].ModelfileName != installDbrainModelfileName || len(setups[0].Modelfile) == 0 {
		t.Fatalf("unexpected dbrain setup: %#v", setups[0])
	}

	setups = installOllamaModelSetups(installFlags{}, dbrainSelections)
	if len(setups) != 1 || setups[0].Model != installDbrainOllamaAPIModel || setups[0].PullModel != installDbrainOllamaBase || len(setups[0].Modelfile) == 0 {
		t.Fatalf("default dbrain selection should request dbrain setup: %#v", setups)
	}

	smallSelections := installer.Selections{SummaryModel: installSmallOllamaModel}
	setups = installOllamaModelSetups(installFlags{localModelProfile: installModelProfileSmallOllama}, smallSelections)
	if len(setups) != 1 || setups[0].Model != installSmallOllamaAPIModel || setups[0].PullModel != installSmallOllamaAPIModel || len(setups[0].Modelfile) != 0 {
		t.Fatalf("unexpected small setup: %#v", setups)
	}

	customSelections := installer.Selections{SummaryModel: "ollama/custom:latest", CategorizeModel: "ollama/custom:latest"}
	if setups := installOllamaModelSetups(installFlags{localModelProfile: installModelProfileDbrain, summaryModel: "ollama/custom:latest"}, customSelections); len(setups) != 0 {
		t.Fatalf("explicit custom model should not request dbrain setup: %#v", setups)
	}
}

func TestValidateInstallModelProfileRejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	if err := validateInstallModelProfile("bogus"); err == nil || !strings.Contains(err.Error(), "unknown local model profile") {
		t.Fatalf("expected unknown profile error, got %v", err)
	}
}

func TestInstallCommandDryRunSkipsFilesAndLaunchd(t *testing.T) {
	home := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "brain")

	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--no-debug",
		"install",
		"--yes",
		"--base-path", basePath,
		"--dry-run",
		"--no-detect",
		"--enable-scheduler",
		"--install-launchd",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install dry-run command: %v", err)
	}

	if _, err := os.Stat(filepath.Join(basePath, "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run unexpectedly created config, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", defaultLaunchdLabel+".plist")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run unexpectedly created launchd plist, err=%v", err)
	}
	if output := stdout.String(); !strings.Contains(output, "Dry run skipped launchd") {
		t.Fatalf("expected dry-run launchd warning, got %q", output)
	}
}

func TestInstallCommandWarnsWhenLaunchdRequestedWithoutScheduler(t *testing.T) {
	home := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "brain")

	t.Setenv("HOME", home)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--no-debug",
		"install",
		"--yes",
		"--base-path", basePath,
		"--force",
		"--no-detect",
		"--install-launchd",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install command: %v", err)
	}
	if output := stdout.String(); !strings.Contains(output, "Launchd installation was skipped because scheduler.sync_all is not enabled.") {
		t.Fatalf("expected launchd scheduler warning, got %q", output)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", defaultLaunchdLabel+".plist")
	if _, err := os.Stat(plistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launchd plist should not be written when scheduler is disabled, err=%v", err)
	}
}

func TestInstallCommandConfigFileUsesXDGDataLayout(t *testing.T) {
	home := t.TempDir()
	xdgData := filepath.Join(home, ".local", "share")
	xdgConfig := filepath.Join(home, ".config")
	configDir := filepath.Join(t.TempDir(), "custom-config")
	configPath := filepath.Join(configDir, "brain.yaml")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_DATA_HOME", xdgData)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--config-file", configPath,
		"--no-debug",
		"install",
		"--yes",
		"--force",
		"--no-detect",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install command: %v", err)
	}
	for _, path := range []string{
		configPath,
		filepath.Join(configDir, "categories.yaml"),
		filepath.Join(xdgData, "dbrain"),
		filepath.Join(xdgData, "dbrain", "vault", "items"),
		filepath.Join(xdgData, "dbrain", "logs"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected install path %s to exist: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(configDir, "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config-file install should keep data under XDG_DATA_HOME, err=%v", err)
	}
	if output := stdout.String(); !strings.Contains(output, configPath) {
		t.Fatalf("expected output to mention config path %s, got %q", configPath, output)
	}
}

func TestInstallCommandWritesLaunchdPlistWhenSchedulerEnabled(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("dbrain install --install-launchd is macOS-only")
	}

	home := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "brain")
	t.Setenv("HOME", home)
	t.Setenv("DBRAIN_ROOT", "")
	t.Setenv("DBRAIN_CONFIG_FILE", "")

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--no-debug",
		"install",
		"--yes",
		"--base-path", basePath,
		"--force",
		"--no-detect",
		"--enable-scheduler",
		"--install-launchd",
		"--bin", "/bin/echo",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("install command: %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", defaultLaunchdLabel+".plist")
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	plist := string(data)
	for _, expected := range []string{
		"<string>/bin/echo</string>",
		"<string>--root</string>",
		"<string>" + basePath + "</string>",
		filepath.Join(basePath, "logs", "launchd.out.log"),
	} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("expected plist to contain %q, got %s", expected, plist)
		}
	}
}

func TestPrintInstallResultUsesNonLaunchdSchedulerHintOffDarwin(t *testing.T) {
	var stdout bytes.Buffer
	printInstallResult(&stdout, installer.Result{}, installer.Selections{EnableScheduler: true}, "linux")

	output := stdout.String()
	if strings.Contains(output, "launchd") {
		t.Fatalf("non-darwin scheduler hint should not mention launchd, got %q", output)
	}
	if !strings.Contains(output, "Configure your OS service manager") {
		t.Fatalf("expected non-darwin service manager hint, got %q", output)
	}
}

func TestImportAppleNotesSummarizeDefaultsEnabled(t *testing.T) {
	t.Parallel()

	cmd := newImportAppleNotesCommand(&rootOptions{})
	flag := cmd.Flags().Lookup("summarize")
	if flag == nil {
		t.Fatal("expected summarize flag to exist")
	}
	if flag.DefValue != "true" {
		t.Fatalf("summarize default = %q, want true", flag.DefValue)
	}
	value, err := cmd.Flags().GetBool("summarize")
	if err != nil {
		t.Fatalf("GetBool(summarize): %v", err)
	}
	if !value {
		t.Fatal("summarize flag value = false, want true")
	}
}

func TestWriteAppleNotesProgressSuppressesCurrentScanNoise(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	for _, event := range []applenotes.ProgressEvent{
		{Phase: "decoded_note", Total: 3, SourceKey: "apple-note:default:current"},
		{Phase: "attachments", Total: 3, SourceKey: "apple-note:default:current", Status: "start"},
		{Phase: "attachment", Total: 3, SourceKey: "apple-note:default:current", Status: "extracting"},
		{Phase: "unchanged", Total: 3, SourceKey: "apple-note:default:current"},
	} {
		writeAppleNotesProgress(&dst, event, false)
	}
	if dst.Len() != 0 {
		t.Fatalf("expected scan/current events to be suppressed, got %q", dst.String())
	}

	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "processing", Total: 3, SourceKey: "apple-note:default:summary", Reason: "summary"}, false)
	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "imported", Total: 3, SourceKey: "apple-note:default:summary", Status: "unchanged", SummaryStatus: "ok"}, false)
	if dst.Len() != 0 {
		t.Fatalf("expected summary-only processing/completion noise to be suppressed, got %q", dst.String())
	}

	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "processing", Total: 3, SourceKey: "apple-note:default:new", Reason: "new"}, false)
	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "summarizing", Total: 3, SourceKey: "apple-note:default:new", Status: "created", SummaryStatus: "running"}, false)
	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "attachments", Total: 1, SourceKey: "apple-note:default:new", Status: "start"}, false)
	output := dst.String()
	for _, value := range []string{"processing source=apple-note:default:new", "summarizing source=apple-note:default:new", "attachments source=apple-note:default:new"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected actionable progress output to contain %q, got %q", value, output)
		}
	}
}

func TestVersionCommandJSON(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload version.Details
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	current := version.Current()
	if payload.Commit != current.Commit {
		t.Fatalf("Commit = %q, want %q", payload.Commit, current.Commit)
	}
	if payload.Short != current.Short {
		t.Fatalf("Short = %q, want %q", payload.Short, current.Short)
	}
	if payload.GitStatus != current.GitStatus {
		t.Fatalf("GitStatus = %q, want %q", payload.GitStatus, current.GitStatus)
	}
	if payload.GoVersion != current.GoVersion {
		t.Fatalf("GoVersion = %q, want %q", payload.GoVersion, current.GoVersion)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	for key, want := range map[string]string{
		"config_dir":      cfg.ConfigDir,
		"config_file":     cfg.ConfigPath,
		"categories_file": cfg.CategoriesPath,
		"data_dir":        cfg.DataDir,
		"database":        cfg.DBPath,
		"vault_dir":       cfg.VaultDir,
		"okf_dir":         cfg.OKFDir,
		"temp_dir":        cfg.TempDir,
		"cache_dir":       cfg.CacheDir,
		"log_dir":         cfg.LogDir,
	} {
		if payload[key] != want {
			t.Fatalf("%s = %q, want %q", key, payload[key], want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandDoesNotMutateTarget(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	legacy := filepath.Join(root, "dbrain-summary-preserve.md")
	if err := os.WriteFile(legacy, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "config", "paths", "--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("config paths mutated legacy file: %v", err)
	}
	if _, err := os.Stat(cfg.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config paths created data directory: %v", err)
	}
}

func TestConfigPathsCommandUsesRootEnv(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv(rootEnvVar, root)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["database"] != cfg.DBPath {
		t.Fatalf("database = %q, want %q", payload["database"], cfg.DBPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandRootFlagOverridesRootEnv(t *testing.T) {
	envRoot := t.TempDir()
	flagRoot := t.TempDir()
	cfg, err := config.Load(flagRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv(rootEnvVar, envRoot)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", flagRoot, "--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["database"] != cfg.DBPath {
		t.Fatalf("database = %q, want %q", payload["database"], cfg.DBPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandConfigFileOverridesRootEnv(t *testing.T) {
	home := t.TempDir()
	envRoot := t.TempDir()
	configDir := filepath.Join(home, "configs", "dbrain")
	dataHome := filepath.Join(home, "data-home")
	configPath := filepath.Join(configDir, "stable.yaml")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("tsnet:\n  hostname: dbrain\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv(rootEnvVar, envRoot)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config-file", configPath, "--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["config_file"] != configPath {
		t.Fatalf("config_file = %q, want %q", payload["config_file"], configPath)
	}
	if payload["database"] != filepath.Join(dataHome, "dbrain", "brain.db") {
		t.Fatalf("database = %q", payload["database"])
	}
	if strings.Contains(payload["database"], envRoot) {
		t.Fatalf("database should not use DBRAIN_ROOT, got %q", payload["database"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigEnvCommandIncludesKnownEnvVars(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "config", "env"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"DBRAIN_ROOT", "DBRAIN_CONFIG_FILE", "DBRAIN_OPENROUTER_API_KEY", "DBRAIN_SOURCE_READER_BASE_URL", "DBRAIN_TSNET_AUTH_KEY_REF", "DBRAIN_TSNET_FUNNEL", "DBRAIN_R2_SECRET_ACCESS_KEY", "config.yaml"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected config env output to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigEnvCommandMarkdownOutput(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "config", "env", "--markdown"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{
		"| Environment variable(s) | config.yaml key | Default | Purpose |",
		"`DBRAIN_ROOT`",
		"`DBRAIN_OPENROUTER_API_KEY / OPENROUTER_API_KEY`",
		"`DBRAIN_RESEARCH_SEMANTIC_MODE`", "`research.semantic.mode`",
		"`DBRAIN_RESEARCH_SEMANTIC_PROVIDER`", "`research.semantic.provider`",
		"`DBRAIN_RESEARCH_SEMANTIC_MODEL`", "`research.semantic.model`",
		"`DBRAIN_RESEARCH_SEMANTIC_DIMENSIONS`", "`research.semantic.dimensions`",
		"`DBRAIN_RESEARCH_SEMANTIC_INDEX_BACKEND`", "`research.semantic.index_backend`",
		"`DBRAIN_RESEARCH_SEMANTIC_CANDIDATE_DEPTH`", "`research.semantic.candidate_depth`",
		"`DBRAIN_RESEARCH_SEMANTIC_EXACT_FALLBACK_MAX_CHUNKS`", "`research.semantic.exact_fallback_max_chunks`",
		"`DBRAIN_TSNET_MCP_PATH`", "`DBRAIN_TSNET_FUNNEL`",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected markdown config env output to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestServeRemoteHelpIncludesTSNetFlags(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "remote", "--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"--tsnet-hostname", "--tsnet-state-dir", "--tsnet-funnel", "--mcp-path", "read/write web UI"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected serve remote help to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestServeMCPHelpIncludesTSNetTransport(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "mcp", "--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"stdio, http, or tsnet", "--mcp-path", "--tsnet-hostname", "--tsnet-funnel", "--tsnet-auth-key-ref"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected serve mcp help to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestServeRemoteRequiresAtLeastOneSurface(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "serve", "remote", "--web=false", "--mcp=false"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "at least one surface") {
		t.Fatalf("expected at least-one-surface error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestServeRemoteRejectsFunnelWithoutTLS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "serve", "remote", "--web=false", "--mcp=true", "--tsnet-funnel", "--tsnet-tls=false"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tsnet funnel requires --tsnet-tls=true") {
		t.Fatalf("expected Funnel TLS validation error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestServeRemoteRejectsFunnelUnsupportedPort(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "serve", "remote", "--web=false", "--mcp=true", "--tsnet-funnel", "--tsnet-listen", ":80"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tsnet funnel listen port must be 443, 8443, or 10000") {
		t.Fatalf("expected Funnel port validation error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestServeMCPRejectsFunnelWithoutTLS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "serve", "mcp", "--transport", "tsnet", "--tsnet-funnel", "--tsnet-tls=false"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tsnet funnel requires --tsnet-tls=true") {
		t.Fatalf("expected Funnel TLS validation error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestServeMCPRejectsFunnelUnsupportedPort(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "serve", "mcp", "--transport", "tsnet", "--tsnet-funnel", "--tsnet-listen", ":80"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tsnet funnel listen port must be 443, 8443, or 10000") {
		t.Fatalf("expected Funnel port validation error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestTSNetStatusReportsResolvedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "status", "--json", "--tsnet-hostname", "dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	wantStateDir := filepath.Join(root, "data", "tsnet", "dbrain")
	if payload["state_dir"] != wantStateDir {
		t.Fatalf("state_dir = %#v, want %q", payload["state_dir"], wantStateDir)
	}
	if payload["control_url"] != "" {
		t.Fatalf("control_url = %#v, want empty", payload["control_url"])
	}
	if payload["exists"] != false {
		t.Fatalf("exists = %#v, want false", payload["exists"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusReadsExplicitConfigFileValues(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "configs")
	dataHome := filepath.Join(home, "data-home")
	configPath := filepath.Join(configDir, "stable.yaml")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
tsnet:
  hostname: dbrain-stable
  funnel: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("DBRAIN_ROOT", t.TempDir())
	t.Setenv("DBRAIN_TSNET_HOSTNAME", "")

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config-file", configPath, "--no-debug", "tsnet", "status", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	wantStateDir := filepath.Join(dataHome, "dbrain", "tsnet", "dbrain-stable")
	if payload["state_dir"] != wantStateDir {
		t.Fatalf("state_dir = %#v, want %q", payload["state_dir"], wantStateDir)
	}
	if payload["funnel"] != true {
		t.Fatalf("funnel = %#v, want true", payload["funnel"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusAcceptsRemoteSurfaceAndListenFlags(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"--no-debug",
		"tsnet", "status",
		"--json",
		"--web=false",
		"--mcp=true",
		"--mcp-path", "/brain",
		"--tsnet-listen", ":8080",
		"--tsnet-tls=false",
		"--tsnet-control-url", "https://control.example",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["tls"] != false {
		t.Fatalf("tls = %#v, want false", payload["tls"])
	}
	if payload["control_url"] != "https://control.example" {
		t.Fatalf("control_url = %#v", payload["control_url"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusAcceptsFunnelFlag(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"--no-debug",
		"tsnet", "status",
		"--json",
		"--web=false",
		"--mcp=true",
		"--tsnet-funnel",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["funnel"] != true {
		t.Fatalf("funnel = %#v, want true", payload["funnel"])
	}
	if payload["tls"] != true {
		t.Fatalf("tls = %#v, want true", payload["tls"])
	}
	if !strings.Contains(fmt.Sprint(payload["warning"]), "Tailscale Funnel is enabled") {
		t.Fatalf("warning = %#v, want Funnel warning", payload["warning"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusRejectsInvalidFunnelPort(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--root", t.TempDir(),
		"--no-debug",
		"tsnet", "status",
		"--tsnet-funnel",
		"--tsnet-listen", ":80",
	})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tsnet funnel listen port must be 443, 8443, or 10000") {
		t.Fatalf("ExecuteContext error = %v, want Funnel port validation", err)
	}
}

func TestTSNetStatusRejectsInvalidMCPPath(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--root", t.TempDir(),
		"--no-debug",
		"tsnet", "status",
		"--mcp-path", "/",
	})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mcp path must not be /") {
		t.Fatalf("ExecuteContext error = %v, want mcp path validation", err)
	}
}

func TestTSNetStatusIgnoresDisabledServeSurfaces(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
tsnet:
  web: false
  mcp: false
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "status"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !strings.Contains(stdout.String(), "State dir") {
		t.Fatalf("expected status output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusReportsLockedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
tsnet:
  web: false
  mcp: false
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	stateDir, err := remote.PrepareStateDir(filepath.Join(root, "data", "tsnet", "dbrain"))
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lock, err := remote.AcquireStateLock(stateDir)
	if err != nil {
		t.Fatalf("AcquireStateLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "status", "--json", "--tsnet-hostname", "dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["exists"] != true {
		t.Fatalf("exists = %#v, want true", payload["exists"])
	}
	if payload["locked"] != true {
		t.Fatalf("locked = %#v, want true", payload["locked"])
	}
	if payload["running"] != true {
		t.Fatalf("running = %#v, want true", payload["running"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStateStatusReportsHealthyRunningNode(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: 200, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return []string{"100.64.0.10"}
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "healthy" || !status.Running || !status.Reachable || !status.WebReachable || !status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net/" {
		t.Fatalf("WebURL = %q", status.WebURL)
	}
	if status.MCPURL != "https://dbrain.tailnet.ts.net/mcp" {
		t.Fatalf("MCPURL = %q", status.MCPURL)
	}
	if status.CertHealth != "ok" {
		t.Fatalf("CertHealth = %q", status.CertHealth)
	}
	if status.NeedsLogin {
		t.Fatalf("NeedsLogin = true, want false")
	}
}

func TestTSNetStateStatusReportsFunnelURLsAndWarning(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":10000",
		TLS:      true,
		Funnel:   true,
	}
	var probed []string
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			probed = append(probed, rawURL)
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusOK, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if !status.Funnel {
		t.Fatalf("Funnel = false, want true")
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net:10000/" {
		t.Fatalf("WebURL = %q", status.WebURL)
	}
	if status.MCPURL != "https://dbrain.tailnet.ts.net:10000/mcp" {
		t.Fatalf("MCPURL = %q", status.MCPURL)
	}
	if !strings.Contains(status.Warning, "Tailscale Funnel is enabled") {
		t.Fatalf("Warning = %q, want Funnel warning", status.Warning)
	}
	if len(probed) != 2 || probed[0] != status.WebURL || probed[1] != status.MCPURL {
		t.Fatalf("probed URLs = %#v", probed)
	}
}

func TestTSNetStateStatusIncludesRunningScheduler(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      false,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	started := time.Date(2026, time.May, 10, 2, 17, 42, 0, time.UTC)
	var schedulerURL string
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusOK, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		fetchScheduler: func(_ context.Context, rawURL string, _ string) (schedulerstate.SyncAllStatus, error) {
			schedulerURL = rawURL
			return schedulerstate.SyncAllStatus{
				Enabled:          true,
				Interval:         "1h0m0s",
				Running:          true,
				CurrentReason:    "interval",
				CurrentStartedAt: started,
			}, nil
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if schedulerURL != "https://dbrain.tailnet.ts.net/api/scheduler/sync-all" {
		t.Fatalf("schedulerURL = %q", schedulerURL)
	}
	if status.SyncAll == nil || !status.SyncAll.Running || status.SyncAll.CurrentReason != "interval" || !status.SyncAll.CurrentStartedAt.Equal(started) {
		t.Fatalf("unexpected scheduler status: %#v", status.SyncAll)
	}
}

func TestTSNetStateStatusIncludesIdleScheduler(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      false,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusOK, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		fetchScheduler: func(context.Context, string, string) (schedulerstate.SyncAllStatus, error) {
			return schedulerstate.SyncAllStatus{Enabled: true, Interval: "1h0m0s", Running: false}, nil
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.SyncAll == nil || !status.SyncAll.Enabled || status.SyncAll.Running {
		t.Fatalf("unexpected idle scheduler status: %#v", status.SyncAll)
	}
}

func TestTSNetStateStatusReportsSanitizedSchedulerAuthFailure(t *testing.T) {
	t.Parallel()

	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		statusCode := statusCode
		t.Run(fmt.Sprintf("status_%d", statusCode), func(t *testing.T) {
			t.Parallel()
			const secretBody = `credential=super-secret-token endpoint=https://private.example/api/scheduler/sync-all`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, secretBody)
			}))
			defer server.Close()

			stateDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			status, err := tsnetStateStatusWithDeps(context.Background(), remote.Options{
				Web: true, Hostname: "dbrain", StateDir: stateDir, Listen: ":443", TLS: true,
			}, tsnetStatusDeps{
				acquireStateLock: func(string) (io.Closer, error) {
					return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
				},
				probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
					return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusOK, EffectiveURL: rawURL, CertHealth: "ok"}
				},
				fetchScheduler: func(ctx context.Context, _ string, _ string) (schedulerstate.SyncAllStatus, error) {
					return fetchTSNetSchedulerStatus(ctx, server.URL, "")
				},
				lookupIPs: func(context.Context, string) []string { return nil },
				readCertState: func(string, bool) tsnetCertState {
					return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
				},
			})
			if err != nil {
				t.Fatalf("tsnetStateStatusWithDeps: %v", err)
			}
			if status.SyncAll != nil {
				t.Fatalf("SyncAll = %#v, want nil on authentication failure", status.SyncAll)
			}
			if status.SyncAllError == nil || status.SyncAllError.Code != "scheduler_auth_failed" || status.SyncAllError.StatusCode != statusCode {
				t.Fatalf("unexpected scheduler diagnostic: %#v", status.SyncAllError)
			}
			payload, err := json.Marshal(status)
			if err != nil {
				t.Fatalf("marshal status: %v", err)
			}
			for _, forbidden := range []string{"super-secret-token", "private.example", server.URL, secretBody} {
				if strings.Contains(string(payload), forbidden) {
					t.Fatalf("scheduler diagnostic leaked %q: %s", forbidden, payload)
				}
			}
		})
	}
}

func TestWriteTSNetStatusRendersTables(t *testing.T) {
	t.Parallel()

	started := time.Now().UTC().Add(-2 * time.Minute)
	var out bytes.Buffer
	err := writeTSNetStatus(&out, tsnetStateInfo{
		Hostname:     "dbrain-dev",
		StateDir:     "/tmp/dbrain/tsnet/dbrain-dev",
		Exists:       true,
		Locked:       true,
		Running:      true,
		Reachable:    true,
		WebReachable: true,
		MCPReachable: true,
		TailnetIPs:   []string{"100.105.187.55"},
		WebURL:       "https://dbrain-dev.tailbdd5.ts.net/",
		MCPURL:       "https://dbrain-dev.tailbdd5.ts.net/mcp",
		TLS:          true,
		Funnel:       true,
		State:        "healthy",
		CertHealth:   "ok",
		LockPath:     "/tmp/dbrain/tsnet/dbrain-dev/dbrain.lock",
		SyncAll: &schedulerstate.SyncAllStatus{
			Enabled:          true,
			Interval:         "1h0m0s",
			Jitter:           "5m0s",
			Running:          true,
			CurrentReason:    "interval",
			CurrentStartedAt: started,
			LastStatus:       "ok",
		},
	})
	if err != nil {
		t.Fatalf("writeTSNetStatus: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"TSNet Node",
		"TSNet Endpoints",
		"Scheduled Sync All",
		"Hostname",
		"dbrain-dev",
		"Funnel",
		"Web URL",
		"https://dbrain-dev.tailbdd5.ts.net/",
		"Current reason",
		"interval",
		"Current elapsed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestWriteTSNetStatusRendersSanitizedSchedulerDiagnostic(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := writeTSNetStatus(&out, tsnetStateInfo{
		Hostname: "dbrain-dev",
		SyncAllError: &tsnetSchedulerStatusDiagnostic{
			Code:       "scheduler_auth_failed",
			StatusCode: http.StatusUnauthorized,
		},
	})
	if err != nil {
		t.Fatalf("writeTSNetStatus: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Scheduled Sync All", "scheduler_auth_failed", "401"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestTimeStringWithRelative(t *testing.T) {
	now := time.Date(2026, 5, 11, 1, 16, 2, 0, time.UTC)

	tests := []struct {
		name  string
		value time.Time
		want  string
	}{
		{
			name:  "zero",
			value: time.Time{},
			want:  "-",
		},
		{
			name:  "past minutes",
			value: time.Date(2026, 5, 11, 0, 21, 2, 0, time.UTC),
			want:  "2026-05-11T00:21:02Z (55 minutes ago)",
		},
		{
			name:  "future minutes",
			value: time.Date(2026, 5, 11, 2, 1, 2, 0, time.UTC),
			want:  "2026-05-11T02:01:02Z (45 minutes from now)",
		},
		{
			name:  "singular hour",
			value: time.Date(2026, 5, 11, 0, 16, 2, 0, time.UTC),
			want:  "2026-05-11T00:16:02Z (1 hour ago)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timeStringWithRelative(tt.value, now); got != tt.want {
				t.Fatalf("timeStringWithRelative() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTSNetStatusTableWidthIsCapped(t *testing.T) {
	t.Setenv("COLUMNS", "220")

	if got := tsnetStatusTableWidth(&bytes.Buffer{}); got != 132 {
		t.Fatalf("tsnetStatusTableWidth = %d, want 132", got)
	}
}

func TestTSNetStateStatusUsesSurfaceSpecificProbeStatus(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      false,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: 404, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.WebReachable || status.Reachable || status.State != "down" {
		t.Fatalf("expected web 404 to be down, status=%#v", status)
	}

	opts.Web = false
	opts.MCP = true
	status, err = tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusMethodNotAllowed, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if !status.MCPReachable || !status.Reachable || status.State != "healthy" || status.WebURL != "" {
		t.Fatalf("expected MCP 405 to be healthy without web URL, status=%#v", status)
	}
}

func TestTSNetStateStatusReportsNonDefaultPortAndControlURL(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:        true,
		MCP:        true,
		MCPPath:    "/mcp",
		Hostname:   "dbrain",
		StateDir:   stateDir,
		Listen:     ":8443",
		TLS:        true,
		ControlURL: "https://control.example",
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusOK, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.ControlURL != "https://control.example" {
		t.Fatalf("ControlURL = %q", status.ControlURL)
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net:8443/" || status.MCPURL != "https://dbrain.tailnet.ts.net:8443/mcp" {
		t.Fatalf("unexpected URLs: web=%q mcp=%q", status.WebURL, status.MCPURL)
	}
	if !strings.Contains(status.Warning, "custom tsnet control URL is experimental") {
		t.Fatalf("expected custom control warning, got %q", status.Warning)
	}
}

func TestTSNetStateStatusFallsBackToShortHostWithTLSServerName(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	var alternateProbes int
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, tlsServerName string) tsnetEndpointProbe {
			if strings.Contains(rawURL, "dbrain.tailnet.ts.net") {
				return tsnetEndpointProbe{Reachable: false, Error: "no such host", CertHealth: "ok"}
			}
			if strings.Contains(rawURL, "https://dbrain/") && tlsServerName == "dbrain.tailnet.ts.net" {
				alternateProbes++
				return tsnetEndpointProbe{Reachable: true, StatusCode: 200, EffectiveURL: rawURL, CertHealth: "ok"}
			}
			return tsnetEndpointProbe{Reachable: false, Error: "unexpected probe", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "healthy" || !status.Reachable || !status.WebReachable || !status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net/" || status.MCPURL != "https://dbrain.tailnet.ts.net/mcp" {
		t.Fatalf("unexpected URLs: web=%q mcp=%q", status.WebURL, status.MCPURL)
	}
	if alternateProbes != 2 {
		t.Fatalf("alternateProbes = %d, want 2", alternateProbes)
	}
}

func TestTSNetStateStatusFallsBackToPeerIPWithTLSServerName(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	var ipProbes int
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, tlsServerName string) tsnetEndpointProbe {
			if strings.Contains(rawURL, "100.64.0.10") && tlsServerName == "dbrain.tailnet.ts.net" {
				ipProbes++
				return tsnetEndpointProbe{Reachable: true, StatusCode: 200, EffectiveURL: rawURL, CertHealth: "ok"}
			}
			return tsnetEndpointProbe{Reachable: false, Error: "no such host", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return []string{"100.64.0.10"}
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "healthy" || !status.Reachable || !status.WebReachable || !status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if ipProbes != 2 {
		t.Fatalf("ipProbes = %d, want 2", ipProbes)
	}
}

func TestTSNetStateStatusReportsDownWhenLockedButUnreachable(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(context.Context, string, string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: false, Error: "connection refused", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "down" || !status.Running || status.Reachable || status.WebReachable || status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if status.WebError == "" || status.MCPError == "" {
		t.Fatalf("expected probe errors, status = %#v", status)
	}
}

func TestTSNetStateStatusReportsNeedsLogin(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(context.Context, string, string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: false, Error: "not listening", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "missing"}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "needs_login" || !status.NeedsLogin {
		t.Fatalf("unexpected status = %#v", status)
	}
}

func TestTSNetResetRequiresConfirmation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateDir := filepath.Join(root, "data", "tsnet", "dbrain")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stateFile := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(stateFile, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetIn(strings.NewReader("no\n"))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "reset", "--tsnet-hostname", "dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected state file to remain after abort: %v", err)
	}
	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Fatalf("expected aborted output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetResetRefusesLockedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateDir, err := remote.PrepareStateDir(filepath.Join(root, "data", "tsnet", "dbrain"))
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	stateFile := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(stateFile, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lock, err := remote.AcquireStateLock(stateDir)
	if err != nil {
		t.Fatalf("AcquireStateLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "reset", "--yes", "--tsnet-hostname", "dbrain"})

	err = cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("expected locked-state error, got %v", err)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected state file to remain after locked reset: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestTSNetResetRemovesStateWithYes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateDir := filepath.Join(root, "data", "tsnet", "dbrain")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "reset", "--yes", "--tsnet-hostname", "dbrain"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected state dir removed, stat err=%v", err)
	}
	if !strings.Contains(stdout.String(), "Removed tsnet state directory") {
		t.Fatalf("expected removal output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestEvalMCPWriteExample(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-caffeinate", "--no-debug", "eval", "mcp", "--write-example", "-"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !strings.Contains(stdout.String(), "expect_any_source_keys") {
		t.Fatalf("expected example eval JSON, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestSyncCommandHelpIncludesAll(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"sync"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "all") {
		t.Fatalf("expected sync help output to contain %q, got %q", "all", output)
	}
}

func TestSyncAllCommandPassesSeparateXMediaAndPhotoOCRLimits(t *testing.T) {
	root := t.TempDir()
	var captured syncjob.Options

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() {
		runSyncAll = oldRunSyncAll
	})
	runSyncAll = func(_ context.Context, cfg config.Config, _ *store.Store, opts syncjob.Options) (syncjob.Stats, error) {
		if cfg.RootDir != root {
			t.Fatalf("expected root %s, got %s", root, cfg.RootDir)
		}
		captured = opts
		now := time.Unix(0, 0).UTC()
		return syncjob.Stats{StartedAt: now, CompletedAt: now}, nil
	}

	cmd := newSyncAllCommand(&rootOptions{root: root})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--json",
		"--skip-github",
		"--categorize-model", "ollama/test-categorizer",
		"--ocr-model", "ollama/test-ocr",
		"--x-limit", "7",
		"--x-media-limit", "3",
		"--x-photo-ocr-limit", "5",
		"--x-media-download-timeout", "45m",
		"--okf-export",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if captured.XLimit != 7 {
		t.Fatalf("expected x limit 7, got %d", captured.XLimit)
	}
	if captured.XMediaLimit != 3 {
		t.Fatalf("expected x media limit 3, got %d", captured.XMediaLimit)
	}
	if captured.XPhotoOCRLimit != 5 {
		t.Fatalf("expected x photo OCR limit 5, got %d", captured.XPhotoOCRLimit)
	}
	if captured.XMediaTimeout != 45*time.Minute {
		t.Fatalf("expected x media download timeout 45m, got %s", captured.XMediaTimeout)
	}
	if !captured.OKFExportEnabled {
		t.Fatalf("expected OKF export enabled")
	}
	stderrText := stderr.String()
	for _, want := range []string{
		"dbrain version:",
		"SQLite migration running schema_version=1",
		"SQLite migration applied schema_version=1",
	} {
		if !strings.Contains(stderrText, want) {
			t.Fatalf("expected sync startup stderr to contain %q, got %q", want, stderrText)
		}
	}
}

func TestSyncAllCommandOpensConfiguredMetricsSink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: sync-metrics.jsonl
  detail: stage
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() {
		runSyncAll = oldRunSyncAll
	})

	var captured syncjob.Options
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, opts syncjob.Options) (syncjob.Stats, error) {
		captured = opts
		if !opts.Metrics.Enabled() {
			t.Fatal("expected metrics enabled")
		}
		if opts.Metrics.Invocation != "cli" {
			t.Fatalf("metrics invocation = %q, want cli", opts.Metrics.Invocation)
		}
		if err := opts.Metrics.Emit(map[string]any{"event": "test.metrics", "status": "ok"}); err != nil {
			t.Fatalf("emit metrics: %v", err)
		}
		now := time.Unix(0, 0).UTC()
		return syncjob.Stats{StartedAt: now, CompletedAt: now}, nil
	}

	cmd := newSyncAllCommand(&rootOptions{root: root})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--json",
		"--skip-github",
		"--skip-x-photo-ocr",
		"--skip-categorize",
		"--skip-youtube",
		"--skip-x-bookmarks",
		"--skip-x",
		"--skip-x-media",
		"--skip-links",
		"--skip-sources",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	var jsonStats syncjob.Stats
	if err := json.Unmarshal(stdout.Bytes(), &jsonStats); err != nil {
		t.Fatalf("stdout was not valid sync stats JSON: %v\n%s", err, stdout.String())
	}
	if jsonStats.StartedAt.IsZero() || jsonStats.CompletedAt.IsZero() {
		t.Fatalf("unexpected stdout stats: %+v", jsonStats)
	}
	if captured.Metrics.RunID == "" || captured.Metrics.Command != "sync all" {
		t.Fatalf("unexpected metrics context: %+v", captured.Metrics)
	}
	path := filepath.Join(root, "logs", "sync-metrics.jsonl")
	events := readAppMetricEvents(t, path)
	if len(events) != 1 || events[0]["event"] != "test.metrics" || events[0]["invocation"] != "cli" {
		t.Fatalf("unexpected metrics events: %#v", events)
	}
}

func TestSyncAllCommandFailsBeforeWorkWhenMetricsPathCannotOpen(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: /dev/null/metrics.jsonl
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() {
		runSyncAll = oldRunSyncAll
	})

	called := false
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, _ syncjob.Options) (syncjob.Stats, error) {
		called = true
		return syncjob.Stats{}, nil
	}

	cmd := newSyncAllCommand(&rootOptions{root: root})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--json"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("ExecuteContext succeeded, want metrics open error")
	}
	if called {
		t.Fatal("runSyncAll was called after metrics open failed")
	}
	if !strings.Contains(err.Error(), "open metrics file") && !strings.Contains(err.Error(), "create metrics directory") {
		t.Fatalf("unexpected error: %v (stderr=%q)", err, stderr.String())
	}
}

func TestSyncAllCommandFailsWhenRunLockHeld(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	lock, err := acquireSyncAllLock(cfg, "test")
	if err != nil {
		t.Fatalf("acquireSyncAllLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	cmd := newSyncAllCommand(&rootOptions{root: root})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--json"})

	err = cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected sync all to fail while lock is held")
	}
	if !isSyncAllAlreadyRunning(err) || !strings.Contains(err.Error(), "sync all already running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSyncAllFlagsUsesRootEnvForUnsetValues(t *testing.T) {
	root := t.TempDir()
	clearSyncEnvForTest(t)
	env := strings.Join([]string{
		"DBRAIN_AUTO_ARCHIVE_MEDIA=true",
		"DBRAIN_APPLE_NOTES_ENABLED=true",
		"DBRAIN_APPLE_NOTES_DB_PATH=/tmp/notes.sqlite",
		"DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS=Archive, Trash",
		"DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS=Work",
		"DBRAIN_APPLE_NOTES_EXCLUDE_SHARED=true",
		"DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS=false",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_OCR=false",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES=12345",
		"DBRAIN_APPLE_NOTES_TESSERACT_BINARY=/opt/bin/tesseract",
		"DBRAIN_SAFARI_TABS_ENABLED=true",
		"DBRAIN_SAFARI_TABS_DB_PATH=/tmp/cloudtabs.db",
		"DBRAIN_SAFARI_TABS_DEVICE=phone",
		"DBRAIN_SAFARI_TABS_LIMIT=8",
		"DBRAIN_SAFARI_TABS_OLDER_THAN=2h",
		"DBRAIN_OKF_EXPORT_ENABLED=true",
		"DBRAIN_SCHEDULER_SYNC_ALL_SKIP_X_PHOTO_OCR=true",
		"DBRAIN_SCHEDULER_SYNC_ALL_SKIP_CATEGORIZE=true",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if !resolved.archiveMedia || !resolved.appleNotes || !resolved.safariTabs {
		t.Fatalf("expected archive/apple-notes/safari-tabs enabled, got archive=%v apple=%v safari=%v", resolved.archiveMedia, resolved.appleNotes, resolved.safariTabs)
	}
	if !resolved.okfExport {
		t.Fatalf("expected OKF export enabled from env")
	}
	if !resolved.skipXPhotoOCR || !resolved.skipCategorize {
		t.Fatalf("expected scheduler skip flags to apply to sync defaults, got x_photo_ocr=%v categorize=%v", resolved.skipXPhotoOCR, resolved.skipCategorize)
	}
	if resolved.appleNotesDBPath != "/tmp/notes.sqlite" {
		t.Fatalf("appleNotesDBPath = %q", resolved.appleNotesDBPath)
	}
	if got := strings.Join(resolved.appleNotesExcludeFolders, ","); got != "Archive,Trash" {
		t.Fatalf("appleNotesExcludeFolders = %q", got)
	}
	if got := strings.Join(resolved.appleNotesExcludeAccounts, ","); got != "Work" {
		t.Fatalf("appleNotesExcludeAccounts = %q", got)
	}
	if !resolved.appleNotesExcludeShared || !resolved.appleNotesSkipAttachments || !resolved.appleNotesSkipAttachmentOCR {
		t.Fatalf("expected Apple Notes exclusion/skip booleans resolved, got shared=%v skipAttachments=%v skipOCR=%v", resolved.appleNotesExcludeShared, resolved.appleNotesSkipAttachments, resolved.appleNotesSkipAttachmentOCR)
	}
	if resolved.appleNotesAttachmentMaxBytes != 12345 || resolved.appleNotesTesseractBinary != "/opt/bin/tesseract" {
		t.Fatalf("unexpected Apple Notes attachment settings: max=%d tesseract=%q", resolved.appleNotesAttachmentMaxBytes, resolved.appleNotesTesseractBinary)
	}
	if resolved.safariTabsDBPath != "/tmp/cloudtabs.db" || resolved.safariTabsDevice != "phone" || resolved.safariTabsLimit != 8 || resolved.safariTabsOlderThan != 2*time.Hour {
		t.Fatalf("unexpected Safari tabs settings: db=%q device=%q limit=%d older=%s", resolved.safariTabsDBPath, resolved.safariTabsDevice, resolved.safariTabsLimit, resolved.safariTabsOlderThan)
	}
}

func readAppMetricEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("parse metrics line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func TestResolveSyncAllFlagsKeepsExplicitValues(t *testing.T) {
	root := t.TempDir()
	clearSyncEnvForTest(t)
	env := strings.Join([]string{
		"DBRAIN_APPLE_NOTES_ENABLED=true",
		"DBRAIN_APPLE_NOTES_DB_PATH=/tmp/env-notes.sqlite",
		"DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS=Env",
		"DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS=false",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES=999",
		"DBRAIN_SAFARI_TABS_ENABLED=true",
		"DBRAIN_SAFARI_TABS_DB_PATH=/tmp/env-cloudtabs.db",
		"DBRAIN_SAFARI_TABS_DEVICE=env-device",
		"DBRAIN_SAFARI_TABS_LIMIT=20",
		"DBRAIN_SAFARI_TABS_OLDER_THAN=4h",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{
		appleNotes:                   true,
		appleNotesDBPath:             "/tmp/explicit-notes.sqlite",
		appleNotesExcludeFolders:     []string{"Explicit"},
		appleNotesAttachmentMaxBytes: 10,
		safariTabs:                   true,
		safariTabsDBPath:             "/tmp/explicit-cloudtabs.db",
		safariTabsDevice:             "explicit-device",
		safariTabsLimit:              3,
		safariTabsOlderThan:          30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if resolved.appleNotesDBPath != "/tmp/explicit-notes.sqlite" || strings.Join(resolved.appleNotesExcludeFolders, ",") != "Explicit" {
		t.Fatalf("explicit Apple Notes values were not preserved: db=%q folders=%v", resolved.appleNotesDBPath, resolved.appleNotesExcludeFolders)
	}
	if resolved.appleNotesAttachmentMaxBytes != 10 {
		t.Fatalf("appleNotesAttachmentMaxBytes = %d, want 10", resolved.appleNotesAttachmentMaxBytes)
	}
	if resolved.safariTabsDBPath != "/tmp/explicit-cloudtabs.db" || resolved.safariTabsDevice != "explicit-device" || resolved.safariTabsLimit != 3 || resolved.safariTabsOlderThan != 30*time.Minute {
		t.Fatalf("explicit Safari values were not preserved: db=%q device=%q limit=%d older=%s", resolved.safariTabsDBPath, resolved.safariTabsDevice, resolved.safariTabsLimit, resolved.safariTabsOlderThan)
	}
}

func clearSyncEnvForTest(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DBRAIN_AUTO_ARCHIVE_MEDIA",
		"DBRAIN_ARCHIVE_AUTO",
		"DBRAIN_APPLE_NOTES_ENABLED",
		"DBRAIN_APPLE_NOTES_DB_PATH",
		"DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS",
		"DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS",
		"DBRAIN_APPLE_NOTES_EXCLUDE_SHARED",
		"DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS",
		"DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_OCR",
		"DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES",
		"DBRAIN_APPLE_NOTES_TESSERACT_BINARY",
		"DBRAIN_SAFARI_TABS_ENABLED",
		"DBRAIN_SAFARI_TABS_DB_PATH",
		"DBRAIN_SAFARI_TABS_DEVICE",
		"DBRAIN_SAFARI_TABS_LIMIT",
		"DBRAIN_SAFARI_TABS_OLDER_THAN",
		"DBRAIN_OKF_EXPORT_ENABLED",
		"DBRAIN_SYNC_OKF_EXPORT",
		"DBRAIN_SYNC_ALL_IMPORT_X_BOOKMARKS",
		"DBRAIN_SYNC_ALL_IMPORT_GITHUB_STARS",
		"DBRAIN_SYNC_ALL_IMPORT_YOUTUBE_WATCH_LATER",
		"DBRAIN_SYNC_ALL_IMPORT_YOUTUBE_LIKED",
		"DBRAIN_SYNC_ALL_IMPORT_FEEDS",
		"DBRAIN_SYNC_ALL_IMPORT_APPLE_NOTES",
		"DBRAIN_SYNC_ALL_IMPORT_SAFARI_TABS",
		"DBRAIN_SYNC_ALL_BROWSER",
		"DBRAIN_SYNC_ALL_PROFILE",
		"DBRAIN_SCHEDULER_SYNC_ALL_SKIP_X_PHOTO_OCR",
		"DBRAIN_SCHEDULER_SYNC_ALL_SKIP_CATEGORIZE",
	} {
		t.Setenv(key, "")
	}
}

func TestImportCommandHelpIncludesYouTubeImporter(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"import"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"github", "x-bookmarks", "youtube"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected import help output to contain %q, got %q", value, output)
		}
	}
}

func TestServeCommandHelpIncludesSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"mcp", "web"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected serve help output to contain %q, got %q", value, output)
		}
	}
}

func TestTopicCommandHelpIncludesTopicSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"topic"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"map", "generate", "refresh", "index"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected topic help output to contain %q, got %q", value, output)
		}
	}
}

func TestEntityCommandHelpIncludesSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"entity"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"map", "generate", "index"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected entity help output to contain %q, got %q", value, output)
		}
	}
}

func TestExtractCommandHelpIncludesLinksAndSources(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"extract"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"links", "sources"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected extract help output to contain %q, got %q", value, output)
		}
	}
}

func TestCategorizeCommandHelpIncludesLMStudioModels(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"categorize", "item", "--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "lmstudio/*") {
		t.Fatalf("expected categorize help output to contain %q, got %q", "lmstudio/*", output)
	}
	if !strings.Contains(output, "omlx/*") {
		t.Fatalf("expected categorize help output to contain %q, got %q", "omlx/*", output)
	}
	if !strings.Contains(output, "configured alias") {
		t.Fatalf("expected categorize help output to contain %q, got %q", "configured alias", output)
	}
}

func TestTranscribeCommandHelpIncludesXMedia(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"transcribe"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "x-media") {
		t.Fatalf("expected transcribe help output to contain %q, got %q", "x-media", output)
	}
}

func TestOCRCommandHelpIncludesXPhotos(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"ocr"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "x-photos") {
		t.Fatalf("expected ocr help output to contain %q, got %q", "x-photos", output)
	}
}

func TestWorkerCommandHelpIncludesSources(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"worker"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "sources") {
		t.Fatalf("expected worker help output to contain %q, got %q", "sources", output)
	}
}

func TestLinkCommandHelpIncludesAdd(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"link"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "add") {
		t.Fatalf("expected link help output to contain add, got %q", output)
	}
}

func TestLinkAddQueuesManualSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "link", "add", "https://example.com/manual?utm_source=test"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v stderr=%s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Queued: 1") || !strings.Contains(output, "created src:") || !strings.Contains(output, "https://example.com/manual") {
		t.Fatalf("unexpected link add output %q", output)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()
	pending, err := st.ListSourcesForEnrichment(context.Background(), 10, false, true, "dbrain-v1", "summarize", "0.1.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(pending) != 1 || pending[0].CanonicalURL != "https://example.com/manual" {
		t.Fatalf("expected pending normalized manual source, got %+v", pending)
	}
}

func TestRepairSourcesRequiresConfirmationAndResetsDomain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	sourceResult, err := st.UpsertSource(context.Background(), model.SourceCandidate{
		SourceKey:     "src:repair-canada",
		OriginalURL:   "https://canada.ca/en/news",
		CanonicalURL:  "https://canada.ca/en/news",
		NormalizedURL: "https://canada.ca/en/news",
		SourceType:    "web",
		Domain:        "canada.ca",
		NotePath:      "sources/web/repair-canada.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://canada.ca/en/news",
		FinalURL:     "https://canada.ca/en/news",
		Title:        "Canada source",
		Content:      "Canada source text",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, "hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), sourceResult.SourceID, model.SummaryResult{
		Text:          "Canada source summary",
		Status:        "ok",
		Model:         "test-model",
		PromptVersion: "dbrain-v1",
		Tool:          "summarize",
		ToolVersion:   "test-version",
		FetchedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "repair", "sources", "--domain", "canada.ca"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v stderr=%s", err, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "This will reset extraction and summary state for 1 sources") ||
		!strings.Contains(output, "Sources reset: 1") {
		t.Fatalf("unexpected repair sources output %q", output)
	}

	st, err = store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()
	source, err := st.GetSourceByID(context.Background(), sourceResult.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.ExtractStatus != "" || source.SummaryStatus != "" || source.ExtractedText != "" || source.SummaryText != "" {
		t.Fatalf("expected source enrichment reset, got %+v", source)
	}
}

func TestWriteSyncStatsIncludesXMediaStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.April, 24, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.April, 24, 15, 2, 0, 0, time.UTC),
		Duration:    2 * time.Minute,
		XMedia: &syncjob.XMediaStage{
			Stats: xmediatranscribe.Stats{
				ItemsProcessed:   10,
				ItemsUpdated:     6,
				ItemsSkipped:     4,
				MediaTranscribed: 6,
				Errors:           1,
				SummaryErrors:    2,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "X Media", "processed=10 transcribed=6", "summarized=0 skipped=4", "3"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestWriteSyncStatsIncludesAppleNotesStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.April, 24, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.April, 24, 15, 2, 0, 0, time.UTC),
		Duration:    2 * time.Minute,
		AppleNotes: &syncjob.AppleNotesStage{
			Stats: applenotes.Stats{
				NotesImported:        8,
				NotesRendered:        6,
				NotesSkipped:         2,
				NotesBlocked:         1,
				AttachmentsIndexed:   4,
				AttachmentsExtracted: 3,
				SummariesCreated:     5,
				Errors:               1,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "Apple Notes", "imported=8 rendered=6", "skipped=2 blocked=1 attachments=4 extracted=3 summarized=5", "1"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestWriteSyncStatsIncludesSafariTabsStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.May, 1, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.May, 1, 15, 1, 0, 0, time.UTC),
		Duration:    time.Minute,
		SafariTabs: &syncjob.SafariTabsStage{
			Stats: safaritabs.Stats{
				DeviceName:    "phone",
				TabsImported:  498,
				TabsCreated:   1,
				TabsUpdated:   2,
				TabsUnchanged: 495,
				TabsRendered:  498,
				TabsSkipped:   2,
				LinksFound:    492,
				Errors:        1,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "Safari Tabs", "created=1 updated=2", "unchanged=495 rendered=498 skipped=2 links=492 device=phone", "1"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestWriteSyncStatsIncludesFeedLevelChangedAndUnchanged(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.May, 9, 18, 42, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.May, 9, 18, 43, 0, 0, time.UTC),
		Duration:    time.Minute,
		Feeds: &syncjob.FeedsStage{
			Stats: feedimport.Stats{
				FeedsChecked:   1,
				FeedsChanged:   0,
				FeedsUnchanged: 1,
				EntriesSeen:    0,
				ItemsCreated:   0,
				ItemsUpdated:   0,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "Feeds", "checked=1 entries=0", "changed=0 unchanged=1 created=0 updated=0"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestWriteSyncStatsIncludesOKFExportStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.June, 14, 18, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.June, 14, 18, 1, 0, 0, time.UTC),
		Duration:    time.Minute,
		OKFExport: &syncjob.OKFExportStage{
			Stats: okf.ExportResult{
				ConceptsWritten: 25000,
				IndexesWritten:  8,
				ItemsWritten:    14000,
				SourcesWritten:  10000,
				EntitiesWritten: 900,
				TopicsWritten:   3,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "OKF Export", "concepts=25000 indexes=8", "items=14000 sources=10000 entities=900 topics=3"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestFormatSyncDurationUsesTwoDecimalSeconds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		duration time.Duration
		want     string
	}{
		{69*time.Millisecond + 980*time.Microsecond + 750*time.Nanosecond, "0.07s"},
		{24*time.Second + 245*time.Millisecond + 314*time.Microsecond, "24.25s"},
		{2 * time.Minute, "120.00s"},
	}
	for _, tc := range cases {
		if got := formatSyncDuration(tc.duration); got != tc.want {
			t.Fatalf("formatSyncDuration(%s) = %q, want %q", tc.duration, got, tc.want)
		}
	}
}

func TestWriteSyncStatsRightAlignsDurationColumn(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.May, 5, 16, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.May, 5, 16, 2, 40, 0, time.UTC),
		Duration:    160 * time.Second,
		Links: &syncjob.LinksStage{
			Duration: 70 * time.Millisecond,
		},
		MediaArchive: &syncjob.MediaArchiveStage{
			Duration: 160 * time.Second,
		},
	}
	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	if !strings.Contains(output, "│ Links         │    0.07s │") {
		t.Fatalf("expected short duration to be right aligned, got %q", output)
	}
	if !strings.Contains(output, "│ Media Archive │  160.00s │") {
		t.Fatalf("expected long duration to define right edge, got %q", output)
	}
}

func TestSyncSummaryRowsSeparateBlockedMediaFromErrors(t *testing.T) {
	t.Parallel()

	rows := syncSummaryRows(syncjob.Stats{
		X: &syncjob.XStage{
			Stats: xapi.Stats{
				Hydrated:        1,
				MediaBlocked:    2,
				MediaErrors:     1,
				MediaDownloaded: 3,
			},
		},
	})
	if len(rows) != 1 {
		t.Fatalf("expected one x summary row, got %#v", rows)
	}
	if !strings.Contains(rows[0][3], "blocked=2") {
		t.Fatalf("expected blocked media in secondary column, got %#v", rows[0])
	}
	if rows[0][4] != "1" {
		t.Fatalf("expected blocked media not to count as active errors, got %#v", rows[0])
	}
}

func TestSyncProgressUIFormatsStageLines(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	ui := newSyncProgressUI(&dst)
	_, _ = fmt.Fprintln(ui, "Sync started at 2026-04-26T21:01:56Z")
	_, _ = fmt.Fprintln(ui, "==> hydrate x")
	_, _ = fmt.Fprintln(ui, "X hydration complete: hydrated=4 missing=0 api_errors=0 media_downloaded=3 media_errors=0 media_blocked=0 rendered=4 (3s)")
	ui.Close()

	output := dst.String()
	for _, value := range []string{"Sync started at", "Hydrating X posts and media", "X hydration complete"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync progress output to contain %q, got %q", value, output)
		}
	}
	if strings.Contains(output, "==>") {
		t.Fatalf("expected raw stage marker to be formatted, got %q", output)
	}
}

func TestSyncProgressUILogWriterPreservesDebugLines(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	ui := newSyncProgressUI(&dst)
	_, _ = fmt.Fprintln(ui, "==> import youtube")
	_, _ = fmt.Fprintln(ui.LogWriter(), `time=2026-04-26T15:03:43.870-06:00 level=DEBUG msg="loading youtube feed"`)
	_, _ = fmt.Fprintln(ui, "YouTube import complete: items_processed=100 sources_summarized=0 errors=0 (3s)")
	ui.Close()

	output := dst.String()
	for _, value := range []string{"Importing YouTube feeds", "DEBUG", "15:03:43.870", "loading youtube feed", "YouTube import complete"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync progress output to contain %q, got %q", value, output)
		}
	}
	if strings.Contains(output, "==>") {
		t.Fatalf("expected raw stage marker to be formatted, got %q", output)
	}
}

func TestSyncProgressUIFormatsStructuredLogFields(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	ui := newSyncProgressUI(&dst)
	_, _ = fmt.Fprintln(ui.LogWriter(), `time=2026-04-26T15:02:01.505-06:00 level=DEBUG msg="source enrichment candidates loaded" sources=1 limit=5000 summarize=true`)
	ui.Close()

	output := dst.String()
	for _, value := range []string{"DEBUG", "15:02:01.505", "source enrichment candidates loaded", "sources=1", "limit=5000", "summarize=true"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected formatted log output to contain %q, got %q", value, output)
		}
	}
	if strings.Contains(output, `msg="source enrichment candidates loaded"`) {
		t.Fatalf("expected msg field to be promoted, got %q", output)
	}
}

func TestLoadConfigRemovesLegacyRootSummaryTempFiles(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "dbrain-summary-legacy.md")
	if err := os.WriteFile(legacy, []byte("legacy summary temp"), 0o644); err != nil {
		t.Fatalf("write legacy temp: %v", err)
	}
	preserved := filepath.Join(root, "keep-me.md")
	if err := os.WriteFile(preserved, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write preserved file: %v", err)
	}

	cfg, err := loadConfig(root)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.TempDir == "" {
		t.Fatal("expected temp dir")
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy temp file to be removed, got err=%v", err)
	}
	if _, err := os.Stat(preserved); err != nil {
		t.Fatalf("expected unrelated root file to remain: %v", err)
	}
}

func TestLoadConfigKeepsSummaryTempFilesUnderTmp(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	tmpFile := filepath.Join(cfg.TempDir, "dbrain-summary-active.md")
	if err := os.WriteFile(tmpFile, []byte("active summary temp"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	if _, err := loadConfig(root); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("expected tmp summary file to remain, got %v", err)
	}
}

func TestCaffeinateStartsByDefaultForLeafCommandWhenAvailable(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	var called int
	startKeepAwake = func(pid int) error {
		called++
		if pid <= 0 {
			t.Fatalf("expected positive pid, got %d", pid)
		}
		return nil
	}
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if called != 1 {
		t.Fatalf("expected caffeinate to start once, got %d", called)
	}
}

func TestNoCaffeinateDisablesAutomaticKeepAwake(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	var called int
	startKeepAwake = func(pid int) error {
		called++
		return nil
	}
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if called != 0 {
		t.Fatalf("expected automatic caffeinate to be disabled, got %d", called)
	}
}

func TestCaffeinateDebugLogsEnabledByDefault(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	startKeepAwake = func(int) error { return nil }
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "keep-awake: started for pid") {
		t.Fatalf("expected keep-awake debug log, got %q", stderr.String())
	}
}

func TestNoDebugSuppressesKeepAwakeDebugLogs(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	startKeepAwake = func(int) error { return nil }
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "keep-awake:") {
		t.Fatalf("expected no keep-awake debug log, got %q", stderr.String())
	}
}

func TestCaffeinateSkipsGroupingHelpCommand(t *testing.T) {
	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	var called int
	startKeepAwake = func(pid int) error {
		called++
		return nil
	}
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"extract"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if called != 0 {
		t.Fatalf("expected caffeinate to be skipped for help command, got %d", called)
	}
}

func TestExtractSourcesCommandOutputsZeroStatsForEmptyBacklog(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "5"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Sources queued: 0", "Sources summarized: 0", "Errors: 0"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected extract sources output to contain %q, got %q", value, output)
		}
	}
}

func TestExtractCommandsDefaultToParallelSourceEnrichment(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()

	for _, args := range [][]string{
		{"extract", "links"},
		{"extract", "sources"},
	} {
		target, _, err := cmd.Find(args)
		if err != nil {
			t.Fatalf("find %v: %v", args, err)
		}
		flag := target.Flags().Lookup("concurrency")
		if flag == nil {
			t.Fatalf("expected %v to define --concurrency", args)
		}
		if flag.DefValue != "4" {
			t.Fatalf("expected %v --concurrency default 4, got %q", args, flag.DefValue)
		}
	}
}

func TestExtractSourcesCommandUsesTargetedSourceLookup(t *testing.T) {
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
		SourceKey:    "x:test-targeted-source",
		SourceType:   "x_bookmark",
		ExternalID:   "test-targeted-source",
		CanonicalURL: "https://x.com/example/status/test-targeted-source",
		Title:        "targeted source item",
		ContentHash:  "item-hash-targeted-source",
		NotePath:     "items/test-targeted-source.md",
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
		SourceKey:     "src:test-targeted-source",
		OriginalURL:   "https://example.com/targeted-source",
		CanonicalURL:  "https://example.com/targeted-source",
		NormalizedURL: "https://example.com/targeted-source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/test-targeted-source.md",
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	oldRunPending := runSourceEnrichPending
	oldRunSourceIDs := runSourceEnrichSourceIDs
	defer func() {
		runSourceEnrichPending = oldRunPending
		runSourceEnrichSourceIDs = oldRunSourceIDs
	}()

	runSourceEnrichPending = func(context.Context, config.Config, *store.Store, sourceenrich.Options) (sourceenrich.Stats, []int64, error) {
		t.Fatal("expected targeted source lookup to bypass backlog run")
		return sourceenrich.Stats{}, nil, nil
	}

	var capturedIDs []int64
	runSourceEnrichSourceIDs = func(_ context.Context, _ config.Config, _ *store.Store, sourceIDs []int64, _ sourceenrich.Options) (sourceenrich.Stats, []int64, error) {
		capturedIDs = append([]int64(nil), sourceIDs...)
		return sourceenrich.Stats{SourcesQueued: len(sourceIDs)}, sourceIDs, nil
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--source", "src:test-targeted-source"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	if len(capturedIDs) != 1 || capturedIDs[0] != link.SourceID {
		t.Fatalf("unexpected targeted source ids: %v want [%d]", capturedIDs, link.SourceID)
	}
	if !strings.Contains(stdout.String(), "Sources queued: 1") {
		t.Fatalf("expected targeted output to report one queued source, got %q", stdout.String())
	}
}

func TestWorkerSourcesCommandOutputsQueueDrainedForEmptyBacklog(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "worker", "sources"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Cycles: 0", "Stopped: queue_drained", "Final source extraction pending: 0", "Final source summary pending: 0", "Duration: "} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected worker output to contain %q, got %q", value, output)
		}
	}
}

func TestTopicMapCommandJSON(t *testing.T) {
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

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-topic-map",
		SourceType:   "x_bookmark",
		ExternalID:   "test-topic-map",
		CanonicalURL: "https://x.com/example/status/test-topic-map",
		Title:        "Agent Memory Post",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory retrieval system",
		ContentHash:  "topic-map-item-hash",
		NotePath:     "items/x/2026/test-topic-map.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-topic-map",
		OriginalURL:   "https://github.com/test/agent-memory",
		CanonicalURL:  "https://github.com/test/agent-memory",
		NormalizedURL: "https://github.com/test/agent-memory",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-topic-map.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "map", "agent memory", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, `"topic": "agent memory"`) || !strings.Contains(output, `"nodes"`) || !strings.Contains(output, `"entities"`) || !strings.Contains(output, `"pivots"`) || !strings.Contains(output, `"synthesis"`) || !strings.Contains(output, `"key": "github-repo:test/agent-memory"`) {
		t.Fatalf("unexpected topic map output: %q", output)
	}
}

func TestTopicGenerateCommandWritesNote(t *testing.T) {
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

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-topic-generate",
		SourceType:   "x_bookmark",
		ExternalID:   "test-topic-generate",
		CanonicalURL: "https://x.com/example/status/test-topic-generate",
		Title:        "Vector Database Post",
		AuthorHandle: "vectordb",
		AuthorName:   "Vector DB",
		Text:         "vector database retrieval indexing",
		ContentHash:  "topic-generate-item-hash",
		NotePath:     "items/x/2026/test-topic-generate.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "generate", "vector database", "--source-type", "x_bookmark"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	notePath := filepath.Join(cfg.VaultDir, "topics", "vector-database.md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read topic note: %v", err)
	}
	if !strings.Contains(string(data), "# vector database") && !strings.Contains(strings.ToLower(string(data)), "# vector database") {
		t.Fatalf("unexpected topic note: %q", string(data))
	}
	if !strings.Contains(string(data), "source_types:") || !strings.Contains(string(data), `"x_bookmark"`) {
		t.Fatalf("expected topic note to persist source types, got %q", string(data))
	}
	if !strings.Contains(string(data), "## What This Topic Is") ||
		!strings.Contains(string(data), "## Main Angles") ||
		!strings.Contains(string(data), "## Key People") ||
		!strings.Contains(string(data), "## Open Questions") ||
		!strings.Contains(string(data), "## Suggested Starting Notes") ||
		!strings.Contains(string(data), "## Why It Matters") ||
		!strings.Contains(string(data), "Vector DB") {
		t.Fatalf("expected topic note to include synthesized topic sections, got %q", string(data))
	}

	indexPath := filepath.Join(cfg.VaultDir, "topics", "index.md")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read topic index: %v", err)
	}
	if !strings.Contains(string(indexData), "[[topics/vector-database|vector database]]") {
		t.Fatalf("unexpected topic index: %q", string(indexData))
	}
}

func TestTopicRefreshCommandUsesStoredFrontmatter(t *testing.T) {
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

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-topic-refresh",
		SourceType:   "x_bookmark",
		ExternalID:   "test-topic-refresh",
		CanonicalURL: "https://x.com/example/status/test-topic-refresh",
		Title:        "Agent Memory Refresh",
		Text:         "agent memory retrieval context",
		ContentHash:  "topic-refresh-item-hash",
		NotePath:     "items/x/2026/test-topic-refresh.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	topicPath := filepath.Join(cfg.VaultDir, "topics", "agent-memory.md")
	if err := os.MkdirAll(filepath.Dir(topicPath), 0o755); err != nil {
		t.Fatalf("mkdir topic dir: %v", err)
	}
	seedNote := `---
brain_topic: "agent memory"
seed_limit: "4"
related_limit: "1"
source_types:
  - "x_bookmark"
tags:
  - "source/topic"
---

# stale
`
	if err := os.WriteFile(topicPath, []byte(seedNote), 0o644); err != nil {
		t.Fatalf("write seed topic note: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "refresh", "agent memory"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Topics refreshed: 1") {
		t.Fatalf("unexpected refresh output: %q", output)
	}

	data, err := os.ReadFile(topicPath)
	if err != nil {
		t.Fatalf("read refreshed topic note: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "# agent memory") {
		t.Fatalf("expected refreshed topic heading, got %q", body)
	}
	if !strings.Contains(body, `seed_limit: "4"`) || !strings.Contains(body, `related_limit: "1"`) {
		t.Fatalf("expected stored limits to be preserved, got %q", body)
	}
	if !strings.Contains(body, `"x_bookmark"`) {
		t.Fatalf("expected stored source types to be preserved, got %q", body)
	}
}

func TestTopicIndexCommandWritesIndex(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	topicsDir := filepath.Join(cfg.VaultDir, "topics")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatalf("mkdir topics dir: %v", err)
	}
	first := `---
brain_topic: "agent memory"
seed_limit: "6"
related_limit: "2"
source_types:
  - "github"
---
`
	second := `---
brain_topic: "vector database"
seed_limit: "5"
related_limit: "1"
source_types: []
---
`
	if err := os.WriteFile(filepath.Join(topicsDir, "agent-memory.md"), []byte(first), 0o644); err != nil {
		t.Fatalf("write first topic note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(topicsDir, "vector-database.md"), []byte(second), 0o644); err != nil {
		t.Fatalf("write second topic note: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "index"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	indexPath := filepath.Join(cfg.VaultDir, "topics", "index.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read topic index: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "[[topics/agent-memory|agent memory]]") || !strings.Contains(body, "[[topics/vector-database|vector database]]") {
		t.Fatalf("unexpected topic index body: %q", body)
	}
}

func TestEntityMapCommandJSON(t *testing.T) {
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

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-entity-map",
		SourceType:   "x_bookmark",
		ExternalID:   "test-entity-map",
		CanonicalURL: "https://x.com/example/status/test-entity-map",
		Title:        "Entity map item",
		AuthorHandle: "entityauthor",
		AuthorName:   "Entity Author",
		Text:         "entity map body",
		ContentHash:  "entity-map-item-hash",
		NotePath:     "items/x/2026/test-entity-map.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "entity", "map", "entityauthor", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, `"key": "x-author:entityauthor"`) || !strings.Contains(output, `"kind": "person"`) {
		t.Fatalf("unexpected entity map output: %q", output)
	}
}

func TestEntityGenerateCommandWritesNote(t *testing.T) {
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

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:test-entity-generate",
		SourceType:   "github_star",
		ExternalID:   "test-entity-generate",
		CanonicalURL: "https://github.com/example/project",
		Title:        "Entity generate item",
		ContentHash:  "entity-generate-item-hash",
		NotePath:     "items/github/test-entity-generate.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-entity-generate",
		OriginalURL:   "https://github.com/example/project",
		CanonicalURL:  "https://github.com/example/project",
		NormalizedURL: "https://github.com/example/project",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/example-project.md",
	}); err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "entity", "generate", "example/project", "--kind", "project"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	notePath := filepath.Join(cfg.VaultDir, "entities", "project", "github-repo-example-project.md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read entity note: %v", err)
	}
	if !strings.Contains(string(data), "# example/project") {
		t.Fatalf("unexpected entity note: %q", string(data))
	}

	indexPath := filepath.Join(cfg.VaultDir, "entities", "index.md")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read entity index: %v", err)
	}
	if !strings.Contains(string(indexData), "[[entities/project/github-repo-example-project|example/project]]") {
		t.Fatalf("unexpected entity index: %q", string(indexData))
	}
}

func TestEntityIndexCommandWritesAllEntityNotes(t *testing.T) {
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

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-entity-index",
		SourceType:   "x_bookmark",
		ExternalID:   "test-entity-index",
		CanonicalURL: "https://x.com/example/status/test-entity-index",
		Title:        "Entity index item",
		AuthorHandle: "entityindexer",
		AuthorName:   "Entity Indexer",
		Text:         "entity index body",
		ContentHash:  "entity-index-item-hash",
		NotePath:     "items/x/2026/test-entity-index.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-entity-index-site",
		OriginalURL:   "https://example.com/project",
		CanonicalURL:  "https://example.com/project",
		NormalizedURL: "https://example.com/project",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-project.md",
	}); err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "entity", "index"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	for _, path := range []string{
		filepath.Join(cfg.VaultDir, "entities", "person", "x-author-entityindexer.md"),
		filepath.Join(cfg.VaultDir, "entities", "site", "site-example-com.md"),
		filepath.Join(cfg.VaultDir, "entities", "index.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected entity artifact at %s: %v", path, err)
		}
	}
}

func TestRepairNotesCommandRebuildsMissingNotes(t *testing.T) {
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
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-repair",
		SourceType:   "x_bookmark",
		ExternalID:   "test-repair",
		CanonicalURL: "https://x.com/example/status/test-repair",
		Title:        "Repair test item",
		ContentHash:  "repair-hash",
		NotePath:     "items/x/2026/test-repair.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:repair-source",
		OriginalURL:   "https://example.com/repair",
		CanonicalURL:  "https://example.com/repair",
		NormalizedURL: "https://example.com/repair",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/repair-source.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}

	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/repair",
		FinalURL:     "https://example.com/repair",
		Title:        "Repair source",
		Content:      "source body",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "repair-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "repair", "notes"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	for _, path := range []string{
		filepath.Join(cfg.VaultDir, "items/x/2026/test-repair.md"),
		filepath.Join(cfg.VaultDir, "sources/web/repair-source.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected repaired note at %s: %v", path, err)
		}
	}

	output := stdout.String()
	for _, value := range []string{"Items written: 1", "Sources written: 1", "Errors: 0"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected repair output to contain %q, got %q", value, output)
		}
	}
}

func TestResearchCommandOutputsResearchPack(t *testing.T) {
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
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/retrieval",
		SourceType:   "github_star",
		ExternalID:   "test/retrieval",
		CanonicalURL: "https://github.com/test/retrieval",
		Title:        "retrieval repo",
		ContentHash:  "hash-retrieval-item",
		NotePath:     "items/github/test-retrieval.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-retrieval",
		OriginalURL:   "https://github.com/test/retrieval",
		CanonicalURL:  "https://github.com/test/retrieval",
		NormalizedURL: "https://github.com/test/retrieval",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-retrieval.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/retrieval",
		FinalURL:     "https://github.com/test/retrieval",
		Title:        "retrieval repo",
		Content:      "This tool handles retrieval for internal knowledge bases.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "hash-retrieval-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "Retrieval-oriented knowledge base tooling.",
		RawJSON:       `{"summary":"Retrieval-oriented knowledge base tooling."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "test retrieval repo", "--retrieval-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Research pack: test retrieval repo", "Mode: evidence_only", "Retrieved evidence:", "[src:test-retrieval] retrieval repo", "summary: Retrieval-oriented knowledge base tooling.", "entity_matches:"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected research output to contain %q, got %q", value, output)
		}
	}
	traceDir := assertResearchTraceDir(t, cfg, output)
	assertTraceFile(t, traceDir, "run.md")
	assertTraceFile(t, traceDir, "run.json")
	assertTraceFile(t, traceDir, ".complete")
	if _, err := os.Stat(filepath.Join(traceDir, "synthesis-input.md")); !os.IsNotExist(err) {
		t.Fatalf("retrieval-only trace should not write synthesis input, err=%v", err)
	}
}

func TestResearchCommandSynthesizesByDefault(t *testing.T) {
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
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "item:test-synthesis",
		SourceType:   "x_bookmark",
		ExternalID:   "test-synthesis",
		CanonicalURL: "https://x.com/example/status/test-synthesis",
		Title:        "Synthesis item",
		Text:         "synthesis retrieval local answer",
		ContentHash:  "hash-synthesis-item",
		NotePath:     "items/x/test-synthesis.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-synthesis",
		OriginalURL:   "https://example.com/synthesis",
		CanonicalURL:  "https://example.com/synthesis",
		NormalizedURL: "https://example.com/synthesis",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-synthesis.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/synthesis",
		FinalURL:     "https://example.com/synthesis",
		Title:        "Synthesis source",
		Content:      "synthesis retrieval source body",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "hash-synthesis-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "Synthesis retrieval should produce a cited local answer.",
		RawJSON:       `{"summary":"Synthesis retrieval should produce a cited local answer."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Synthesis retrieval produces a cited answer [src:test-synthesis]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "synthesis retrieval", "--model", "ollama/qwen-test", "--synthesis-timeout", "5s"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Answer status: ok", "Model: ollama/qwen-test", "Answer:", "Synthesis retrieval produces a cited answer [src:test-synthesis].", "Citations:", "Research pack:"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected synthesized research output to contain %q, got %q", value, output)
		}
	}
	traceDir := assertResearchTraceDir(t, cfg, output)
	assertTraceFile(t, traceDir, "run.md")
	assertTraceFile(t, traceDir, "run.json")
	assertTraceFile(t, traceDir, "planner-input.md")
	assertTraceFile(t, traceDir, "planner-output.json")
	assertTraceFile(t, traceDir, "synthesis-input.md")
	assertTraceFile(t, traceDir, ".complete")
	traceJSON, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{`"surface": "cli"`, `"stop_reason": "enough_evidence"`, `"synthesis"`, `"synthesis_input_path": "synthesis-input.md"`} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected trace json to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func TestResearchCommandNoTraceSuppressesTraceWriting(t *testing.T) {
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

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "nothing here", "--retrieval-only", "--no-trace"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "Trace:") {
		t.Fatalf("no-trace output should not include trace path: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "research-runs")); !os.IsNotExist(err) {
		t.Fatalf("expected no research-runs directory, err=%v", err)
	}
}

func TestResearchCommandRunnerPersistsNoEvidenceTrace(t *testing.T) {
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
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "nothing here", "--runner", "--no-planner"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Research pack: nothing here", "Stop reason: no_evidence", "Trace: research-runs/"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected runner output to contain %q, got %q", value, output)
		}
	}
	traceDir := assertResearchTraceDir(t, cfg, output)
	assertTraceFile(t, traceDir, "run.md")
	assertTraceFile(t, traceDir, "run.json")
	assertTraceFile(t, traceDir, "synthesis-input.md")
	assertTraceFile(t, traceDir, ".complete")
	traceJSON, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{`"surface": "cli"`, `"stop_reason": "no_evidence"`, `"prepared_synthesis"`, `"synthesis_input_path": "synthesis-input.md"`} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected runner trace json to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func assertResearchTraceDir(t *testing.T, cfg config.Config, output string) string {
	t.Helper()
	marker := "Trace: "
	idx := strings.LastIndex(output, marker)
	if idx < 0 {
		t.Fatalf("expected research output to include trace path, got %q", output)
	}
	line := strings.TrimSpace(output[idx+len(marker):])
	if newline := strings.IndexByte(line, '\n'); newline >= 0 {
		line = strings.TrimSpace(line[:newline])
	}
	if !strings.HasPrefix(line, "research-runs/") {
		t.Fatalf("trace path should be relative under research-runs, got %q", line)
	}
	traceDir := filepath.Join(cfg.DataDir, filepath.FromSlash(line))
	if _, err := os.Stat(traceDir); err != nil {
		t.Fatalf("expected trace dir %s: %v", traceDir, err)
	}
	return traceDir
}

func assertTraceFile(t *testing.T, traceDir string, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(traceDir, name)); err != nil {
		t.Fatalf("expected trace file %s: %v", name, err)
	}
}

func TestResearchCommandSourceTypeFilterLimitsEvidence(t *testing.T) {
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
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/filter",
		SourceType:   "github_star",
		ExternalID:   "test/filter",
		CanonicalURL: "https://github.com/test/filter",
		Title:        "filter repo",
		ContentHash:  "hash-filter-item",
		NotePath:     "items/github/test-filter.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-filter",
		OriginalURL:   "https://github.com/test/filter",
		CanonicalURL:  "https://github.com/test/filter",
		NormalizedURL: "https://github.com/test/filter",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-filter.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/filter",
		FinalURL:     "https://github.com/test/filter",
		Title:        "filter repo",
		Content:      "This repo helps filter search results.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "hash-filter-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "show me github repos search results", "--source-type", "github", "--retrieval-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "source_type: github") {
		t.Fatalf("expected github source type in output, got %q", output)
	}
	if strings.Contains(output, "source_type: x_bookmark") {
		t.Fatalf("did not expect x evidence in filtered output, got %q", output)
	}
	if strings.Contains(output, "entity_matches:") {
		t.Fatalf("did not expect generic github query to create entity matches, got %q", output)
	}
}

func TestResearchCommandIncludeRelatedAddsLinkedEvidence(t *testing.T) {
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
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-related-item",
		SourceType:   "x_bookmark",
		ExternalID:   "test-related-item",
		CanonicalURL: "https://x.com/example/status/test-related-item",
		Title:        "Parent item",
		Text:         "special retrieval phrase for related evidence",
		ContentHash:  "hash-related-item",
		NotePath:     "items/x/2026/test-related-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-related-source",
		OriginalURL:   "https://example.com/related",
		CanonicalURL:  "https://example.com/related",
		NormalizedURL: "https://example.com/related",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-related-source.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/related",
		FinalURL:     "https://example.com/related",
		Title:        "Related source",
		Content:      "related source body",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "hash-related-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "Linked source summary for the parent item.",
		RawJSON:       `{"summary":"Linked source summary for the parent item."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "special retrieval phrase", "--include-related", "--related-limit", "1", "--limit", "2", "--retrieval-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"[x:test-related-item] Parent item", "[src:test-related-source] Related source", "relationship: linked source (x:test-related-item)"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected related research output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsSourcesCommandOutputsSummaryStatusCounts(t *testing.T) {
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
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/one",
		SourceType:   "github_star",
		ExternalID:   "test/one",
		CanonicalURL: "https://github.com/test/one",
		Title:        "test/one",
		ContentHash:  "hash-one",
		NotePath:     "items/github/test-one.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item one: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-one",
		OriginalURL:   "https://github.com/test/one",
		CanonicalURL:  "https://github.com/test/one",
		NormalizedURL: "https://github.com/test/one",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-one.md",
	})
	if err != nil {
		t.Fatalf("source link one: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/one",
		FinalURL:     "https://github.com/test/one",
		Title:        "test/one",
		Content:      "README one",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "source-hash-one"); err != nil {
		t.Fatalf("save extract one: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "summary one",
		RawJSON:       `{"summary":"summary one"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary one: %v", err)
	}

	item, err = st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/two",
		SourceType:   "github_star",
		ExternalID:   "test/two",
		CanonicalURL: "https://github.com/test/two",
		Title:        "test/two",
		ContentHash:  "hash-two",
		NotePath:     "items/github/test-two.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item two: %v", err)
	}
	link, err = st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-two",
		OriginalURL:   "https://github.com/test/two",
		CanonicalURL:  "https://github.com/test/two",
		NormalizedURL: "https://github.com/test/two",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-two.md",
	})
	if err != nil {
		t.Fatalf("source link two: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/two",
		FinalURL:     "https://github.com/test/two",
		Title:        "test/two",
		Content:      "README two",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "source-hash-two"); err != nil {
		t.Fatalf("save extract two: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "sources",
		"--source-type", "github",
		"--extract-tool", "github-api",
		"--group-by", "summary-status",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"ok: 1", "pending: 1", "Total: 2"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected stats output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsActivityCommandOutputsRecentWrites(t *testing.T) {
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
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:activity/test",
		SourceType:   "github_star",
		ExternalID:   "activity/test",
		CanonicalURL: "https://github.com/activity/test",
		Title:        "activity/test",
		ContentHash:  "activity-hash",
		NotePath:     "items/github/activity-test.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:activity-test",
		OriginalURL:   "https://github.com/activity/test",
		CanonicalURL:  "https://github.com/activity/test",
		NormalizedURL: "https://github.com/activity/test",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/activity-test.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/activity/test",
		FinalURL:     "https://github.com/activity/test",
		Title:        "activity/test",
		Content:      "README",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "activity-source-hash"); err != nil {
		t.Fatalf("save extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "summary",
		RawJSON:       `{"summary":"summary"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "activity",
		"--window", "15m",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{
		"Latest item write:",
		"Latest source write:",
		"Latest source summary:",
		"Items updated in window: 1",
		"Sources updated in window: 1",
		"Sources summarized in window: 1",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected activity output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsBacklogCommandOutputsPendingQueues(t *testing.T) {
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
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:backlog",
		SourceType:   "x_bookmark",
		ExternalID:   "backlog",
		CanonicalURL: "https://x.com/example/status/backlog",
		Title:        "backlog",
		ContentHash:  "backlog-hash",
		LinksJSON:    `["https://example.com/post"]`,
		NotePath:     "items/x/backlog.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), item.ItemID, model.XHydration{
		Status: "error",
		Error:  "boom",
	}); err != nil {
		t.Fatalf("save hydration error: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "backlog",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{
		"Queue drained: no",
		"X hydration pending: 1",
		"Link discovery pending: 1",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected backlog output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsPipelineCommandJSON(t *testing.T) {
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
	hydratedItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:pipeline-hydrated",
		SourceType:   "x_bookmark",
		ExternalID:   "pipeline-hydrated",
		CanonicalURL: "https://x.com/example/status/pipeline-hydrated",
		Title:        "hydrated",
		ContentHash:  "pipeline-hydrated-hash",
		NotePath:     "items/x/pipeline-hydrated.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert hydrated x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), hydratedItem.ItemID, model.XHydration{
		Status:    "ok_graphql",
		FullText:  "hydrated text",
		FetchedAt: now,
	}); err != nil {
		t.Fatalf("save hydrated x item: %v", err)
	}

	videoPendingItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:pipeline-video-pending",
		SourceType:   "x_bookmark",
		ExternalID:   "pipeline-video-pending",
		CanonicalURL: "https://x.com/example/status/pipeline-video-pending",
		Title:        "video pending",
		ContentHash:  "pipeline-video-pending-hash",
		NotePath:     "items/x/pipeline-video-pending.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert pending x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), videoPendingItem.ItemID, model.XHydration{
		Status:    "error",
		Error:     "boom",
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"https://cdn.example.com/video.mp4","expanded_url":"https://x.com/example/status/pipeline-video-pending/video/1","width":1280,"height":720}]}}`,
		FetchedAt: now,
	}); err != nil {
		t.Fatalf("save pending x item hydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), videoPendingItem.ItemID)
	if err != nil {
		t.Fatalf("list item media refs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %d", len(refs))
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		LocalPath:    "media/x/video/test.mp4",
		ContentHash:  "video-download-hash",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("save media download: %v", err)
	}

	webItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "manual:pipeline-web",
		SourceType:   "manual_link",
		ExternalID:   "pipeline-web",
		CanonicalURL: "https://example.com/items/pipeline-web",
		Title:        "web source item",
		ContentHash:  "pipeline-web-item-hash",
		NotePath:     "items/manual/pipeline-web.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert web item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), webItem.ItemID, model.SourceCandidate{
		SourceKey:     "src:pipeline-web",
		OriginalURL:   "https://example.com/pipeline-web",
		CanonicalURL:  "https://example.com/pipeline-web",
		NormalizedURL: "https://example.com/pipeline-web",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/pipeline-web.md",
	}); err != nil {
		t.Fatalf("upsert web source: %v", err)
	}

	youtubeItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "youtube:pipeline-current",
		SourceType:   "youtube_watch_later",
		ExternalID:   "pipeline-current",
		CanonicalURL: "https://www.youtube.com/watch?v=pipeline-current",
		Title:        "youtube source item",
		ContentHash:  "pipeline-youtube-item-hash",
		NotePath:     "items/youtube/pipeline-current.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert youtube item: %v", err)
	}
	youtubeLink, err := st.UpsertSourceLink(context.Background(), youtubeItem.ItemID, model.SourceCandidate{
		SourceKey:     "src:pipeline-youtube",
		OriginalURL:   "https://www.youtube.com/watch?v=pipeline-current",
		CanonicalURL:  "https://www.youtube.com/watch?v=pipeline-current",
		NormalizedURL: "https://www.youtube.com/watch?v=pipeline-current",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      "sources/youtube/pipeline-current.md",
	})
	if err != nil {
		t.Fatalf("upsert youtube source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), youtubeLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://www.youtube.com/watch?v=pipeline-current",
		FinalURL:     "https://www.youtube.com/watch?v=pipeline-current",
		Title:        "pipeline current video",
		Content:      "youtube transcript",
		Status:       "ok",
		Tool:         "youtube-test",
		ToolVersion:  "1.0.0",
		FetchedAt:    now,
	}, "pipeline-youtube-source-hash"); err != nil {
		t.Fatalf("save youtube extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), youtubeLink.SourceID, model.SummaryResult{
		Text:          "youtube summary",
		Model:         "ollama/qwen3.6:35b",
		PromptVersion: sourceenrich.SummaryPromptVersion,
		Status:        "ok",
		Tool:          "ollama-direct",
		ToolVersion:   "ollama-direct-v1",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save youtube summary: %v", err)
	}

	xArticleItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:pipeline-article",
		SourceType:   "x_bookmark",
		ExternalID:   "pipeline-article",
		CanonicalURL: "https://x.com/example/status/pipeline-article",
		Title:        "x article source item",
		ContentHash:  "pipeline-x-article-item-hash",
		NotePath:     "items/x/pipeline-article.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert x article item: %v", err)
	}
	xArticleLink, err := st.UpsertSourceLink(context.Background(), xArticleItem.ItemID, model.SourceCandidate{
		SourceKey:     "src:pipeline-x-article",
		OriginalURL:   "https://x.com/example/article/pipeline",
		CanonicalURL:  "https://x.com/example/article/pipeline",
		NormalizedURL: "https://x.com/example/article/pipeline",
		SourceType:    "x_article",
		Domain:        "x.com",
		NotePath:      "sources/x_article/pipeline.md",
	})
	if err != nil {
		t.Fatalf("upsert x article source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), xArticleLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://x.com/example/article/pipeline",
		FinalURL:     "https://x.com/example/article/pipeline",
		Title:        "pipeline x article",
		Content:      "x article content",
		Status:       "ok",
		Tool:         "x-hydration",
		ToolVersion:  "x-hydration-test",
		FetchedAt:    now,
	}, "pipeline-x-article-source-hash"); err != nil {
		t.Fatalf("save x article extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), xArticleLink.SourceID, model.SummaryResult{
		Text:          "x article summary",
		Model:         "openrouter/qwen/qwen3.5-27b",
		PromptVersion: sourceenrich.SummaryPromptVersion,
		Status:        "ok",
		Tool:          "openrouter-direct",
		ToolVersion:   "openrouter-direct-v1",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save x article summary: %v", err)
	}

	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "safari-tab:pipeline-current",
		SourceType:   "safari_tab",
		ExternalID:   "pipeline-current",
		CanonicalURL: "https://example.com/safari-tab",
		Title:        "safari tab item",
		Text:         "Safari tab captured from iCloud Tabs.",
		ContentHash:  "pipeline-safari-tab-hash",
		NotePath:     "items/safari-tabs/2026/pipeline-current.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert safari tab item: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "pipeline",
		"--model", "ollama/qwen3.6:35b",
		"--json",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	var stats store.PipelineStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal pipeline stats: %v\n%s", err, stdout.String())
	}

	assertPipelineRowCounts(t, stats.Hydration, "x_bookmark", 3, 1, 2, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "web", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "x_article", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "safari_tab", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "web", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Transcription, "x_media_transcript", 1, 0, 1, 0, 0)

	cmd = NewRootCommand()
	stdout.Reset()
	stderr.Reset()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "pipeline",
		"--json",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext default pipeline: %v (stderr=%q)", err, stderr.String())
	}

	stats = store.PipelineStats{}
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal default pipeline stats: %v\n%s", err, stdout.String())
	}

	assertPipelineRowCounts(t, stats.Summary, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 1, 0, 0, 0)
}

func assertPipelineRowCounts(t *testing.T, rows []store.PipelineStageRow, kind string, total int, current int, pending int, blocked int, failed int) {
	t.Helper()

	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		if row.Total != total || row.Current != current || row.Pending != pending || row.Blocked != blocked || row.Failed != failed {
			t.Fatalf("unexpected pipeline row for %s: %+v", kind, row)
		}
		return
	}
	t.Fatalf("missing pipeline row for %s in %+v", kind, rows)
}

func TestCLIFlagsDefaultToCodex(t *testing.T) {
	cmd := NewRootCommand()

	for _, path := range [][]string{
		{"extract", "links"},
		{"extract", "sources"},
		{"import", "github", "stars"},
		{"import", "youtube"},
		{"link", "add"},
		{"sync", "all"},
		{"worker", "sources"},
	} {
		target, _, err := cmd.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		flag := target.Flags().Lookup("cli")
		if flag == nil {
			t.Fatalf("expected --cli flag on %v", path)
		}
		if flag.DefValue != defaultCLIProvider {
			t.Fatalf("expected default cli %q for %v, got %q", defaultCLIProvider, path, flag.DefValue)
		}
	}
}
