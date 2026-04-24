package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/syncjob"
	"dbrain/internal/xmediatranscribe"
)

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
	for _, value := range []string{"import", "sync", "entity", "topic", "worker", "extract", "hydrate", "transcribe", "repair", "serve", "stats", "ask", "search", "get"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
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
	for _, value := range []string{"ft", "youtube"} {
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
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	if !strings.Contains(output, "X Media: items_processed=10 items_updated=6 items_skipped=4 media_transcribed=6 errors=1") {
		t.Fatalf("expected x media sync stats line, got %q", output)
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

func TestAskCommandRetrieveOnlyOutputsEvidence(t *testing.T) {
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
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "ask", "test retrieval repo", "--retrieve-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Retrieved evidence:", "[src:test-retrieval] retrieval repo", "summary: Retrieval-oriented knowledge base tooling.", "entity_matches:"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected ask output to contain %q, got %q", value, output)
		}
	}
}

func TestAskCommandSynthesizesAnswer(t *testing.T) {
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
		SourceKey:    "x:test-ask-answer",
		SourceType:   "x_bookmark",
		ExternalID:   "test-ask-answer",
		CanonicalURL: "https://x.com/example/status/test-ask-answer",
		Title:        "Kubernetes validation tools",
		Text:         "kubeval validates kubernetes YAML manifests",
		ContentHash:  "hash-ask-answer-item",
		NotePath:     "items/x/2026/test-ask-answer.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-ask-answer",
		OriginalURL:   "https://kubeval.com/",
		CanonicalURL:  "https://kubeval.com/",
		NormalizedURL: "https://kubeval.com/",
		SourceType:    "web",
		Domain:        "kubeval.com",
		NotePath:      "sources/web/test-ask-answer.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://kubeval.com/",
		FinalURL:     "https://kubeval.com/",
		Title:        "kubeval",
		Content:      "kubeval validates Kubernetes manifests and configuration files against schemas.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "hash-ask-answer-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "kubeval validates Kubernetes manifests against upstream schemas.",
		RawJSON:       `{"summary":"kubeval validates Kubernetes manifests against upstream schemas."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	installAskFakeSummarize(t, root)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "ask", "What validates Kubernetes manifests?", "--cli", "codex", "--timeout", "5s"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"kubeval is a Kubernetes manifest validator [src:test-ask-answer].", "Retrieved evidence:", "[src:test-ask-answer] kubeval"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected ask output to contain %q, got %q", value, output)
		}
	}
}

func TestAskCommandSourceTypeFilterLimitsEvidence(t *testing.T) {
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
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "ask", "show me github repos search results", "--retrieve-only", "--source-type", "github"})

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

func TestAskCommandIncludeRelatedAddsLinkedEvidence(t *testing.T) {
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
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "ask", "special retrieval phrase", "--retrieve-only", "--include-related", "--related-limit", "1", "--limit", "2"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"[x:test-related-item] Parent item", "[src:test-related-source] Related source", "relationship: linked source (x:test-related-item)"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected related ask output to contain %q, got %q", value, output)
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
		SourceKey:    "ft:pipeline-web",
		SourceType:   "ft_bookmark",
		ExternalID:   "pipeline-web",
		CanonicalURL: "https://example.com/items/pipeline-web",
		Title:        "web source item",
		ContentHash:  "pipeline-web-item-hash",
		NotePath:     "items/ft/pipeline-web.md",
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
	assertPipelineRowCounts(t, stats.Summary, "web", 1, 0, 0, 1, 0)
	assertPipelineRowCounts(t, stats.Summary, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Transcription, "x_media_transcript", 1, 0, 1, 0, 0)
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

func installAskFakeSummarize(t *testing.T, root string) {
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
if [ "$cli" != "codex" ]; then
  echo "expected codex cli provider, got $cli" >&2
  exit 1
fi
if [ ! -f "$last" ]; then
  echo "expected local ask prompt file" >&2
  exit 1
fi
input="$(cat "$last")"
case "$input" in
  *"What validates Kubernetes manifests?"* ) ;;
  *)
    echo "expected question in ask prompt input" >&2
    exit 1
    ;;
esac
case "$input" in
  *"src:test-ask-answer"* ) ;;
  *)
    echo "expected source key in ask prompt input" >&2
    exit 1
    ;;
esac
printf '%s\n' '{"input":{"model":"cli/test/ask"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"context"},"summary":"kubeval is a Kubernetes manifest validator [src:test-ask-answer].\n\nSources\n- [src:test-ask-answer] kubeval — /vault/sources/web/test-ask-answer.md"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCLIFlagsDefaultToCodex(t *testing.T) {
	cmd := NewRootCommand()

	for _, path := range [][]string{
		{"ask"},
		{"extract", "links"},
		{"extract", "sources"},
		{"import", "github", "stars"},
		{"import", "youtube"},
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
