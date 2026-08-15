package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestLinkCaptureDeadLetterCommandsListAndRequeue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	prepareCurrentAppTestStore(t, root)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	candidate := model.SourceCandidate{
		OriginalURL:   "https://user:pass@example.com/dead-letter",
		CanonicalURL:  "https://user:pass@example.com/dead-letter",
		NormalizedURL: "https://user:pass@example.com/dead-letter",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:dead-letter-command",
		NotePath:      "sources/web/dead-letter-command.md",
	}
	enqueued, err := st.EnqueueLinkCapture(t.Context(), candidate, time.Date(2026, time.August, 15, 4, 0, 0, 0, time.UTC))
	if err != nil {
		_ = st.Close()
		t.Fatalf("enqueue: %v", err)
	}
	for attempt := 1; attempt <= store.MaxLinkCaptureAttempts; attempt++ {
		now := time.Date(2026, time.August, 15, 4+attempt, 0, 0, 0, time.UTC)
		if err := st.MarkLinkCaptureAttempt(t.Context(), enqueued.Capture.ID, now); err != nil {
			_ = st.Close()
			t.Fatalf("mark attempt %d: %v", attempt, err)
		}
		if err := st.MarkLinkCaptureFailed(t.Context(), enqueued.Capture.ID, now, now.Add(time.Minute), "feed_import"); err != nil {
			_ = st.Close()
			t.Fatalf("mark failure %d: %v", attempt, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	humanOutput := runRootCommand(t, root, "link", "capture", "dead-letters")
	for _, want := range []string{"ID", "FAILURE_KIND", "feed_import", "https://user:REDACTED@example.com/dead-letter"} {
		if !strings.Contains(humanOutput, want) {
			t.Fatalf("human dead-letter output missing %q: %s", want, humanOutput)
		}
	}
	if strings.Contains(humanOutput, "user:pass@") {
		t.Fatalf("human dead-letter output exposed URL credentials: %s", humanOutput)
	}

	listOutput := runRootCommand(t, root, "link", "capture", "dead-letters", "--json")
	var listed []store.LinkCapture
	if err := json.Unmarshal([]byte(listOutput), &listed); err != nil {
		t.Fatalf("decode dead-letter list: %v\n%s", err, listOutput)
	}
	if strings.Contains(listOutput, "user:pass@") || !strings.Contains(listOutput, "user:REDACTED@") {
		t.Fatalf("JSON dead-letter list redaction = %s", listOutput)
	}
	if len(listed) != 1 || listed[0].ID != enqueued.Capture.ID || listed[0].LastError != "feed_import" || listed[0].AttemptCount != store.MaxLinkCaptureAttempts {
		t.Fatalf("dead-letter list = %+v", listed)
	}

	requeueOutput := runRootCommand(t, root, "link", "capture", "requeue", fmt.Sprint(enqueued.Capture.ID), "--json")
	var requeued struct {
		Capture  store.LinkCapture `json:"capture"`
		Reopened bool              `json:"reopened"`
	}
	if err := json.Unmarshal([]byte(requeueOutput), &requeued); err != nil {
		t.Fatalf("decode requeue result: %v\n%s", err, requeueOutput)
	}
	if strings.Contains(requeueOutput, "user:pass@") || !strings.Contains(requeueOutput, "user:REDACTED@") {
		t.Fatalf("JSON requeue redaction = %s", requeueOutput)
	}
	if !requeued.Reopened || requeued.Capture.ID != enqueued.Capture.ID {
		t.Fatalf("requeue result = %+v", requeued)
	}
	st, err = store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = st.Close() }()
	pending, err := st.ListPendingLinkCaptures(t.Context(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("list requeued pending captures: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != enqueued.Capture.ID || pending[0].AttemptCount != 0 {
		t.Fatalf("requeued pending captures = %+v", pending)
	}
	emptyOutput := runRootCommand(t, root, "link", "capture", "dead-letters", "--json")
	var empty []store.LinkCapture
	if err := json.Unmarshal([]byte(emptyOutput), &empty); err != nil {
		t.Fatalf("decode empty dead-letter list: %v\n%s", err, emptyOutput)
	}
	if len(empty) != 0 {
		t.Fatalf("empty dead-letter list = %+v", empty)
	}
}
