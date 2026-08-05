package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

func TestSyncFamilyStoreCloseFailureEmitsTerminalErrorAndClosesMetrics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: semantic-store-close-metrics.jsonl
  detail: stage
  strict: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRunSyncAll := runSyncAll
	oldCloseSyncStore := closeSyncStore
	t.Cleanup(func() {
		runSyncAll = oldRunSyncAll
		closeSyncStore = oldCloseSyncStore
	})
	closeFailure := errors.New("close source store: injected failure")
	closeCalls := 0
	closeSyncStore = func(st *store.Store) error {
		closeCalls++
		if closeCalls == 1 {
			return closeFailure
		}
		return st.Close()
	}
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, options syncjob.Options) (syncjob.Stats, error) {
		if !options.ParentOwnsRunCompletion {
			t.Fatal("source pipeline retained terminal completion ownership")
		}
		if err := options.Metrics.Emit(metrics.Event{"event": "sync.run.started", "status": "started"}); err != nil {
			t.Fatal(err)
		}
		if err := options.Metrics.Emit(metrics.Event{"event": "sync.pipeline.completed", "status": "ok"}); err != nil {
			t.Fatal(err)
		}
		return syncSemanticTestStats(), nil
	}

	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, semanticRefreshDeps{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))
	err := cmd.ExecuteContext(t.Context())
	if !errors.Is(err, closeFailure) {
		t.Fatalf("ExecuteContext error = %v, want injected close failure", err)
	}

	events := readAppMetricEvents(t, filepath.Join(root, "logs", "semantic-store-close-metrics.jsonl"))
	wantNames := []string{"sync.run.started", "sync.pipeline.completed", "sync.run.completed"}
	if len(events) != len(wantNames) {
		t.Fatalf("events = %#v", events)
	}
	for index, want := range wantNames {
		if events[index]["event"] != want {
			t.Fatalf("event %d = %#v, want %q", index, events[index], want)
		}
	}
	if events[2]["status"] != "error" {
		t.Fatalf("terminal event = %#v, want error status", events[2])
	}
}

func TestSyncFamilyMetricsRemainOpenThroughSemanticAndCompleteOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
metrics:
  enabled: true
  path: semantic-sync-metrics.jsonl
  detail: stage
  strict: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = oldRunSyncAll })
	runSyncAll = func(_ context.Context, _ config.Config, _ *store.Store, options syncjob.Options) (syncjob.Stats, error) {
		if !options.ParentOwnsRunCompletion {
			t.Fatal("source pipeline retained terminal completion ownership")
		}
		if err := options.Metrics.Emit(metrics.Event{"event": "sync.run.started", "status": "started"}); err != nil {
			t.Fatal(err)
		}
		if err := options.Metrics.Emit(metrics.Event{"event": "sync.pipeline.completed", "status": "ok"}); err != nil {
			t.Fatal(err)
		}
		return syncSemanticTestStats(), nil
	}

	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(semanticconfig.ModeOff), nil
		},
		capability: semanticRefreshReadyCapability,
		now: func() time.Time {
			return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
		},
	}
	cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs(syncSemanticTestArgs(true))
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	events := readAppMetricEvents(t, filepath.Join(root, "logs", "semantic-sync-metrics.jsonl"))
	wantNames := []string{
		"sync.run.started",
		"sync.pipeline.completed",
		"semantic.refresh.started",
		"semantic.refresh.completed",
		"sync.run.completed",
	}
	if len(events) != len(wantNames) {
		t.Fatalf("events = %#v", events)
	}
	for index, want := range wantNames {
		if events[index]["event"] != want {
			t.Fatalf("event %d = %#v, want %q", index, events[index], want)
		}
	}
}
