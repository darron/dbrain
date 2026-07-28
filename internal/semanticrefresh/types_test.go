package semanticrefresh

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

func TestRequestValidateRejectsInvalidInputs(t *testing.T) {
	valid := Request{
		ProfileID:           "profile",
		PurgeEpoch:          1,
		ProjectionWatermark: 2,
		Capability: semanticindex.Capability{
			State:   semanticindex.CapabilitySupportedReady,
			Backend: semanticindex.BackendUSearch,
			Version: semanticindex.USearchVersion,
		},
		Now:          time.Now,
		NewRunIDFunc: func() (string, error) { return "run", nil },
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "empty profile", mutate: func(request *Request) { request.ProfileID = " \t\n" }},
		{name: "profile over byte bound", mutate: func(request *Request) { request.ProfileID = strings.Repeat("é", 97) }},
		{name: "negative purge epoch", mutate: func(request *Request) { request.PurgeEpoch = -1 }},
		{name: "negative watermark", mutate: func(request *Request) { request.ProjectionWatermark = -1 }},
		{name: "unsupported capability", mutate: func(request *Request) {
			request.Capability = semanticindex.Capability{State: semanticindex.CapabilityUnsupported}
		}},
		{name: "missing clock", mutate: func(request *Request) { request.Now = nil }},
		{name: "missing run id generator", mutate: func(request *Request) { request.NewRunIDFunc = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}

func TestRefreshErrorBoundsNormalizeAndUnwrap(t *testing.T) {
	cause := errors.New(" provider\tfailed\n" + strings.Repeat("é", 400))
	run := store.SemanticRefreshRun{
		RunID:      "run-1",
		Stage:      store.SemanticRefreshEmbedding,
		Checkpoint: " checkpoint\t" + strings.Repeat("界", 100),
	}
	refreshErr := NewError(
		" semantic_embedding_failed\t"+strings.Repeat("x", 80),
		run,
		" not\nready ",
		Debt{PendingEmbeddings: 7},
		cause,
	)

	if !errors.Is(refreshErr, cause) {
		t.Fatal("RefreshError does not unwrap its cause")
	}
	if got := refreshErr.Error(); got != refreshErr.Message {
		t.Fatalf("Error() = %q, want bounded message %q", got, refreshErr.Message)
	}
	for name, field := range map[string]struct {
		value string
		limit int
	}{
		"code":       {refreshErr.Code, 64},
		"checkpoint": {refreshErr.Checkpoint, 256},
		"readiness":  {refreshErr.Readiness, 64},
		"message":    {refreshErr.Message, 512},
	} {
		if len(field.value) > field.limit {
			t.Fatalf("%s is %d bytes, want <= %d", name, len(field.value), field.limit)
		}
		if !utf8.ValidString(field.value) {
			t.Fatalf("%s is not valid UTF-8: %q", name, field.value)
		}
		if strings.ContainsAny(field.value, "\t\n\r") || strings.Contains(field.value, "  ") {
			t.Fatalf("%s whitespace is not normalized: %q", name, field.value)
		}
	}
	if refreshErr.Debt.PendingEmbeddings != 7 {
		t.Fatalf("remaining debt = %+v, want pending_embeddings=7", refreshErr.Debt)
	}
}

func TestRefreshErrorJSONExposesOnlyBoundedFields(t *testing.T) {
	cause := errors.New(`open /Users/alice/private/corpus.db: provider response body={"vector":[1,2,3],"source":"private corpus text"}`)
	refreshErr := NewError(ErrorNativeRoot, store.SemanticRefreshRun{
		RunID:      "run-1",
		Stage:      store.SemanticRefreshVerify,
		Checkpoint: "verify:cursor=chunk-1",
	}, "not_ready", Debt{Segments: 3}, cause)

	encoded, err := json.Marshal(refreshErr)
	if err != nil {
		t.Fatalf("marshal RefreshError: %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{"/Users/alice", "corpus.db", `"vector"`, "private corpus text"} {
		if strings.Contains(text, secret) {
			t.Fatalf("error JSON leaked %q: %s", secret, text)
		}
	}
	if strings.Contains(text, "cause") {
		t.Fatalf("error JSON exposed cause field: %s", text)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal RefreshError JSON: %v", err)
	}
	for _, name := range []string{"code", "stage", "run_id", "checkpoint", "readiness", "remaining_debt", "message"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("error JSON missing %q: %s", name, text)
		}
	}
	if len(fields) != 7 {
		t.Fatalf("error JSON fields = %v, want exact bounded contract", fields)
	}
}

func TestRefreshErrorMarshalBoundsExportedFieldsAtOutputBoundary(t *testing.T) {
	refreshErr := &RefreshError{
		Code:       strings.Repeat("é", 40),
		Checkpoint: strings.Repeat("界", 100),
		Readiness:  strings.Repeat("é", 40),
		Message:    `open /private/corpus.db: provider response body={"source":"private corpus text"}` + strings.Repeat("界", 200),
	}
	encoded, err := json.Marshal(refreshErr)
	if err != nil {
		t.Fatalf("marshal RefreshError: %v", err)
	}
	var fields struct {
		Code, Checkpoint, Readiness, Message string
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal RefreshError: %v", err)
	}
	for name, field := range map[string]struct {
		value string
		limit int
	}{
		"code":       {fields.Code, 64},
		"checkpoint": {fields.Checkpoint, 256},
		"readiness":  {fields.Readiness, 64},
		"message":    {fields.Message, 512},
	} {
		if len(field.value) > field.limit || !utf8.ValidString(field.value) {
			t.Fatalf("%s = %q (%d bytes), want valid UTF-8 within %d bytes", name, field.value, len(field.value), field.limit)
		}
	}
	for _, secret := range []string{"/private/corpus.db", "private corpus text"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("error JSON leaked %q: %s", secret, encoded)
		}
	}
}

func TestResultJSONUsesBoundedAggregateDebt(t *testing.T) {
	result := Result{
		Outcome:    OutcomeSkipped,
		SkipReason: "semantic_mode_off",
		Capability: semanticindex.Capability{
			State: semanticindex.CapabilityUnsupported,
		},
		Debt: Debt{DirtyParents: 4, L0Ready: 2},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal Result: %v", err)
	}
	const want = `{"outcome":"skipped","skip_reason":"semantic_mode_off","capability":{"state":"unsupported"},"remaining_debt":{"dirty_parents":4,"pending_embeddings":0,"due_retries":0,"scheduled_retries":0,"blocked_embeddings":0,"failed_embeddings":0,"l0_ready":2,"tombstones":0,"segments":0}}`
	if string(encoded) != want {
		t.Fatalf("Result JSON = %s, want %s", encoded, want)
	}
}

type compileStageExecutor struct{}

func (compileStageExecutor) Execute(context.Context, store.SemanticRefreshRun) (StageOutcome, error) {
	return StageOutcome{}, nil
}

var _ StageExecutor = compileStageExecutor{}
