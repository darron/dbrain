package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/store"
)

func TestOKFExportAndValidateCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Date(2026, 6, 14, 18, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:cli-okf",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "CLI OKF item",
		Text:         "CLI raw item text.",
		SummaryText:  "CLI item summary.",
		ContentHash:  "cli-item-hash",
		LinksJSON:    `["https://example.com/cli"]`,
		NotePath:     "items/x/2026/123.md",
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		OriginalURL:   "https://example.com/cli",
		CanonicalURL:  "https://example.com/cli",
		NormalizedURL: "https://example.com/cli",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:cli-okf",
		NotePath:      "sources/web/cli.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "CLI source summary.",
		Status:        model.SourceSummaryStatusOK,
		Model:         "test/model",
		PromptVersion: "source-summary-v1",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	exportOut := runRootCommand(t, root, "okf", "export", "--json")
	var exportResult okf.ExportResult
	if err := json.Unmarshal([]byte(exportOut), &exportResult); err != nil {
		t.Fatalf("unmarshal export: %v output=%q", err, exportOut)
	}
	if exportResult.Bundle != cfg.OKFDir || exportResult.ItemsWritten != 1 || exportResult.SourcesWritten != 1 || exportResult.BrokenInternalLinks != 0 {
		t.Fatalf("unexpected export result: %+v", exportResult)
	}

	validateOut := runRootCommand(t, root, "okf", "validate", cfg.OKFDir, "--json")
	var validation okf.ValidationResult
	if err := json.Unmarshal([]byte(validateOut), &validation); err != nil {
		t.Fatalf("unmarshal validate: %v output=%q", err, validateOut)
	}
	if !validation.Conformant || validation.Concepts < 3 || validation.BrokenInternalLinks != 0 {
		t.Fatalf("unexpected validation: %+v", validation)
	}
}

func TestOKFExportRejectsUnimplementedProfiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "--no-debug", "okf", "export", "--profile", "public", "--out", filepath.Join(root, "okf", "public")})
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected non-private profile to fail")
	}
	if !strings.Contains(err.Error(), "only private is supported") {
		t.Fatalf("unexpected error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
}

func TestOKFExportRejectsNoSelectedConceptKinds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "--no-debug", "okf", "export", "--items=false", "--sources=false", "--entities=false", "--topics=false"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected no concept kinds selected to fail")
	}
	if !strings.Contains(err.Error(), "no OKF concept kinds selected") {
		t.Fatalf("unexpected error: %v stderr=%q stdout=%q", err, stderr.String(), stdout.String())
	}
}
