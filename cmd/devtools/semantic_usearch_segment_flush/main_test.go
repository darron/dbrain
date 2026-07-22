//go:build usearch && cgo

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresExplicitApply(t *testing.T) {
	err := runWithDeps(context.Background(), []string{
		"--db", filepath.Join(t.TempDir(), "restored.db"),
		"--cache", t.TempDir(),
		"--provider", "ollama", "--model", "test-model", "--dimensions", "2",
		"--report", filepath.Join(t.TempDir(), "report.json"),
	}, flushDeps{})
	if err == nil || !strings.Contains(err.Error(), "--apply") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteRefusesConfiguredProductionDatabaseBeforeOpen(t *testing.T) {
	production := filepath.Join(t.TempDir(), "brain.db")
	called := false
	err := execute(context.Background(), flushOptions{
		database: production, cache: t.TempDir(), provider: "ollama", model: "test-model", dimensions: 2,
		apply: true, reportPath: filepath.Join(t.TempDir(), "report.json"),
	}, flushDeps{
		refusesProduction: func(string) (bool, error) { return true, nil },
		openReadOnly:      func(string) (flushStore, error) { called = true; return nil, errors.New("must not open") },
	})
	if err == nil || !strings.Contains(err.Error(), "configured production database") || called {
		t.Fatalf("error=%v open=%v", err, called)
	}
}

func TestRefusesProductionDatabaseDiscoversCandidateRoot(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "data", "brain.db")
	if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
		t.Fatal(err)
	}
	refused, err := refusesProductionDatabase(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !refused {
		t.Fatalf("candidate %s was not recognized as its root's configured database", candidate)
	}
}
