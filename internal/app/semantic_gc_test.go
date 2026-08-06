package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/semanticgc"
	"github.com/darron/dbrain/internal/store"
)

func TestSemanticGCCommandIsDryRunByDefault(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	cfg := config.Config{RootDir: rootDir, DBPath: filepath.Join(rootDir, "brain.db"), CacheDir: filepath.Join(rootDir, "cache")}
	initialized, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := initialized.Close(); err != nil {
		t.Fatal(err)
	}
	var got semanticgc.Options
	deps := defaultSemanticDeps()
	deps.loadWriteConfig = func(string, ...string) (config.Config, error) { return cfg, nil }
	deps.openReadOnly = store.OpenReadOnly
	deps.openWritable = func(string, string) (*store.Store, error) {
		t.Fatal("dry-run opened a writable store")
		return nil, nil
	}
	deps.runGC = func(_ context.Context, _ semanticgc.Catalog, cacheDir, databaseID string, opts semanticgc.Options) (semanticgc.Result, error) {
		got = opts
		if cacheDir != cfg.CacheDir || databaseID == "" {
			t.Fatalf("runGC cache=%q database=%q", cacheDir, databaseID)
		}
		return semanticgc.Result{Catalog: store.RetrievalSemanticGCPlan{PrunableMemberRows: 7}, PrunableBytes: 11}, nil
	}
	cmd := newSemanticGCCommand(&rootOptions{}, deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got.Apply || got.Vacuum || got.GracePeriod != defaultSemanticGCGracePeriod || got.RetainPublished != defaultSemanticGCRetainPublished || got.LockTimeout != 0 {
		t.Fatalf("default options=%+v", got)
	}
	if !strings.Contains(out.String(), "no changes made") || !strings.Contains(out.String(), "bytes=11") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestSemanticGCCommandApplyJSONForwardsExplicitSafetyFlags(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	cfg := config.Config{RootDir: rootDir, DBPath: filepath.Join(rootDir, "brain.db"), CacheDir: filepath.Join(rootDir, "cache")}
	initialized, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := initialized.Close(); err != nil {
		t.Fatal(err)
	}
	deps := defaultSemanticDeps()
	deps.loadWriteConfig = func(string, ...string) (config.Config, error) { return cfg, nil }
	deps.runGC = func(_ context.Context, _ semanticgc.Catalog, _, _ string, opts semanticgc.Options) (semanticgc.Result, error) {
		if !opts.Apply || !opts.Vacuum || opts.GracePeriod.String() != "15m0s" || opts.RetainPublished != 3 {
			t.Fatalf("explicit options=%+v", opts)
		}
		return semanticgc.Result{Applied: true, Vacuumed: true}, nil
	}
	cmd := newSemanticGCCommand(&rootOptions{}, deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--apply", "--vacuum", "--grace-period=15m", "--retain-generations=3", "--json"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	var result semanticgc.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON output %q: %v", out.String(), err)
	}
	if !result.Applied || !result.Vacuumed {
		t.Fatalf("JSON result=%+v", result)
	}
}

func TestSemanticGCCommandRejectsVacuumWithoutApplyBeforeOpeningStore(t *testing.T) {
	t.Parallel()
	opened := false
	deps := defaultSemanticDeps()
	deps.openWritable = func(string, string) (*store.Store, error) {
		opened = true
		return nil, nil
	}
	cmd := newSemanticGCCommand(&rootOptions{}, deps)
	cmd.SetArgs([]string{"--vacuum"})
	if err := cmd.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "requires --apply") {
		t.Fatalf("Execute error=%v", err)
	}
	if opened {
		t.Fatal("invalid flags opened the store")
	}
}
