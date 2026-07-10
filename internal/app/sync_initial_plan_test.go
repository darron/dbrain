package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/syncjob"
)

func TestInitialSyncPlanHighlightsUnboundedImports(t *testing.T) {
	plan := buildInitialSyncPlan(syncjob.Options{
		XBookmarksEnabled: true,
		XEnabled:          true,
		XLimit:            100,
		XMediaEnabled:     true,
		XPhotoOCREnabled:  false,
		LinksEnabled:      true,
		GitHubEnabled:     true,
		YouTubeEnabled:    true,
		YouTubeLimit:      50,
		FeedsEnabled:      true,
		SourcesEnabled:    true,
		CategorizeEnabled: true,
	})

	if strings.Join(plan.Unbounded, ",") != "X bookmarks,GitHub stars" {
		t.Fatalf("unbounded imports = %#v", plan.Unbounded)
	}
	text := formatInitialSyncPlan(plan)
	for _, expected := range []string{
		"First sync on an empty brain.",
		"About to pull all configured unbounded imports: X bookmarks, GitHub stars.",
		"X bookmarks=all",
		"GitHub stars=all",
		"X hydration=100",
		"YouTube=50",
		"Skipped stages:",
		"X photo OCR",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected plan to contain %q:\n%s", expected, text)
		}
	}
}

func TestConfirmInitialSyncPlanRequiresExplicitYes(t *testing.T) {
	for _, input := range []string{"\n", "no\n", "n\n"} {
		var out bytes.Buffer
		ok, err := confirmInitialSyncPlan(strings.NewReader(input), &out)
		if err != nil {
			t.Fatalf("confirmInitialSyncPlan(%q): %v", input, err)
		}
		if ok {
			t.Fatalf("confirmInitialSyncPlan(%q) = true, want false", input)
		}
		if !strings.Contains(out.String(), "Proceed with this first sync?") {
			t.Fatalf("prompt missing from output: %q", out.String())
		}
	}

	var out bytes.Buffer
	ok, err := confirmInitialSyncPlan(strings.NewReader("yes\n"), &out)
	if err != nil {
		t.Fatalf("confirmInitialSyncPlan yes: %v", err)
	}
	if !ok {
		t.Fatal("confirmInitialSyncPlan yes = false, want true")
	}
}
