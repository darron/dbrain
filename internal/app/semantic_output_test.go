package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticreadiness"
)

func TestSemanticProgressOutputIncludesQuarantined(t *testing.T) {
	progress := semanticbuild.Progress{Stage: "embed", Interrupted: true, Scanned: 2, Quarantined: 1}
	for name, write := range map[string]func(*bytes.Buffer) error{
		"snapshot": func(dst *bytes.Buffer) error { return writeSemanticProgressSnapshot(dst, progress) },
		"final":    func(dst *bytes.Buffer) error { return writeSemanticProgress(dst, progress) },
	} {
		t.Run(name, func(t *testing.T) {
			var dst bytes.Buffer
			if err := write(&dst); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(dst.String()), "quarantined") || !strings.Contains(strings.ToLower(dst.String()), "interrupted") || !strings.Contains(dst.String(), "1") {
				t.Fatalf("progress output = %q, want quarantine count", dst.String())
			}
		})
	}
}

func TestSemanticStatusOutputReportsSharedReadinessSnapshot(t *testing.T) {
	status := semanticbuild.Status{
		Status: "catching_up", Reason: "bounded debt", Searchable: true, Mode: "on", ProfileID: "profile",
		Store: semanticreadiness.Snapshot{Available: true, ExpectedParents: 5, CurrentParents: 4, PendingParents: 1, DirtyParents: 1, EstimatedNotReadyChunks: 3, ChunkCount: 8, ReadyEmbeddings: 7, PendingEmbeddings: 1, ActiveGenerationID: "root", L0ReadyCount: 2},
	}
	var dst bytes.Buffer
	if err := writeSemanticStatus(&dst, status); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Status: catching_up", "Searchable: true", "Parents: expected=5 current=4", "Estimated not-ready chunks: 3", "Ready embeddings: 7", "Index: active=root l0=2"} {
		if !strings.Contains(dst.String(), want) {
			t.Fatalf("output=%q missing %q", dst.String(), want)
		}
	}
}

func TestSemanticVerifyOutputIncludesResume(t *testing.T) {
	progress := semanticbuild.VerifyProgress{Progress: semanticbuild.Progress{Stage: "verify", Scanned: 2, Current: 1, Quarantined: 1}, Resume: "chunk-b", HasMore: true, CountersRepaired: true}
	var dst bytes.Buffer
	if err := writeSemanticVerifyProgress(&dst, progress); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Quarantined: 1", "Resume: chunk-b", "Has more: true", "Counters repaired: true"} {
		if !strings.Contains(dst.String(), want) {
			t.Fatalf("output=%q missing %q", dst.String(), want)
		}
	}
}
