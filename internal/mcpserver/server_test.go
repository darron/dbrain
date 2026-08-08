package mcpserver

import (
	"bufio"
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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

func TestResearchPackSchemaValidatesChunkFusionAndShadowArrays(t *testing.T) {
	fused, distance, raw, contribution := 0.03, 0.1, 7.0, 0.02
	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion, Question: "q", Mode: "evidence_only",
		QueryPlan: brainresearch.QueryPlan{TextQuery: "q", QueryTerms: []string{}, TagQueries: []string{}, Concepts: []brainresearch.QueryConcept{{Key: "q", Terms: []string{"q"}, Required: true, Role: "content"}}, Limit: 1, MaxCharsPerDoc: 100, SemanticMode: semanticconfig.ModeShadow, SemanticReadiness: semanticreadiness.StateCatchingUp, SemanticReadinessDiagnostics: &brainresearch.SemanticReadinessDiagnostics{OmittedParentCount: 2, EstimatedNotReadyChunks: 3}, ShadowComparison: &brainresearch.ShadowComparison{
			Status: semanticindex.StateSearched, Lexical: []brainresearch.ShadowRankedReference{}, Hybrid: []brainresearch.ShadowRankedReference{}, Added: []brainresearch.ShadowRankedReference{}, Removed: []brainresearch.ShadowRankedReference{}, Reordered: []brainresearch.ShadowRankedReference{},
		}, RetryShadowComparison: &brainresearch.ShadowComparison{Status: semanticindex.StateUnavailable, Reason: semanticindex.ReasonProviderUnavailable, Lexical: []brainresearch.ShadowRankedReference{}, Hybrid: []brainresearch.ShadowRankedReference{}, Added: []brainresearch.ShadowRankedReference{}, Removed: []brainresearch.ShadowRankedReference{}, Reordered: []brainresearch.ShadowRankedReference{}}},
		Coverage: brainresearch.Coverage{ByKind: []brainresearch.Bucket{}, BySourceType: []brainresearch.Bucket{}},
		Evidence: []retrieval.EvidenceDocument{{SourceKey: "src", Kind: "source", Title: "t", URL: "u", NotePath: "n", Summary: "s", Excerpt: "e", EvidenceRole: "raw", ContentSections: []retrieval.ContentSection{}, Chunk: &retrieval.EvidenceChunk{ID: "c", ParentSourceKey: "src", ContributingIDs: []string{}}, Retrieval: &retrieval.RetrievalInfo{FusedScore: &fused, Lanes: []retrieval.RetrievalLane{{Name: "semantic", Rank: 1, RawDistance: &distance, RawScore: &raw, Contribution: &contribution}}, Signals: []retrieval.RetrievalSignal{}, MatchedTerms: []string{}, MissingTerms: []string{}}}},
	}
	encoded, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateJSONSchemaValue(researchPackOutputSchema(), decoded); err != nil {
		t.Fatalf("schema validation: %v\n%s", err, encoded)
	}
}

func TestResearchQueryConceptSchemaAdvertisesRole(t *testing.T) {
	t.Parallel()

	properties, ok := researchQueryConceptSchema()["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("concept schema properties missing: %#v", researchQueryConceptSchema())
	}
	role, ok := properties["role"].(map[string]interface{})
	if !ok {
		t.Fatalf("concept role schema missing: %#v", properties)
	}
	want := []interface{}{"anchor", "content", "intent", "frame"}
	if got := role["enum"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("concept role enum = %#v, want %#v", got, want)
	}
}

func TestResearchQueryPlanSchemaAdvertisesSemanticReadinessDiagnostics(t *testing.T) {
	t.Parallel()

	properties, ok := researchQueryPlanSchema()["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("query plan schema properties missing: %#v", researchQueryPlanSchema())
	}
	readiness, ok := properties["semantic_readiness"].(map[string]interface{})
	if !ok {
		t.Fatalf("semantic readiness schema missing: %#v", properties)
	}
	wantStates := []interface{}{
		"not_configured", "disabled", "needs_projection", "needs_embeddings", "retry_scheduled", "needs_index",
		"building", "catching_up", "degraded_blocked", "stale", "corrupt", "ready", "unavailable",
	}
	if got := readiness["enum"]; !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("semantic readiness enum = %#v, want %#v", got, wantStates)
	}

	diagnostics, ok := properties["semantic_readiness_diagnostics"].(map[string]interface{})
	if !ok {
		t.Fatalf("semantic readiness diagnostics schema missing: %#v", properties)
	}
	diagnosticProperties, ok := diagnostics["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("semantic readiness diagnostics properties missing: %#v", diagnostics)
	}
	for _, key := range []string{"omitted_parent_count", "estimated_not_ready_chunks"} {
		field, ok := diagnosticProperties[key].(map[string]interface{})
		if !ok || field["type"] != "integer" {
			t.Fatalf("diagnostic %s schema = %#v, want integer", key, field)
		}
	}
	oldest, ok := diagnosticProperties["oldest_debt_at"].(map[string]interface{})
	if !ok || oldest["type"] != "string" {
		t.Fatalf("oldest_debt_at schema = %#v, want string", oldest)
	}
}

func TestResearchPackConfiguredShadowIsTraceFreeAndConflictIsToolError(t *testing.T) {
	var embedCalled atomic.Bool
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embedCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"test-model","embeddings":[[1,0]]}`))
	}))
	defer embed.Close()
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	configYAML := "research:\n  semantic:\n    mode: shadow\n    model: test-model\n    dimensions: 2\nollama:\n  base_url: " + embed.URL + "\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	server := New(cfg, st)
	result, err := server.toolResearchPack(context.Background(), json.RawMessage(`{"question":"agent memory","disable_planner":true,"include_topic_brief":false}`))
	if err != nil {
		t.Fatal(err)
	}
	pack := result["structuredContent"].(brainresearch.Pack)
	if pack.QueryPlan.SemanticMode != semanticconfig.ModeShadow || pack.QueryPlan.SemanticReadiness != semanticreadiness.StateNeedsEmbeddings || pack.QueryPlan.ShadowComparison == nil || embedCalled.Load() {
		t.Fatalf("shadow pack=%#v embed_called=%t", pack.QueryPlan, embedCalled.Load())
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "research-runs")); !os.IsNotExist(err) {
		t.Fatalf("MCP shadow wrote trace directory: %v", err)
	}
	_, err = server.toolResearchPack(context.Background(), json.RawMessage(`{"question":"q","use_semantic":true,"disable_semantic":true}`))
	if !errors.Is(err, semanticconfig.ErrConflictingOverrides) {
		t.Fatalf("conflict err=%v", err)
	}
}

func TestResearchPackConflictWinsBeforeMalformedSemanticConfig(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("research:\n  semantic:\n    mode: malformed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_research_pack","arguments":{"question":"q","use_semantic":true,"disable_semantic":true}}}`
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatal(err)
	}
	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	if result["isError"] != true || !strings.Contains(fmt.Sprint(result["content"]), semanticconfig.ErrConflictingOverrides.Error()) || strings.Contains(fmt.Sprint(result["content"]), "malformed") {
		t.Fatalf("unexpected structured conflict result: %#v", result)
	}
}

func TestServerInitializeAndToolsList(t *testing.T) {
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

	server := New(cfg, st)

	input := lineJSON(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		lineJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(responses))
	}
	if bytes.Contains(out.Bytes(), []byte("Content-Length:")) {
		t.Fatalf("stdio MCP responses should be newline-delimited JSON, got %q", out.String())
	}

	initResult := responses[0]["result"].(map[string]interface{})
	if initResult["protocolVersion"] != protocolVersion {
		t.Fatalf("unexpected protocol version: %#v", initResult["protocolVersion"])
	}
	serverInfo := initResult["serverInfo"].(map[string]interface{})
	if serverInfo["version"] == "0.1.0" || strings.TrimSpace(fmt.Sprint(serverInfo["version"])) == "" {
		t.Fatalf("expected build-derived server version, got %#v", serverInfo["version"])
	}
	capabilities := initResult["capabilities"].(map[string]interface{})
	if _, ok := capabilities["resources"]; !ok {
		t.Fatalf("expected resources capability: %#v", capabilities)
	}
	if _, ok := capabilities["prompts"]; !ok {
		t.Fatalf("expected prompts capability: %#v", capabilities)
	}

	listResult := responses[1]["result"].(map[string]interface{})
	tools := listResult["tools"].([]interface{})
	if len(tools) == 0 {
		t.Fatal("expected non-empty tools list")
	}
	firstTool := tools[0].(map[string]interface{})
	if _, ok := firstTool["outputSchema"]; !ok {
		t.Fatalf("expected tool output schema, got %#v", firstTool)
	}
	requiredTools := map[string]bool{
		"dbrain_search":        false,
		"dbrain_get":           false,
		"dbrain_get_many":      false,
		"dbrain_research_pack": false,
		"dbrain_related":       false,
		"dbrain_topic_map":     false,
		"dbrain_topic_brief":   false,
		"dbrain_entity_map":    false,
		"dbrain_whats_new":     false,
	}
	for _, entry := range tools {
		tool := entry.(map[string]interface{})
		name, _ := tool["name"].(string)
		if _, ok := requiredTools[name]; ok {
			requiredTools[name] = true
		}
	}
	var missing []string
	for name, found := range requiredTools {
		if !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("expected core research workflow tools in tools list, missing %v: %#v", missing, tools)
	}
}

type auditTestStore struct {
	report  *audit.Report
	profile audit.Profile
	calls   atomic.Int32
}

func (s *auditTestStore) Latest(profile audit.Profile) (*audit.Report, error) {
	s.profile = profile
	s.calls.Add(1)
	return s.report, nil
}

func TestAuditStdioAdvertisesAndCalls(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{
		Audit: AuditDependencies{
			RunFast: func(context.Context) (audit.Report, error) { return report, nil },
			Now:     func() time.Time { return now },
		},
	})
	input := lineJSON(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`) +
		lineJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_audit","arguments":{}}}`)
	var out bytes.Buffer
	if err := server.Serve(t.Context(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	responses := parseResponses(t, out.Bytes())
	if len(responses) != 2 {
		t.Fatalf("responses = %d, want 2", len(responses))
	}
	assertAuditToolAdvertised(t, responses[0], true)
	result := responses[1]["result"].(map[string]interface{})
	if result["isError"] != false {
		t.Fatalf("audit result = %#v", result)
	}
	structured := result["structuredContent"].(map[string]interface{})
	if structured["report"].(map[string]interface{})["profile"] != "fast" {
		t.Fatalf("audit structured result = %#v", structured)
	}
	if structured["freshness"].(map[string]interface{})["status"] != "current" {
		t.Fatalf("audit freshness = %#v", structured["freshness"])
	}
}

func TestAuditHTTPRequiresBearerCapability(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	var runnerCalls atomic.Int32
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) {
			runnerCalls.Add(1)
			return report, nil
		},
		Now: func() time.Time { return now },
	}})

	t.Run("auth disabled omits and rejects", func(t *testing.T) {
		handler := server.HTTPHandler(HTTPOptions{Path: "/mcp"})
		listed := postMCPJSON(t, handler, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		assertAuditToolAdvertised(t, listed, false)
		called := postMCPJSON(t, handler, "", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_audit","arguments":{}}}`)
		result := called["result"].(map[string]interface{})
		if result["isError"] != true || !strings.Contains(fmt.Sprint(result), "unknown tool") {
			t.Fatalf("auth-disabled direct call = %#v", called)
		}
		if runnerCalls.Load() != 0 {
			t.Fatalf("auth-disabled audit invoked runner %d times", runnerCalls.Load())
		}
	})

	t.Run("bearer authenticated advertises and calls", func(t *testing.T) {
		handler := server.HTTPHandler(HTTPOptions{
			Path:              "/mcp",
			RequireBearerAuth: true,
			BearerTokenValidator: func(context.Context, string) (bool, error) {
				return true, nil
			},
		})
		listed := postMCPJSON(t, handler, "secret", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		assertAuditToolAdvertised(t, listed, true)
		called := postMCPJSON(t, handler, "secret", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_audit","arguments":{"profile":"fast"}}}`)
		if called["result"].(map[string]interface{})["isError"] != false {
			t.Fatalf("authenticated audit call = %#v", called)
		}
	})
}

func TestAuditInputSchemaIsClosed(t *testing.T) {
	definition := findToolDefinition(t, "dbrain_audit")
	input := definition["inputSchema"].(map[string]interface{})
	if input["additionalProperties"] != false {
		t.Fatalf("audit input schema is not closed: %#v", input)
	}
	properties := input["properties"].(map[string]interface{})
	if len(properties) != 1 {
		t.Fatalf("audit input properties = %#v, want only profile", properties)
	}
	profile := properties["profile"].(map[string]interface{})
	if fmt.Sprint(profile["enum"]) != "[fast standard]" || profile["default"] != "fast" {
		t.Fatalf("audit profile schema = %#v", profile)
	}
}

func TestAuditRejectsUnsupportedInputs(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) { return report, nil },
		Now:     func() time.Time { return now },
	}})
	for _, args := range []string{
		`null`,
		`[]`,
		`{"profile":"deep"}`,
		`{"categories":["pipeline"]}`,
		`{"since":"24h"}`,
		`{"path":"/tmp/private"}`,
		`{"url":"https://example.com"}`,
		`{"endpoint":"https://example.com"}`,
		`{"source_key":"x:private"}`,
		`{"archive_key":"private/key"}`,
		`{"max_archive_bytes":1}`,
	} {
		t.Run(args, func(t *testing.T) {
			input := lineJSON(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_audit","arguments":%s}}`, args))
			var out bytes.Buffer
			if err := server.Serve(t.Context(), strings.NewReader(input), &out); err != nil {
				t.Fatalf("Serve: %v", err)
			}
			result := parseResponses(t, out.Bytes())[0]["result"].(map[string]interface{})
			if result["isError"] != true {
				t.Fatalf("unsupported args %s succeeded: %#v", args, result)
			}
		})
	}
}

func TestAuditArgumentErrorsAreSanitizedAndBounded(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) { return report, nil },
		Now:     func() time.Time { return now },
	}})

	const marker = "attacker-controlled-audit-input"
	oversized := strings.Repeat(marker, (auditToolMaxBytes/len(marker))+2)
	cases := map[string]string{
		"unknown key": fmt.Sprintf(`{%q:true}`, oversized),
		"profile":     fmt.Sprintf(`{"profile":%q}`, oversized),
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			input := lineJSON(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_audit","arguments":%s}}`, arguments))
			var out bytes.Buffer
			if err := server.Serve(t.Context(), strings.NewReader(input), &out); err != nil {
				t.Fatalf("Serve: %v", err)
			}
			responses := parseResponses(t, out.Bytes())
			result := responses[0]["result"].(map[string]interface{})
			if result["isError"] != true {
				t.Fatalf("oversized %s succeeded: %#v", name, result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			if len(encoded) > auditToolMaxBytes {
				t.Fatalf("oversized %s result bytes = %d, want <= %d", name, len(encoded), auditToolMaxBytes)
			}
			if bytes.Contains(encoded, []byte(marker)) {
				t.Fatalf("oversized %s result reflected attacker-controlled input", name)
			}
		})
	}
}

func TestAuditFastUsesTenSecondDeadlineAndProcessWideSingleflight(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	runner := func(ctx context.Context) (audit.Report, error) {
		calls.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok {
			return audit.Report{}, fmt.Errorf("fast audit context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 9*time.Second || remaining > 10*time.Second {
			return audit.Report{}, fmt.Errorf("fast audit deadline remaining %s, want fixed ten-second ceiling", remaining)
		}
		once.Do(func() { close(started) })
		select {
		case <-release:
			return report, nil
		case <-ctx.Done():
			return audit.Report{}, ctx.Err()
		}
	}
	deps := ServerDependencies{Audit: AuditDependencies{RunFast: runner, Now: func() time.Time { return now }}}
	servers := []*Server{NewWithDependencies(config.Config{}, nil, deps), NewWithDependencies(config.Config{}, nil, deps)}
	errs := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *Server) {
			_, err := server.callAudit(t.Context(), json.RawMessage(`{"profile":"fast"}`))
			errs <- err
		}(server)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fast audit did not start")
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range servers {
		if err := <-errs; err != nil {
			t.Fatalf("fast audit call: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("process-wide fast audit runner calls = %d, want 1", calls.Load())
	}
}

func TestAuditFastReturnsBoundedTimeoutWhenRunnerIgnoresContext(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var releaseOnce, startedOnce, finishedOnce sync.Once
	var runnerCalls, timerCalls, timerStops atomic.Int32
	var invalidTimerDuration atomic.Bool
	releaseRunner := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRunner)

	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) {
			runnerCalls.Add(1)
			startedOnce.Do(func() { close(started) })
			<-release
			finishedOnce.Do(func() { close(finished) })
			return report, nil
		},
		Now: func() time.Time { return now },
	}})
	deadline := make(chan time.Time)
	server.newAuditTimer = func(timeout time.Duration) auditTimer {
		timerCalls.Add(1)
		if timeout != auditToolDeadline {
			invalidTimerDuration.Store(true)
		}
		return auditTimer{
			done: deadline,
			stop: func() bool {
				timerStops.Add(1)
				return true
			},
		}
	}
	input := lineJSON(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_audit","arguments":{"profile":"fast"}}}`)
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- server.Serve(t.Context(), strings.NewReader(input), &out) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fast audit runner did not start")
	}
	close(deadline)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(time.Second):
		releaseRunner()
		<-done
		t.Fatal("dbrain_audit did not enforce its fixed ten-second wall-clock deadline")
	}

	result := parseResponses(t, out.Bytes())[0]["result"].(map[string]interface{})
	if result["isError"] != true {
		t.Fatalf("timeout result = %#v, want tool error", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal timeout result: %v", err)
	}
	if len(encoded) > auditToolMaxBytes {
		t.Fatalf("timeout result bytes = %d, want <= %d", len(encoded), auditToolMaxBytes)
	}
	if !bytes.Contains(encoded, []byte("fast audit timed out")) {
		t.Fatalf("timeout result = %s, want sanitized timeout error", encoded)
	}
	if _, err := server.callAudit(t.Context(), json.RawMessage(`{"profile":"fast"}`)); !errors.Is(err, errFastAuditTimedOut) {
		t.Fatalf("second call while runner is stuck error = %v, want sanitized timeout", err)
	}
	if runnerCalls.Load() != 1 {
		t.Fatalf("timed-out singleflight runner calls = %d, want 1 without fan-out", runnerCalls.Load())
	}
	if invalidTimerDuration.Load() {
		t.Fatalf("audit timer duration was not fixed at %s", auditToolDeadline)
	}

	never := make(chan time.Time)
	server.newAuditTimer = func(timeout time.Duration) auditTimer {
		timerCalls.Add(1)
		if timeout != auditToolDeadline {
			invalidTimerDuration.Store(true)
		}
		return auditTimer{done: never, stop: func() bool { timerStops.Add(1); return true }}
	}
	releaseRunner()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("released audit runner did not finish")
	}
	cleanupDeadline := time.Now().Add(time.Second)
	for runnerCalls.Load() < 2 && time.Now().Before(cleanupDeadline) {
		if _, err := server.callAudit(t.Context(), json.RawMessage(`{"profile":"fast"}`)); err != nil {
			t.Fatalf("post-cleanup fast audit: %v", err)
		}
		if runnerCalls.Load() < 2 {
			time.Sleep(time.Millisecond)
		}
	}
	if runnerCalls.Load() != 2 {
		t.Fatalf("post-cleanup runner calls = %d, want a new singleflight execution", runnerCalls.Load())
	}
	if invalidTimerDuration.Load() {
		t.Fatalf("audit timer duration was not fixed at %s", auditToolDeadline)
	}
	if timerStops.Load() != timerCalls.Load() {
		t.Fatalf("audit timer stops = %d, calls = %d", timerStops.Load(), timerCalls.Load())
	}
}

func TestAuditStandardReadsNewestExactProfileWithoutRunningAudit(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileStandard, now.Add(-time.Hour))
	store := &auditTestStore{report: &report}
	var runnerCalls atomic.Int32
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) {
			runnerCalls.Add(1)
			return audit.Report{}, fmt.Errorf("must not run")
		},
		Reports:          store,
		SyncInterval:     time.Hour,
		StandardInterval: 6 * time.Hour,
		Now:              func() time.Time { return now },
	}})
	result, err := server.callAudit(t.Context(), json.RawMessage(`{"profile":"standard"}`))
	if err != nil {
		t.Fatalf("standard audit: %v", err)
	}
	if store.profile != audit.ProfileStandard || store.calls.Load() != 1 {
		t.Fatalf("standard store lookup profile=%q calls=%d", store.profile, store.calls.Load())
	}
	if runnerCalls.Load() != 0 {
		t.Fatalf("standard audit invoked runner %d times", runnerCalls.Load())
	}
	structured := result["structuredContent"].(audit.PresentedReport)
	if structured.Report == nil || structured.Report.AuditID != report.AuditID || structured.Freshness.Status != audit.FreshnessCurrent {
		t.Fatalf("standard presentation = %#v", structured)
	}
}

func TestAuditStandardPresentationCannotMutateCachedReport(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileStandard, now.Add(-time.Hour))
	store := &auditTestStore{report: &report}
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		Reports:          store,
		StandardInterval: 6 * time.Hour,
		Now:              func() time.Time { return now },
	}})
	result, err := server.callAudit(t.Context(), json.RawMessage(`{"profile":"standard"}`))
	if err != nil {
		t.Fatalf("standard audit: %v", err)
	}
	presented := result["structuredContent"].(audit.PresentedReport)
	wantCategory := store.report.Scope.Categories[0]
	wantSource := store.report.Scope.Sources[0]
	presented.Report.Scope.Categories[0] = audit.CategoryDurability
	presented.Report.Scope.Sources[0] = audit.SourceFeeds
	presented.Report.Scope.CheckIDs = append(presented.Report.Scope.CheckIDs, audit.CheckPipelineSummaryPartition)
	presented.Report.Checks[0].Evidence["mutated"] = true
	if store.report.Scope.Categories[0] != wantCategory || store.report.Scope.Sources[0] != wantSource || len(store.report.Scope.CheckIDs) != 0 {
		t.Fatalf("cached report scope mutated: %#v", store.report.Scope)
	}
	if _, ok := store.report.Checks[0].Evidence["mutated"]; ok {
		t.Fatalf("cached report evidence mutated: %#v", store.report.Checks[0].Evidence)
	}
}

func TestAuditStandardNotFoundReturnsUnknownFreshnessAndNullReport(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &auditTestStore{}
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		Reports:          store,
		StandardInterval: 6 * time.Hour,
		Now:              func() time.Time { return now },
	}})
	result, err := server.callAudit(t.Context(), json.RawMessage(`{"profile":"standard"}`))
	if err != nil {
		t.Fatalf("standard audit: %v", err)
	}
	data, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatalf("marshal structured output: %v", err)
	}
	var structured interface{}
	if err := json.Unmarshal(data, &structured); err != nil {
		t.Fatalf("decode structured output: %v", err)
	}
	value := structured.(map[string]interface{})
	if value["report"] != nil {
		t.Fatalf("missing standard report = %#v, want null", value["report"])
	}
	freshness := value["freshness"].(map[string]interface{})
	if freshness["status"] != "unknown" || freshness["reason"] != "not_found" {
		t.Fatalf("missing standard freshness = %#v", freshness)
	}
	if _, ok := freshness["age_seconds"]; ok {
		t.Fatalf("missing standard freshness has age: %#v", freshness)
	}
	if err := validateJSONSchemaValue(auditOutputSchema(), structured); err != nil {
		t.Fatalf("missing standard output does not match schema: %v", err)
	}
}

func TestAuditStructuredOutputMatchesSchemaAndPrivacyBounds(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) { return report, nil },
		Now:     func() time.Time { return now },
	}})
	result, err := server.callAudit(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	structuredJSON, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatalf("marshal structured output: %v", err)
	}
	if len(structuredJSON) > 256<<10 {
		t.Fatalf("structured output bytes = %d, want <= 256 KiB", len(structuredJSON))
	}
	var structured interface{}
	if err := json.Unmarshal(structuredJSON, &structured); err != nil {
		t.Fatalf("decode structured output: %v", err)
	}
	if err := validateJSONSchemaValue(auditOutputSchema(), structured); err != nil {
		t.Fatalf("structured output does not match advertised schema: %v", err)
	}
	assertNoAuditPrivateKeys(t, structured, "")
	reportValue := structured.(map[string]interface{})["report"].(map[string]interface{})
	scope := reportValue["scope"].(map[string]interface{})
	for _, key := range []string{"categories", "sources", "check_ids"} {
		if value, ok := scope[key].([]interface{}); !ok || value == nil {
			t.Fatalf("scope.%s is not a non-null array: %#v", key, scope[key])
		}
	}
	if checks, ok := reportValue["checks"].([]interface{}); !ok || checks == nil {
		t.Fatalf("report.checks is not a non-null array: %#v", reportValue["checks"])
	}
}

func TestAuditOutputSchemaAcceptsBothPersistedReportVersions(t *testing.T) {
	properties := auditReportProperties()
	schema, ok := properties["schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("audit report schema property = %#v", properties["schema"])
	}
	if err := validateJSONSchemaValue(schema, audit.SchemaV1); err != nil {
		t.Fatalf("v1 report schema rejected: %v", err)
	}
	if err := validateJSONSchemaValue(schema, audit.SchemaV2); err != nil {
		t.Fatalf("v2 report schema rejected: %v", err)
	}
	check := auditCheckSchema()
	category := check["properties"].(map[string]interface{})["category"].(map[string]interface{})
	if err := validateJSONSchemaValue(category, "semantic"); err != nil {
		t.Fatalf("semantic audit category rejected: %v", err)
	}
}

func TestAuditOutputSchemaClosesSemanticEvidenceValues(t *testing.T) {
	evidence := auditEvidenceSchema()["properties"].(map[string]interface{})
	const profileID = "embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const generationID = "semantic-root-v1:0123456789abcdef0123456789abcdef"
	if err := validateJSONSchemaValue(evidence["profile_id"].(map[string]interface{}), profileID); err != nil {
		t.Fatalf("canonical semantic profile ID rejected: %v", err)
	}
	if err := validateJSONSchemaValue(evidence["active_generation_id"].(map[string]interface{}), generationID); err != nil {
		t.Fatalf("canonical semantic generation ID rejected: %v", err)
	}
	for name, value := range map[string]interface{}{
		"profile_id":          "/Users/alice/private-profile",
		"readiness":           "the backend said everything is fine",
		"semantic_error_code": "raw provider error: secret",
	} {
		if err := validateJSONSchemaValue(evidence[name].(map[string]interface{}), value); err == nil {
			t.Fatalf("semantic %s accepted unbounded value %q", name, value)
		}
	}
	for _, value := range []string{"generation-current", "semantic-root-v1:0123456789abcdef0123456789abcdeF", "checkpoint:private-run", "/private/root"} {
		if err := validateJSONSchemaValue(evidence["active_generation_id"].(map[string]interface{}), value); err == nil {
			t.Fatalf("semantic active_generation_id accepted non-canonical value %q", value)
		}
	}
	stages := evidence["stages"].(map[string]interface{})
	for _, value := range []interface{}{
		[]interface{}{},
		[]interface{}{
			map[string]interface{}{"stage": "projection", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "embedding", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "flush", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "compaction", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "verification", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "readiness", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "extra", "status": "succeeded", "duration_seconds": float64(1)},
		},
		[]interface{}{
			map[string]interface{}{"stage": "not-a-stage", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "embedding", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "flush", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "compaction", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "verification", "status": "succeeded", "duration_seconds": float64(1)},
			map[string]interface{}{"stage": "readiness", "status": "succeeded", "duration_seconds": float64(1)},
		},
	} {
		if err := validateJSONSchemaValue(stages, value); err == nil {
			t.Fatalf("semantic stages accepted invalid shape: %#v", value)
		}
	}
}

func TestAuditRejectsValidReportAboveOutputCeiling(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := auditTestReport(t, audit.ProfileFast, now)
	daily := make([]map[string]any, audit.MaxDailyEvidence)
	for index := range daily {
		daily[index] = map[string]any{
			"day": "2026-07-14", "created": index, "updated": index, "unchanged": index,
			"skipped": index, "linked": index, "blocked": index, "failed": index,
		}
	}
	for index := range report.Checks {
		if strings.HasSuffix(string(report.Checks[index].ID), ".arrivals") {
			report.Checks[index].Status = audit.StatusUnknown
			report.Checks[index].SkipReason = ""
			report.Checks[index].Evidence = audit.Evidence{"quiet_seconds": 0, "daily": daily}
		}
	}
	audit.FinalizeReport(&report)
	if err := audit.ValidateReport(report); err != nil {
		t.Fatalf("oversized fixture is not a valid audit report: %v", err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal oversized report: %v", err)
	}
	if len(data) <= 256<<10 {
		t.Fatalf("oversized fixture bytes = %d, want >256 KiB", len(data))
	}
	server := NewWithDependencies(config.Config{}, nil, ServerDependencies{Audit: AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) { return report, nil },
		Now:     func() time.Time { return now },
	}})
	if _, err := server.callAudit(t.Context(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "256 KiB") {
		t.Fatalf("oversized audit error = %v", err)
	}
}

func auditTestReport(t *testing.T, profile audit.Profile, completed time.Time) audit.Report {
	t.Helper()
	report, err := audit.Run(t.Context(), audit.Request{Profile: profile}, audit.Dependencies{
		Features: audit.Features{
			Layout:                  "xdg",
			ConfigSource:            "default",
			ConfigVerified:          true,
			DatabaseOpenedQueryOnly: true,
			Sources:                 map[audit.Source]bool{},
			Stages:                  map[audit.PipelineStage]bool{},
		},
		Runtime: audit.RuntimeVersion{
			ReleaseVersion: "v0.6.0", Commit: "abcdef1", GitStatus: "clean", Platform: "linux/amd64",
			SecurityBaselineID: "v0.6.0-security-pass", SecurityBaselineEpoch: 1,
		},
		Clock: func() time.Time { return completed },
	})
	if err != nil {
		t.Fatalf("build audit test report: %v", err)
	}
	return report
}

func postMCPJSON(t *testing.T, handler http.Handler, token, body string) map[string]interface{} {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode HTTP response: %v", err)
	}
	return response
}

func assertAuditToolAdvertised(t *testing.T, response map[string]interface{}, want bool) {
	t.Helper()
	tools := response["result"].(map[string]interface{})["tools"].([]interface{})
	found := false
	for _, raw := range tools {
		if raw.(map[string]interface{})["name"] == "dbrain_audit" {
			found = true
		}
	}
	if found != want {
		t.Fatalf("dbrain_audit advertised=%v, want %v", found, want)
	}
}

func findToolDefinition(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	for _, definition := range toolDefinitions() {
		if definition["name"] == name {
			return definition
		}
	}
	t.Fatalf("tool definition %q not found", name)
	return nil
}

func assertNoAuditPrivateKeys(t *testing.T, value interface{}, path string) {
	t.Helper()
	forbidden := map[string]bool{
		"path": true, "url": true, "endpoint": true, "source_key": true, "external_id": true,
		"row_id": true, "archive_key": true, "object_key": true, "content": true, "title": true,
		"text": true, "raw_json": true,
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if forbidden[key] {
				t.Fatalf("private audit key %s.%s", path, key)
			}
			assertNoAuditPrivateKeys(t, child, path+"."+key)
		}
	case []interface{}:
		for index, child := range typed {
			assertNoAuditPrivateKeys(t, child, fmt.Sprintf("%s[%d]", path, index))
		}
	}
}

func TestProcessPayloadEnforcesBatchLimit(t *testing.T) {
	server := &Server{}

	t.Run("sixteen requests produce sixteen responses", func(t *testing.T) {
		result, ok := server.processPayload(t.Context(), testPingBatch(t, 16, false))
		if !ok {
			t.Fatal("expected exact-limit batch response")
		}
		responses, ok := result.([]response)
		if !ok {
			t.Fatalf("exact-limit result type = %T, want []response", result)
		}
		if len(responses) != 16 {
			t.Fatalf("exact-limit responses = %d, want 16", len(responses))
		}
		for i, response := range responses {
			if response.Error != nil || string(response.ID) != fmt.Sprintf("%d", i+1) {
				t.Fatalf("response %d = %#v", i, response)
			}
		}
	})

	t.Run("seventeen requests return one error without per-request results", func(t *testing.T) {
		result, ok := server.processPayload(t.Context(), testPingBatch(t, 17, false))
		if !ok {
			t.Fatal("expected oversized batch error response")
		}
		response, ok := result.(response)
		if !ok {
			t.Fatalf("oversized result type = %T, want one response", result)
		}
		assertBatchLimitError(t, response)
	})

	t.Run("mixed notifications at limit preserve response filtering", func(t *testing.T) {
		result, ok := server.processPayload(t.Context(), testPingBatch(t, 16, true))
		if !ok {
			t.Fatal("expected mixed exact-limit batch response")
		}
		responses, ok := result.([]response)
		if !ok {
			t.Fatalf("mixed result type = %T, want []response", result)
		}
		if len(responses) != 8 {
			t.Fatalf("mixed responses = %d, want 8 request responses", len(responses))
		}
		for i, response := range responses {
			wantID := fmt.Sprintf("%d", 2*i+1)
			if response.Error != nil || string(response.ID) != wantID {
				t.Fatalf("mixed response %d = %#v, want id %s", i, response, wantID)
			}
		}
	})
}

func TestProcessBatchDoesNotDispatchOversizedBatch(t *testing.T) {
	tests := []struct {
		name      string
		batchSize int
		wantCalls int
	}{
		{name: "at limit", batchSize: 16, wantCalls: 16},
		{name: "over limit", batchSize: 17, wantCalls: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var batch []json.RawMessage
			if err := json.Unmarshal(testPingBatch(t, test.batchSize, false), &batch); err != nil {
				t.Fatalf("decode test batch: %v", err)
			}

			calls := 0
			processBatch(t.Context(), batch, func(context.Context, []byte) (response, bool) {
				calls++
				return response{}, false
			})
			if calls != test.wantCalls {
				t.Fatalf("dispatch calls = %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestMCPBatchLimitAppliesToHTTPAndStdio(t *testing.T) {
	payload := testPingBatch(t, 17, false)
	server := &Server{}

	t.Run("http", func(t *testing.T) {
		handler := server.HTTPHandler(HTTPOptions{Path: "/mcp"})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("HTTP status = %d, want 200: %s", recorder.Code, recorder.Body.String())
		}
		var response response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode HTTP response: %v", err)
		}
		assertBatchLimitError(t, response)
	})

	t.Run("stdio", func(t *testing.T) {
		var out bytes.Buffer
		if err := server.Serve(t.Context(), strings.NewReader(string(payload)+"\n"), &out); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		responses := parseResponses(t, out.Bytes())
		if len(responses) != 1 {
			t.Fatalf("stdio responses = %d, want one: %s", len(responses), out.String())
		}
		errorValue, ok := responses[0]["error"].(map[string]interface{})
		if !ok {
			t.Fatalf("stdio response omitted error: %#v", responses[0])
		}
		if errorValue["code"] != float64(-32600) || errorValue["message"] != "batch exceeds maximum of 16 requests; split into smaller batches" {
			t.Fatalf("stdio batch error = %#v", errorValue)
		}
		if _, ok := responses[0]["result"]; ok {
			t.Fatalf("stdio oversized batch returned per-request result: %#v", responses[0])
		}
	})
}

func testPingBatch(t *testing.T, count int, mixedNotifications bool) []byte {
	t.Helper()
	requests := make([]map[string]interface{}, 0, count)
	for i := 1; i <= count; i++ {
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "ping",
		}
		if !mixedNotifications || i%2 == 1 {
			request["id"] = i
		}
		requests = append(requests, request)
	}
	payload, err := json.Marshal(requests)
	if err != nil {
		t.Fatalf("marshal ping batch: %v", err)
	}
	return payload
}

func assertBatchLimitError(t *testing.T, got response) {
	t.Helper()
	if got.Error == nil {
		t.Fatalf("oversized batch returned per-request result: %#v", got)
	}
	if got.Error.Code != -32600 || got.Error.Message != "batch exceeds maximum of 16 requests; split into smaller batches" {
		t.Fatalf("batch error = %#v", got.Error)
	}
	if len(got.ID) != 0 || got.Result != nil {
		t.Fatalf("oversized batch returned request identity/result: %#v", got)
	}
}

func TestHTTPHandlerStreamableJSONTransport(t *testing.T) {
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

	server := New(cfg, st)
	httpServer := httptest.NewServer(server.HTTPHandler(HTTPOptions{Path: "/mcp"}))
	defer httpServer.Close()

	initReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initResp, err := http.DefaultClient.Do(initReq)
	if err != nil {
		t.Fatalf("post initialize: %v", err)
	}
	defer func() { _ = initResp.Body.Close() }()
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", initResp.StatusCode)
	}
	if got := initResp.Header.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("stateless transport should not return session id, got %q", got)
	}
	var initBody map[string]interface{}
	if err := json.NewDecoder(initResp.Body).Decode(&initBody); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	result := initBody["result"].(map[string]interface{})
	if result["protocolVersion"] != protocolVersion {
		t.Fatalf("unexpected protocol version: %#v", result["protocolVersion"])
	}

	batchReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`[
		{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}},
		{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}
	]`))
	if err != nil {
		t.Fatalf("new batch request: %v", err)
	}
	batchReq.Header.Set("Content-Type", "application/json")
	batchReq.Header.Set("Accept", "application/json, text/event-stream")
	batchResp, err := http.DefaultClient.Do(batchReq)
	if err != nil {
		t.Fatalf("post batch: %v", err)
	}
	defer func() { _ = batchResp.Body.Close() }()
	if batchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected batch 200, got %d", batchResp.StatusCode)
	}
	var batchBody []map[string]interface{}
	if err := json.NewDecoder(batchResp.Body).Decode(&batchBody); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if len(batchBody) != 1 {
		t.Fatalf("expected only request response in batch, got %#v", batchBody)
	}
	if batchBody[0]["id"].(float64) != 2 {
		t.Fatalf("unexpected batch response id: %#v", batchBody[0])
	}

	notifyReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`))
	if err != nil {
		t.Fatalf("new notification request: %v", err)
	}
	notifyReq.Header.Set("Content-Type", "application/json")
	notifyReq.Header.Set("Accept", "application/json, text/event-stream")
	notifyResp, err := http.DefaultClient.Do(notifyReq)
	if err != nil {
		t.Fatalf("post notification: %v", err)
	}
	defer func() { _ = notifyResp.Body.Close() }()
	if notifyResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected notification 202, got %d", notifyResp.StatusCode)
	}

	getResp, err := http.Get(httpServer.URL + "/mcp")
	if err != nil {
		t.Fatalf("get mcp endpoint: %v", err)
	}
	defer func() { _ = getResp.Body.Close() }()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected GET 405, got %d", getResp.StatusCode)
	}
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	for _, expected := range []string{"dbrain MCP endpoint is reachable", "JSON-RPC over HTTP POST", httpServer.URL + "/mcp", "tools/list"} {
		if !strings.Contains(string(getBody), expected) {
			t.Fatalf("expected GET body to contain %q, got %q", expected, string(getBody))
		}
	}
}

func TestHTTPHandlerRequiresBearerToken(t *testing.T) {
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

	server := New(cfg, st)
	httpServer := httptest.NewServer(server.HTTPHandler(HTTPOptions{
		Path:              "/mcp",
		RequireBearerAuth: true,
		BearerTokenValidator: func(_ context.Context, token string) (bool, error) {
			return token == "secret-token", nil
		},
	}))
	defer httpServer.Close()

	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new missing-token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post missing-token request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer realm="dbrain mcp"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}

	req, err = http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new invalid-token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post invalid-token request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new valid-token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post valid-token request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodOptions, httpServer.URL+"/mcp", nil)
	if err != nil {
		t.Fatalf("new options request: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("options request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("options status = %d, want 204", resp.StatusCode)
	}
}

func TestHTTPHandlerLogsBearerTokenIdentity(t *testing.T) {
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

	created, err := st.CreateMCPBearerToken(context.Background(), "codex")
	if err != nil {
		t.Fatalf("CreateMCPBearerToken: %v", err)
	}

	var accessLog bytes.Buffer
	server := New(cfg, st)
	httpServer := httptest.NewServer(server.HTTPHandler(HTTPOptions{
		Path:                     "/mcp",
		RequireBearerAuth:        true,
		BearerTokenAuthenticator: storeBearerTokenAuthenticator(st),
		LogOutput:                &accessLog,
	}))
	defer httpServer.Close()

	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new valid-token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+created.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post valid-token request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", resp.StatusCode)
	}

	logOutput := accessLog.String()
	for _, value := range []string{
		"mcp request",
		"method=POST",
		"path=/mcp",
		"status=200",
		`auth="bearer"`,
		`token_status="accepted"`,
		`token_name="codex"`,
		fmt.Sprintf(`token_fingerprint="%s"`, created.Record.TokenFingerprint),
	} {
		if !strings.Contains(logOutput, value) {
			t.Fatalf("expected MCP access log to contain %q, got %q", value, logOutput)
		}
	}
	if strings.Contains(logOutput, created.Token) {
		t.Fatalf("MCP access log exposed raw bearer token: %q", logOutput)
	}
}

func TestHTTPHandlerRejectsUntrustedOrigin(t *testing.T) {
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

	server := New(cfg, st)
	httpServer := httptest.NewServer(server.HTTPHandler(HTTPOptions{
		Path:           "/mcp",
		AllowedOrigins: []string{"https://trusted.example"},
	}))
	defer httpServer.Close()

	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post with untrusted origin: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden for untrusted origin, got %d", resp.StatusCode)
	}

	trustedReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new trusted request: %v", err)
	}
	trustedReq.Header.Set("Content-Type", "application/json")
	trustedReq.Header.Set("Origin", "https://trusted.example")
	trustedResp, err := http.DefaultClient.Do(trustedReq)
	if err != nil {
		t.Fatalf("post with trusted origin: %v", err)
	}
	defer func() { _ = trustedResp.Body.Close() }()
	if trustedResp.StatusCode != http.StatusOK {
		t.Fatalf("expected trusted origin 200, got %d", trustedResp.StatusCode)
	}
}

func TestServerAgentResearchWorkflowOverProtocol(t *testing.T) {
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

	ctx := context.Background()
	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-mcp-agent-workflow",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-agent-workflow",
		CanonicalURL: "https://x.com/example/status/test-mcp-agent-workflow",
		Title:        "Mark Carney Saved Evidence",
		Text:         "Mark Carney fiscal policy saved corpus evidence for the MCP agent workflow.",
		ContentHash:  "mcp-agent-workflow-item",
		NotePath:     "items/x/2026/test-mcp-agent-workflow.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(ctx, itemResult.ItemID, "mark-carney, canadian-politics"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-agent-workflow",
		OriginalURL:   "https://example.com/mark-carney-workflow",
		CanonicalURL:  "https://example.com/mark-carney-workflow",
		NormalizedURL: "https://example.com/mark-carney-workflow",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-agent-workflow.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/mark-carney-workflow",
		FinalURL:     "https://example.com/mark-carney-workflow",
		Title:        "Mark Carney Workflow Source",
		Content:      "Linked source evidence about Mark Carney and Canadian fiscal policy.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-fetch",
		ToolVersion:  "test",
	}, "mcp-agent-workflow-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	input := framedJSON(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"dbrain_research_pack","arguments":{"question":"What do I have in my brain about Mark Carney?","limit":2,"include_related":true,"max_chars_per_doc":140}}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"x:test-mcp-agent-workflow","query":"mark carney","content_mode":"evidence","max_chars_per_section":200}}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"dbrain_related","arguments":{"lookup":"x:test-mcp-agent-workflow"}}}`)

	var out bytes.Buffer
	if err := server.Serve(ctx, strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if len(responses) != 5 {
		t.Fatalf("expected 5 MCP responses, got %d", len(responses))
	}
	research := responses[2]["result"].(map[string]interface{})["structuredContent"].(map[string]interface{})
	queryPlan := research["query_plan"].(map[string]interface{})
	tagQueries := queryPlan["tag_queries"].([]interface{})
	if len(tagQueries) != 1 || tagQueries[0] != "mark-carney" {
		t.Fatalf("expected Mark Carney tag alias in research pack, got %#v", queryPlan)
	}
	coverage := research["coverage"].(map[string]interface{})
	exactTags := coverage["exact_tag_matches"].([]interface{})
	if len(exactTags) != 1 {
		t.Fatalf("expected exact tag coverage in research pack, got %#v", coverage)
	}
	exactTag := exactTags[0].(map[string]interface{})
	if exactTag["key"] != "mark-carney" || int(exactTag["count"].(float64)) != 1 {
		t.Fatalf("expected mark-carney exact tag coverage, got %#v", exactTag)
	}
	evidence := research["evidence"].([]interface{})
	if len(evidence) == 0 {
		t.Fatalf("expected research evidence, got %#v", research)
	}
	tagEvidence := research["exact_tag_evidence"].([]interface{})
	if len(tagEvidence) != 1 {
		t.Fatalf("expected representative exact tag evidence, got %#v", research)
	}
	tagExample := tagEvidence[0].(map[string]interface{})
	if tagExample["source_key"] != "x:test-mcp-agent-workflow" || tagExample["user_tags"] != "mark-carney, canadian-politics" {
		t.Fatalf("expected tagged saved item example, got %#v", tagExample)
	}
	tagRetrieval := tagExample["retrieval"].(map[string]interface{})
	signals := tagRetrieval["signals"].([]interface{})
	if len(signals) == 0 || signals[0].(map[string]interface{})["name"] != "exact_user_tag_example" {
		t.Fatalf("expected exact tag retrieval signal, got %#v", tagRetrieval)
	}
	getResult := responses[3]["result"].(map[string]interface{})
	getText := getResult["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(getText, "fiscal policy saved corpus evidence") {
		t.Fatalf("expected DB-backed get evidence, got %q", getText)
	}
	related := responses[4]["result"].(map[string]interface{})["structuredContent"].(map[string]interface{})
	relatedSources := related["related_sources"].([]interface{})
	if len(relatedSources) != 1 {
		t.Fatalf("expected one linked source from related lookup, got %#v", related)
	}
}

func TestServerToolErrorsAreStructuredAndActionable(t *testing.T) {
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

	server := New(cfg, st)
	input := framedJSON(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_missing_tool","arguments":{}}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_ask","arguments":{"question":"removed"}}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"missing:lookup"}}}`)

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}
	assertToolError(t, responses[0], "unknown tool", "tools/list")
	assertToolError(t, responses[1], "unknown tool", "dbrain_research_pack")
	assertToolError(t, responses[2], "lookup not found", "dbrain_search")
}

func TestServerListsResourcesTemplatesAndPrompts(t *testing.T) {
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

	server := New(cfg, st)

	input := framedJSON(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":2,"method":"resources/templates/list","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":3,"method":"prompts/list","params":{}}`)

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if got := len(responses); got != 3 {
		t.Fatalf("expected 3 responses, got %d", got)
	}

	resources := responses[0]["result"].(map[string]interface{})["resources"].([]interface{})
	if len(resources) == 0 {
		t.Fatal("expected non-empty resources list")
	}
	var foundOverview bool
	for _, entry := range resources {
		resource := entry.(map[string]interface{})
		if resource["uri"] == "dbrain://mcp/overview" {
			foundOverview = true
			break
		}
	}
	if !foundOverview {
		t.Fatalf("expected mcp overview resource in %#v", resources)
	}
	templates := responses[1]["result"].(map[string]interface{})["resourceTemplates"].([]interface{})
	if len(templates) == 0 {
		t.Fatal("expected non-empty resource templates list")
	}
	prompts := responses[2]["result"].(map[string]interface{})["prompts"].([]interface{})
	if len(prompts) == 0 {
		t.Fatal("expected non-empty prompts list")
	}
	var foundTopicMap bool
	var foundTopicBrief bool
	var foundEntityBrowse bool
	var foundResearchTemplate bool
	for _, entry := range templates {
		template := entry.(map[string]interface{})
		if template["uriTemplate"] == "dbrain://research/{query}" {
			foundResearchTemplate = true
			break
		}
	}
	if !foundResearchTemplate {
		t.Fatalf("expected research resource template in %#v", templates)
	}
	for _, entry := range prompts {
		prompt := entry.(map[string]interface{})
		if prompt["name"] == "brain_topic_map" {
			foundTopicMap = true
		}
		if prompt["name"] == "brain_topic_brief" {
			foundTopicBrief = true
		}
		if prompt["name"] == "brain_entity_browse" {
			foundEntityBrowse = true
		}
	}
	if !foundTopicMap {
		t.Fatalf("expected brain_topic_map prompt in %#v", prompts)
	}
	if !foundTopicBrief {
		t.Fatalf("expected brain_topic_brief prompt in %#v", prompts)
	}
	if !foundEntityBrowse {
		t.Fatalf("expected brain_entity_browse prompt in %#v", prompts)
	}
}

func TestServerSearchTool(t *testing.T) {
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
		SourceKey:    "x:test-mcp-search",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-search",
		CanonicalURL: "https://x.com/example/status/test-mcp-search",
		Title:        "MCP Search Item",
		Text:         "special mcp search phrase",
		ContentHash:  "mcp-search-hash",
		NotePath:     "items/x/2026/test-mcp-search.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), itemResult.ItemID, "tagmcp, research"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_search","arguments":{"query":"tagmcp","limit":5}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	result := responses[0]["result"].(map[string]interface{})
	if result["isError"] != false {
		t.Fatalf("expected non-error tool result: %#v", result)
	}
	structured := result["structuredContent"].(map[string]interface{})
	if int(structured["count"].(float64)) < 1 {
		t.Fatalf("expected at least one search result: %#v", structured)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "x:test-mcp-search") {
		t.Fatalf("expected search result text to contain source key, got %q", text)
	}
	if !strings.Contains(text, "tagmcp, research") {
		t.Fatalf("expected search result text to contain user tags, got %q", text)
	}
	if strings.Contains(text, cfg.VaultDir) {
		t.Fatalf("search result text exposed absolute vault path: %q", text)
	}
	results := structured["results"].([]interface{})
	first := results[0].(map[string]interface{})
	if first["user_tags"] != "tagmcp, research" {
		t.Fatalf("expected structured user tags, got %#v", first)
	}
}

func TestServerSearchToolReportsExactTagCoverage(t *testing.T) {
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
		SourceKey:    "x:test-mcp-search-tag",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-search-tag",
		CanonicalURL: "https://x.com/example/status/test-mcp-search-tag",
		Title:        "Tagged Search Item",
		Text:         "saved material",
		ContentHash:  "mcp-search-tag-hash",
		NotePath:     "items/x/2026/test-mcp-search-tag.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), itemResult.ItemID, "mark-carney, canadian-politics"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}
	secondResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-search-tag-second",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-search-tag-second",
		CanonicalURL: "https://x.com/example/status/test-mcp-search-tag-second",
		Title:        "Tagged Search Item Second",
		Text:         "second saved material",
		ContentHash:  "mcp-search-tag-hash-second",
		NotePath:     "items/x/2026/test-mcp-search-tag-second.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("upsert second item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), secondResult.ItemID, "canadian-politics, mark-carney"); err != nil {
		t.Fatalf("save second user tags: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_search","arguments":{"query":"Mark Carney","limit":5}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	aliases := structured["tag_aliases"].([]interface{})
	if len(aliases) != 1 || aliases[0] != "mark-carney" {
		t.Fatalf("expected mark-carney tag alias, got %#v", aliases)
	}
	exact := structured["exact_tag_matches"].([]interface{})
	if len(exact) != 1 {
		t.Fatalf("expected exact tag coverage, got %#v", structured)
	}
	bucket := exact[0].(map[string]interface{})
	if bucket["key"] != "mark-carney" || int(bucket["count"].(float64)) != 2 {
		t.Fatalf("unexpected exact tag bucket: %#v", bucket)
	}
	results := structured["results"].([]interface{})
	var foundFirst, foundSecond bool
	for _, result := range results {
		sourceKey, _ := result.(map[string]interface{})["source_key"].(string)
		if sourceKey == "x:test-mcp-search-tag" {
			foundFirst = true
		}
		if sourceKey == "x:test-mcp-search-tag-second" {
			foundSecond = true
		}
	}
	if !foundFirst || !foundSecond {
		t.Fatalf("expected tagged result, got %#v", results)
	}
}

func TestServerSearchToolSurfacesSourceEvidenceWithSourceTypeFilter(t *testing.T) {
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
		SourceKey:    "x:test-mcp-search-source-filter-noise",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-search-source-filter-noise",
		CanonicalURL: "https://x.com/example/status/test-mcp-search-source-filter-noise",
		Title:        "Mark Carney item noise",
		Text:         "Mark Carney item evidence that should be filtered out for web-only search.",
		ContentHash:  "mcp-search-source-filter-noise",
		NotePath:     "items/x/2026/test-mcp-search-source-filter-noise.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	sourceResult, err := st.UpsertSource(context.Background(), model.SourceCandidate{
		SourceKey:     "src:test-mcp-search-source-filter",
		OriginalURL:   "https://example.com/mark-carney-source",
		CanonicalURL:  "https://example.com/mark-carney-source",
		NormalizedURL: "https://example.com/mark-carney-source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-search-source-filter.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/mark-carney-source",
		FinalURL:     "https://example.com/mark-carney-source",
		Title:        "Mark Carney Source Evidence",
		Content:      "Mark Carney source text from a web article.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-extractor",
		ToolVersion:  "test",
	}, "mcp-search-source-filter-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_search","arguments":{"query":"Mark Carney","limit":1,"source_types":["web"]}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	results := structured["results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("expected one web source result, got %#v", structured)
	}
	first := results[0].(map[string]interface{})
	if first["source_key"] != "src:test-mcp-search-source-filter" {
		t.Fatalf("expected source evidence after web filter, got %#v", first)
	}
}

func TestServerGetToolDefaultsToDBEvidence(t *testing.T) {
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
	notePath := "items/x/2026/test-mcp-get.md"
	noteFile := filepath.Join(cfg.VaultDir, filepath.FromSlash(notePath))
	if err := os.MkdirAll(filepath.Dir(noteFile), 0o755); err != nil {
		t.Fatalf("mkdir note: %v", err)
	}
	if err := os.WriteFile(noteFile, []byte("STALE MARKDOWN CONTENT"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-get",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-get",
		CanonicalURL: "https://x.com/example/status/test-mcp-get",
		Title:        "MCP Get Item",
		Text:         strings.Repeat("fresh db evidence ", 20),
		ContentHash:  "mcp-get-hash",
		NotePath:     notePath,
		RawJSON:      strings.Repeat(`{"raw":true}`, 20),
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), itemResult.ItemID, "mark-carney, mcp-test"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"x:test-mcp-get","max_chars_per_section":80}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["content_mode"] != "evidence" {
		t.Fatalf("expected evidence mode, got %#v", structured)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "fresh db evidence") {
		t.Fatalf("expected DB evidence in response text, got %q", text)
	}
	if strings.Contains(text, "STALE MARKDOWN CONTENT") {
		t.Fatalf("default get should not read rendered markdown: %q", text)
	}
	if strings.Contains(text, cfg.VaultDir) {
		t.Fatalf("get result text exposed absolute vault path: %q", text)
	}
	if structured["note"] != notePath {
		t.Fatalf("expected relative note path, got %#v", structured["note"])
	}
	item := structured["item"].(map[string]interface{})
	if _, ok := item["raw_json"]; ok {
		t.Fatalf("expected slim item without raw_json, got %#v", item)
	}
	available := structured["available_sections"].([]interface{})
	if _, ok := available[0].(map[string]interface{})["text"]; ok {
		t.Fatalf("available_sections should not carry section text, got %#v", available[0])
	}
	sections := structured["content_sections"].([]interface{})
	if len(sections) == 0 {
		t.Fatalf("expected content sections, got %#v", structured)
	}
	first := sections[0].(map[string]interface{})
	if first["text"] == "" || !first["truncated"].(bool) {
		t.Fatalf("expected capped section text, got %#v", first)
	}
}

func TestServerGetToolIncludesMediaTranscriptAndOCRSections(t *testing.T) {
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

	ctx := context.Background()
	now := time.Now().UTC()
	mediaItem := model.Item{
		SourceKey:    "x:test-mcp-get-media-enrichments",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-get-media-enrichments",
		CanonicalURL: "https://x.com/example/status/test-mcp-get-media-enrichments",
		Title:        "MCP Get Media Enrichments",
		ContentHash:  "mcp-get-media-enrichments-hash",
		NotePath:     "items/x/2026/test-mcp-get-media-enrichments.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upsert, err := st.UpsertItem(ctx, mediaItem)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.SaveXHydration(ctx, upsert.ItemID, model.XHydration{
		FullText:  "MCP media post",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"snapshot":{
				"id":"test-mcp-get-media-enrichments",
				"text":"MCP media post",
				"media_objects":[
					{"type":"photo","url":"https://pbs.twimg.com/media/mcp-get-media.jpg","expanded_url":"https://x.com/example/status/test-mcp-get-media-enrichments/photo/1","width":1200,"height":800}
				]
			}
		}`,
	}); err != nil {
		t.Fatalf("save x hydration: %v", err)
	}
	mediaItem.ArticleTitle = model.XMediaTranscriptArticleTitle
	mediaItem.ArticleText = "raw video transcript evidence"
	mediaItem.ContentHash = "mcp-get-media-enrichments-transcript-hash"
	mediaItem.UpdatedAt = now
	if _, err := st.UpsertItem(ctx, mediaItem); err != nil {
		t.Fatalf("save transcript text: %v", err)
	}
	if _, err := st.SaveItemOCR(ctx, upsert.ItemID, model.OCRResult{
		Text:      "raw image OCR evidence",
		Status:    model.ItemOCRStatusOK,
		Model:     "test-vision",
		Tool:      "test-ocr",
		FetchedAt: now,
	}, "mcp-get-media-ocr-input"); err != nil {
		t.Fatalf("save item ocr: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, upsert.ItemID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("save x media transcription state: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"x:test-mcp-get-media-enrichments","content_mode":"evidence","max_chars_per_section":500}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	sections := structured["content_sections"].([]interface{})
	var foundTranscript, foundOCR bool
	for _, raw := range sections {
		section := raw.(map[string]interface{})
		switch section["name"] {
		case "x_media_transcript":
			foundTranscript = section["role"] == "raw_transcript" && strings.Contains(section["text"].(string), "raw video transcript evidence")
		case "ocr_text":
			foundOCR = section["role"] == "raw_ocr" && strings.Contains(section["text"].(string), "raw image OCR evidence")
		case "article_text":
			t.Fatalf("transcript-backed article_text should be exposed as x_media_transcript, got %#v", section)
		}
	}
	if !foundTranscript || !foundOCR {
		t.Fatalf("expected transcript and OCR evidence sections, got %#v", sections)
	}
	media := structured["media"].([]interface{})
	if len(media) != 1 {
		t.Fatalf("expected one media ref, got %#v", structured["media"])
	}
	mediaRef := media[0].(map[string]interface{})
	if mediaRef["media_type"] != "photo" || !strings.Contains(mediaRef["remote_url"].(string), "mcp-get-media.jpg") {
		t.Fatalf("unexpected media ref %#v", mediaRef)
	}
}

func TestServerGetToolExpandsQuotedPostFromDB(t *testing.T) {
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
	parent, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:      "x:test-mcp-get-quote-parent",
		SourceType:     "x_bookmark",
		ExternalID:     "test-mcp-get-quote-parent",
		CanonicalURL:   "https://x.com/example/status/test-mcp-get-quote-parent",
		Title:          "Parent post",
		XPostText:      "parent only mentions the thread",
		XPostStatus:    "ok_graphql",
		XPostFetchedAt: now,
		ContentHash:    "mcp-get-quote-parent",
		NotePath:       "items/x/2026/test-mcp-get-quote-parent.md",
		RawJSON:        `{}`,
		ImportedAt:     now,
		UpdatedAt:      now,
		LastSeenAt:     now,
	})
	if err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), parent.ItemID, model.XHydration{
		FullText:  "parent only mentions the thread",
		FetchedAt: now,
		Status:    "ok_graphql",
	}); err != nil {
		t.Fatalf("save parent hydration: %v", err)
	}
	child, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:      "x:test-mcp-get-quote-child",
		SourceType:     "x_quote",
		ExternalID:     "test-mcp-get-quote-child",
		CanonicalURL:   "https://x.com/example/status/test-mcp-get-quote-child",
		Title:          "Quoted post",
		AuthorHandle:   "quotedauthor",
		XPostText:      "quoted post has the important Carney GFANZ context",
		ArticleTitle:   "X Media Transcript",
		ArticleText:    "video transcript mentions Carney climate banking testimony",
		OCRText:        "image OCR mentions GFANZ report title",
		OCRStatus:      "ok",
		XPostStatus:    "ok_graphql",
		XPostFetchedAt: now,
		ContentHash:    "mcp-get-quote-child",
		NotePath:       "items/x/2026/test-mcp-get-quote-child.md",
		RawJSON:        `{}`,
		ImportedAt:     now,
		UpdatedAt:      now,
		LastSeenAt:     now,
	})
	if err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), child.ItemID, model.XHydration{
		FullText:  "quoted post has the important Carney GFANZ context",
		FetchedAt: now,
		Status:    "ok_graphql",
	}); err != nil {
		t.Fatalf("save child hydration: %v", err)
	}
	if _, err := st.ReplaceItemChildLinks(context.Background(), parent.ItemID, "quoted_post", []int64{child.ItemID}); err != nil {
		t.Fatalf("replace quoted link: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"x:test-mcp-get-quote-parent","content_mode":"evidence","max_chars_per_section":1200}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	relatedItems := structured["related_items"].([]interface{})
	if len(relatedItems) != 1 {
		t.Fatalf("expected one related quoted item, got %#v", structured)
	}
	sections := structured["content_sections"].([]interface{})
	var found bool
	for _, section := range sections {
		entry := section.(map[string]interface{})
		if entry["name"] == "quoted_post:x:test-mcp-get-quote-child" {
			text := entry["text"].(string)
			found = strings.Contains(text, "important Carney GFANZ context") &&
				strings.Contains(text, "Media transcript:") &&
				strings.Contains(text, "video transcript mentions Carney climate banking testimony") &&
				strings.Contains(text, "Image OCR:") &&
				strings.Contains(text, "image OCR mentions GFANZ report title")
			break
		}
	}
	if !found {
		t.Fatalf("expected quoted post DB evidence section, got %#v", sections)
	}
}

func TestServerGetToolRenderedModeReadsMarkdown(t *testing.T) {
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
	notePath := "items/x/2026/test-mcp-get-rendered.md"
	noteFile := filepath.Join(cfg.VaultDir, filepath.FromSlash(notePath))
	if err := os.MkdirAll(filepath.Dir(noteFile), 0o755); err != nil {
		t.Fatalf("mkdir note: %v", err)
	}
	if err := os.WriteFile(noteFile, []byte("# Rendered Note\n\nrendered markdown evidence"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-get-rendered",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-get-rendered",
		CanonicalURL: "https://x.com/example/status/test-mcp-get-rendered",
		Title:        "MCP Rendered Get Item",
		Text:         "fresh db text",
		ContentHash:  "mcp-get-rendered-hash",
		NotePath:     notePath,
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"x:test-mcp-get-rendered","content_mode":"rendered"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["content_mode"] != "rendered" {
		t.Fatalf("expected rendered mode, got %#v", structured)
	}
	if structured["content"] != "# Rendered Note\n\nrendered markdown evidence" {
		t.Fatalf("expected rendered content, got %#v", structured["content"])
	}
	sections := structured["content_sections"].([]interface{})
	first := sections[0].(map[string]interface{})
	if first["name"] != "rendered_note" || !strings.Contains(first["text"].(string), "rendered markdown evidence") {
		t.Fatalf("expected rendered note section, got %#v", first)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if strings.Contains(text, cfg.VaultDir) {
		t.Fatalf("rendered get text exposed absolute vault path: %q", text)
	}
}

func TestServerGetToolUsesSlimSourceProjection(t *testing.T) {
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
	sourceResult, err := st.UpsertSource(context.Background(), model.SourceCandidate{
		SourceKey:     "src:test-mcp-get-source",
		OriginalURL:   "https://example.com/source",
		CanonicalURL:  "https://example.com/source",
		NormalizedURL: "https://example.com/source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-get-source.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/source",
		FinalURL:     "https://example.com/source",
		Title:        "MCP Get Source",
		Description:  "source description",
		SiteName:     "Example",
		Content:      strings.Repeat("fresh source extract ", 20),
		RawJSON:      strings.Repeat(`{"extract":true}`, 20),
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-extractor",
		ToolVersion:  "test",
	}, "mcp-get-source-hash"); err != nil {
		t.Fatalf("save extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), sourceResult.SourceID, model.SummaryResult{
		Text:          "short source summary",
		RawJSON:       `{"summary":true}`,
		Model:         "test-model",
		PromptVersion: "test",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "test-summarizer",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	if err := st.SaveSourceUserTags(context.Background(), sourceResult.SourceID, "source-memory, mcp-source"); err != nil {
		t.Fatalf("save source tags: %v", err)
	}
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-get-source-backlink",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-get-source-backlink",
		CanonicalURL: "https://x.com/example/status/test-mcp-get-source-backlink",
		Title:        "MCP Get Source Backlink",
		ContentHash:  "mcp-get-source-backlink-hash",
		NotePath:     "items/x/2026/test-mcp-get-source-backlink.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert backlink item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), itemResult.ItemID, "agent-memory, source-backlink"); err != nil {
		t.Fatalf("save backlink tags: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-get-source",
		OriginalURL:   "https://example.com/source",
		CanonicalURL:  "https://example.com/source",
		NormalizedURL: "https://example.com/source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-get-source.md",
	}); err != nil {
		t.Fatalf("link backlink item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"src:test-mcp-get-source","max_chars_per_section":240}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	source := structured["source"].(map[string]interface{})
	if _, ok := source["extracted_text"]; ok {
		t.Fatalf("expected slim source without extracted_text, got %#v", source)
	}
	if _, ok := source["extract_json"]; ok {
		t.Fatalf("expected slim source without extract_json, got %#v", source)
	}
	if source["user_tags"] != "source-memory, mcp-source" {
		t.Fatalf("expected source tags, got %#v", source["user_tags"])
	}
	backlinks := structured["backlinks"].([]interface{})
	if len(backlinks) != 1 {
		t.Fatalf("expected source backlink, got %#v", backlinks)
	}
	backlink := backlinks[0].(map[string]interface{})
	if backlink["user_tags"] != "agent-memory, source-backlink" {
		t.Fatalf("expected backlink tags, got %#v", backlink)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "User tags: agent-memory, source-backlink") {
		t.Fatalf("expected backlink tags in text output, got %q", text)
	}
	if !strings.Contains(text, "User tags: source-memory, mcp-source") {
		t.Fatalf("expected source tags in text output, got %q", text)
	}
	available := structured["available_sections"].([]interface{})
	if _, ok := available[0].(map[string]interface{})["text"]; ok {
		t.Fatalf("available_sections should not carry section text, got %#v", available[0])
	}
	sections := structured["content_sections"].([]interface{})
	var foundSummary, foundExtract bool
	for _, raw := range sections {
		section := raw.(map[string]interface{})
		switch section["name"] {
		case "summary_text":
			foundSummary = strings.Contains(section["text"].(string), "short source summary")
		case "extracted_text":
			foundExtract = strings.Contains(section["text"].(string), "fresh source extract") && section["truncated"].(bool)
		case "extract_json":
			t.Fatalf("default evidence mode should not include JSON sections: %#v", section)
		}
	}
	if !foundSummary || !foundExtract {
		t.Fatalf("expected summary and capped extract sections, got %#v", sections)
	}
}

func TestServerGetToolUsesQueryWindowForEvidenceSections(t *testing.T) {
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
	sourceResult, err := st.UpsertSource(context.Background(), model.SourceCandidate{
		SourceKey:     "src:test-mcp-get-query-window",
		OriginalURL:   "https://example.com/query-window",
		CanonicalURL:  "https://example.com/query-window",
		NormalizedURL: "https://example.com/query-window",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-get-query-window.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	target := "Mark Carney GFANZ banking context appears here"
	content := strings.Repeat("navigation boilerplate ", 30) + target + " " + strings.Repeat("tail content ", 20)
	if _, err := st.SaveSourceExtraction(context.Background(), sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/query-window",
		FinalURL:     "https://example.com/query-window",
		Title:        "Query Window Source",
		Content:      content,
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-extractor",
		ToolVersion:  "test",
	}, "mcp-get-query-window-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get","arguments":{"lookup":"src:test-mcp-get-query-window","query":"Mark Carney","content_mode":"evidence","max_chars_per_section":120}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["query"] != "Mark Carney" {
		t.Fatalf("expected query echoed in structured payload, got %#v", structured)
	}
	sections := structured["content_sections"].([]interface{})
	var extractText string
	for _, raw := range sections {
		section := raw.(map[string]interface{})
		if section["name"] == "extracted_text" {
			extractText = section["text"].(string)
			if section["truncated"] != true {
				t.Fatalf("expected query-windowed section to be marked truncated, got %#v", section)
			}
			break
		}
	}
	if !strings.Contains(extractText, target) {
		t.Fatalf("expected query window around target, got %q", extractText)
	}
	if strings.HasPrefix(strings.TrimPrefix(extractText, "..."), "navigation boilerplate navigation boilerplate") {
		t.Fatalf("expected query window to skip leading boilerplate, got %q", extractText)
	}
}

func TestServerGetManyToolReturnsBatchPayloadAndPartialErrors(t *testing.T) {
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

	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-mcp-get-many-item",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-get-many-item",
		CanonicalURL: "https://x.com/example/status/test-mcp-get-many-item",
		Title:        "Get Many Item",
		Text:         "item batch evidence",
		ContentHash:  "mcp-get-many-item",
		NotePath:     "items/x/2026/test-mcp-get-many-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	sourceResult, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-mcp-get-many-source",
		OriginalURL:   "https://example.com/get-many-source",
		CanonicalURL:  "https://example.com/get-many-source",
		NormalizedURL: "https://example.com/get-many-source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-get-many-source.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/get-many-source",
		FinalURL:     "https://example.com/get-many-source",
		Title:        "Get Many Source",
		Content:      "source batch evidence",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-extractor",
		ToolVersion:  "test",
	}, "mcp-get-many-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get_many","arguments":{"lookups":["x:test-mcp-get-many-item","src:test-mcp-get-many-source","missing:key"],"content_mode":"evidence","max_chars_per_section":200}}}`

	var out bytes.Buffer
	if err := server.Serve(ctx, strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if got := int(structured["count"].(float64)); got != 2 {
		t.Fatalf("expected 2 successful get_many results, got %#v", structured)
	}
	results := structured["results"].([]interface{})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %#v", results)
	}
	errors := structured["errors"].([]interface{})
	if len(errors) != 1 || errors[0].(map[string]interface{})["lookup"] != "missing:key" {
		t.Fatalf("expected one partial error for missing lookup, got %#v", errors)
	}
	text := result["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "item batch evidence") || !strings.Contains(text, "source batch evidence") || !strings.Contains(text, "missing:key") {
		t.Fatalf("expected combined batch text and partial error, got %q", text)
	}
}

func TestServerGetManyToolUsesQueryWindowForEvidenceSections(t *testing.T) {
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

	ctx := context.Background()
	now := time.Now().UTC()
	itemText := strings.Repeat("Mark Carney policy context ", 25) + strings.Repeat("item boilerplate ", 20) + "GFANZ climate finance evidence" + strings.Repeat(" more tail", 15)
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:test-mcp-get-many-query-window",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-get-many-query-window",
		CanonicalURL: "https://x.com/example/status/test-mcp-get-many-query-window",
		Title:        "Get Many Query Window Item",
		Text:         itemText,
		ContentHash:  "mcp-get-many-query-window-item",
		NotePath:     "items/x/2026/test-mcp-get-many-query-window.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_get_many","arguments":{"lookups":["x:test-mcp-get-many-query-window"],"query":"mark carney gfanz","content_mode":"evidence","max_chars_per_section":90}}}`

	var out bytes.Buffer
	if err := server.Serve(ctx, strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["query"] != "mark carney gfanz" {
		t.Fatalf("expected query echoed in get_many payload, got %#v", structured)
	}
	results := structured["results"].([]interface{})
	first := results[0].(map[string]interface{})
	sections := first["content_sections"].([]interface{})
	var textSection string
	for _, raw := range sections {
		section := raw.(map[string]interface{})
		if section["name"] == "text" {
			textSection = section["text"].(string)
			break
		}
	}
	if !strings.Contains(textSection, "GFANZ climate finance evidence") {
		t.Fatalf("expected get_many query-windowed text section, got %q", textSection)
	}
	if strings.HasPrefix(strings.TrimPrefix(textSection, "..."), "Mark Carney policy context Mark Carney policy context") {
		t.Fatalf("expected get_many query window to prefer the rarer matched term, got %q", textSection)
	}
}

func TestServerEntityMapTool(t *testing.T) {
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
		SourceKey:    "x:test-mcp-entity",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-entity",
		CanonicalURL: "https://x.com/example/status/test-mcp-entity",
		Title:        "MCP Entity Item",
		AuthorHandle: "entitymcp",
		AuthorName:   "Entity MCP",
		Text:         "entity mcp body",
		ContentHash:  "mcp-entity-hash",
		NotePath:     "items/x/2026/test-mcp-entity.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_entity_map","arguments":{"query":"entitymcp","kind":"person","limit":5}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if int(structured["count"].(float64)) != 1 {
		t.Fatalf("expected one entity result, got %#v", structured)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "x-author:entitymcp") {
		t.Fatalf("unexpected entity map text: %q", text)
	}
}

func TestServerRelatedToolForItem(t *testing.T) {
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
		SourceKey:    "x:test-mcp-related",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-related",
		CanonicalURL: "https://x.com/example/status/test-mcp-related",
		Title:        "MCP Related Item",
		ContentHash:  "mcp-related-item-hash",
		NotePath:     "items/x/2026/test-mcp-related.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-related",
		OriginalURL:   "https://example.com/related",
		CanonicalURL:  "https://example.com/related",
		NormalizedURL: "https://example.com/related",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-related.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}
	childResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-related-quote",
		SourceType:   "x_quote",
		ExternalID:   "test-mcp-related-quote",
		CanonicalURL: "https://x.com/example/status/test-mcp-related-quote",
		Title:        "MCP Related Quote",
		ContentHash:  "mcp-related-quote-hash",
		NotePath:     "items/x/2026/test-mcp-related-quote.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert quoted child: %v", err)
	}
	if _, err := st.ReplaceItemChildLinks(context.Background(), itemResult.ItemID, "quoted_post", []int64{childResult.ItemID}); err != nil {
		t.Fatalf("replace quoted child: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_related","arguments":{"lookup":"x:test-mcp-related"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["kind"] != "item" {
		t.Fatalf("expected item related result, got %#v", structured)
	}
	related := structured["related_sources"].([]interface{})
	if len(related) != 1 {
		t.Fatalf("expected 1 related source, got %#v", related)
	}
	relatedItems := structured["related_items"].([]interface{})
	if len(relatedItems) != 1 {
		t.Fatalf("expected 1 related quoted item, got %#v", structured)
	}
	childItem := relatedItems[0].(map[string]interface{})
	if childItem["source_key"] != "x:test-mcp-related-quote" {
		t.Fatalf("expected related child source key, got %#v", childItem)
	}
	if _, ok := childItem["raw_json"]; ok {
		t.Fatalf("expected slim related child item without raw_json, got %#v", childItem)
	}
	if int(structured["count"].(float64)) != 2 {
		t.Fatalf("expected total related count to include sources and child items, got %#v", structured)
	}
	item := structured["item"].(map[string]interface{})
	if _, ok := item["raw_json"]; ok {
		t.Fatalf("expected slim related item without raw_json, got %#v", item)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "Related child items") || !strings.Contains(text, "x:test-mcp-related-quote") {
		t.Fatalf("expected related child in output text, got %q", text)
	}
}

func TestServerStatsItemsTool(t *testing.T) {
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
	for i := 0; i < 2; i++ {
		if _, err := st.UpsertItem(context.Background(), model.Item{
			SourceKey:    fmt.Sprintf("gh-star:test-mcp-stats-items-%d", i),
			SourceType:   "github_star",
			ExternalID:   fmt.Sprintf("test-mcp-stats-items-%d", i),
			CanonicalURL: fmt.Sprintf("https://github.com/test/stats-items-%d", i),
			Title:        "Stats Item",
			ContentHash:  fmt.Sprintf("mcp-stats-items-%d", i),
			NotePath:     fmt.Sprintf("items/github/test-mcp-stats-items-%d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("upsert item %d: %v", i, err)
		}
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_stats_items","arguments":{"source_type":"github_star","group_by":"none"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if got := int(structured["total"].(float64)); got != 2 {
		t.Fatalf("expected total 2, got %#v", structured)
	}
}

func TestServerStatsSourcesTool(t *testing.T) {
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
		SourceKey:    "x:test-mcp-stats-sources",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-stats-sources",
		CanonicalURL: "https://x.com/example/status/test-mcp-stats-sources",
		Title:        "MCP Stats Source Item",
		ContentHash:  "mcp-stats-source-item",
		NotePath:     "items/x/2026/test-mcp-stats-source.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-stats-source",
		OriginalURL:   "https://github.com/test/stats-source",
		CanonicalURL:  "https://github.com/test/stats-source",
		NormalizedURL: "https://github.com/test/stats-source",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-stats-source.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/stats-source",
		FinalURL:     "https://github.com/test/stats-source",
		Title:        "mcp stats source",
		Content:      "repo overview",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "mcp-stats-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_stats_sources","arguments":{"source_type":"github","extract_tool":"github-api","group_by":"extract-status"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if got := int(structured["total"].(float64)); got != 1 {
		t.Fatalf("expected total 1, got %#v", structured)
	}
}

func TestServerWhatsNewToolReturnsEvents(t *testing.T) {
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
		SourceKey:    "x:test-mcp-whats-new",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-whats-new",
		CanonicalURL: "https://x.com/example/status/test-mcp-whats-new",
		Title:        "MCP What's New Item",
		ContentHash:  "mcp-whats-new-item",
		NotePath:     "items/x/2026/test-mcp-whats-new.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_whats_new","arguments":{"since":"24h","limit":5,"types":["imports"]}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	if result["isError"].(bool) {
		t.Fatalf("expected non-error tool result: %#v", result)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "Events:") || !strings.Contains(text, "item_imported") {
		t.Fatalf("expected whats-new digest text with event counts, got %q", text)
	}

	structured := result["structuredContent"].(map[string]interface{})
	events := structured["events"].([]interface{})
	if len(events) == 0 {
		t.Fatalf("expected review events, got %#v", structured)
	}
	counts := structured["counts"].([]interface{})
	if len(counts) == 0 {
		t.Fatalf("expected count buckets, got %#v", structured)
	}
	if structured["next_cursor"] == "" || structured["high_watermark"] == "" {
		t.Fatalf("expected cursor metadata, got %#v", structured)
	}
	first := events[0].(map[string]interface{})
	if first["event_kind"] != store.ReviewEventKindItemImported || first["entity_key"] != "x:test-mcp-whats-new" {
		t.Fatalf("unexpected review event: %#v", first)
	}
	if _, ok := first["tags"].([]interface{}); !ok {
		t.Fatalf("expected tags array in event payload: %#v", first)
	}
	if _, ok := first["reasons"].([]interface{}); !ok {
		t.Fatalf("expected reasons array in event payload: %#v", first)
	}
}

func TestServerWhatsNewToolReturnsEntityView(t *testing.T) {
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
		SourceKey:    "x:test-mcp-whats-new-entity",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-whats-new-entity",
		CanonicalURL: "https://x.com/example/status/test-mcp-whats-new-entity",
		Title:        "MCP What's New Entity",
		ContentHash:  "mcp-whats-new-entity",
		NotePath:     "items/x/2026/test-mcp-whats-new-entity.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_whats_new","arguments":{"since":"24h","limit":5,"view":"entities"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	if result["isError"].(bool) {
		t.Fatalf("expected non-error tool result: %#v", result)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "Entities:") || !strings.Contains(text, "item_imported") {
		t.Fatalf("expected entity digest text, got %q", text)
	}

	structured := result["structuredContent"].(map[string]interface{})
	events := structured["events"].([]interface{})
	if len(events) != 0 {
		t.Fatalf("entity view should suppress raw events, got %#v", events)
	}
	entities := structured["entities"].([]interface{})
	if len(entities) != 1 {
		t.Fatalf("expected grouped entity, got %#v", structured)
	}
	entity := entities[0].(map[string]interface{})
	if entity["entity_key"] != "x:test-mcp-whats-new-entity" || int(entity["event_count"].(float64)) != 1 {
		t.Fatalf("unexpected grouped entity: %#v", entity)
	}
	if _, ok := entity["event_kinds"].([]interface{}); !ok {
		t.Fatalf("expected entity event kinds array: %#v", entity)
	}
}

func TestServerWhatsNewToolReturnsEmptyArrays(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_whats_new","arguments":{"since":"1h"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	events, ok := structured["events"].([]interface{})
	if !ok {
		t.Fatalf("expected events to be an empty array, got %#v", structured["events"])
	}
	if len(events) != 0 {
		t.Fatalf("expected no review events, got %#v", events)
	}
	counts, ok := structured["counts"].([]interface{})
	if !ok {
		t.Fatalf("expected counts to be an empty array, got %#v", structured["counts"])
	}
	if len(counts) != 0 {
		t.Fatalf("expected no count buckets, got %#v", counts)
	}
}

func TestServerWhatsNewToolReportsInvalidFilters(t *testing.T) {
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

	server := New(cfg, st)
	input := framedJSON(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_whats_new","arguments":{}}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_whats_new","arguments":{"since":"24h","types":["mutations"]}}}`)

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	assertToolError(t, responses[0], "exactly one of since or cursor", "Inspect the error message")
	assertToolError(t, responses[1], "unknown review event type", "Inspect the error message")
}

func TestServerStatsBacklogToolReturnsEmptyBucketArrays(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_stats_backlog","arguments":{}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["source_extraction_pending_by_type"] == nil {
		t.Fatalf("expected source_extraction_pending_by_type to be an empty array, got null: %#v", structured)
	}
	if structured["source_summary_pending_by_type"] == nil {
		t.Fatalf("expected source_summary_pending_by_type to be an empty array, got null: %#v", structured)
	}
	if got := len(structured["source_extraction_pending_by_type"].([]interface{})); got != 0 {
		t.Fatalf("expected no extraction buckets, got %d: %#v", got, structured)
	}
	if got := len(structured["source_summary_pending_by_type"].([]interface{})); got != 0 {
		t.Fatalf("expected no summary buckets, got %d: %#v", got, structured)
	}
}

func TestServerTopicMapTool(t *testing.T) {
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
		SourceKey:    "x:test-mcp-topic",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic",
		Title:        "Agent Memory Discussion",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory systems and retrieval",
		ContentHash:  "mcp-topic-item",
		NotePath:     "items/x/2026/test-mcp-topic.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-topic",
		OriginalURL:   "https://github.com/test/agent-memory",
		CanonicalURL:  "https://github.com/test/agent-memory",
		NormalizedURL: "https://github.com/test/agent-memory",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-topic.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/agent-memory",
		FinalURL:     "https://github.com/test/agent-memory",
		Title:        "Agent Memory Repo",
		Content:      "Agent memory frameworks and retrieval tooling.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "mcp-topic-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_topic_map","arguments":{"topic":"agent memory","seed_limit":4,"related_limit":2}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	nodes := structured["nodes"].([]interface{})
	edges := structured["edges"].([]interface{})
	entities := structured["entities"].([]interface{})
	pivots := structured["pivots"].(map[string]interface{})
	synthesis := structured["synthesis"].(map[string]interface{})
	if len(nodes) == 0 || len(edges) == 0 || len(entities) == 0 || len(pivots) == 0 || synthesis["overview"] == "" {
		t.Fatalf("expected non-empty topic map, got %#v", structured)
	}
}

func TestServerTopicBriefTool(t *testing.T) {
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
		SourceKey:    "x:test-mcp-topic-brief",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic-brief",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic-brief",
		Title:        "Vector Search Discussion",
		AuthorHandle: "vectorsearch",
		AuthorName:   "Vector Search",
		Text:         "vector database retrieval and ranking",
		ContentHash:  "mcp-topic-brief-item",
		NotePath:     "items/x/2026/test-mcp-topic-brief.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-topic-brief",
		OriginalURL:   "https://github.com/test/vector-search",
		CanonicalURL:  "https://github.com/test/vector-search",
		NormalizedURL: "https://github.com/test/vector-search",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-topic-brief.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_topic_brief","arguments":{"topic":"vector search","seed_limit":4,"related_limit":2}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if !strings.Contains(structured["summary"].(string), "vector search") {
		t.Fatalf("unexpected topic brief summary: %#v", structured)
	}
	if _, ok := structured["synthesis"].(map[string]interface{}); !ok {
		t.Fatalf("expected synthesis payload in topic brief: %#v", structured)
	}
	if !strings.Contains(structured["markdown"].(string), "## Suggested Starting Notes") {
		t.Fatalf("expected markdown preview in topic brief: %#v", structured)
	}
}

func TestServerResearchPackTool(t *testing.T) {
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
		SourceKey:    "x:test-mcp-research-pack",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-research-pack",
		CanonicalURL: "https://x.com/example/status/test-mcp-research-pack",
		Title:        "Agent Memory Discussion",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory retrieval and persistence",
		ContentHash:  "mcp-research-pack-item",
		NotePath:     "items/x/2026/test-mcp-research-pack.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-research-pack",
		OriginalURL:   "https://github.com/test/agent-memory",
		CanonicalURL:  "https://github.com/test/agent-memory",
		NormalizedURL: "https://github.com/test/agent-memory",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-research-pack.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_research_pack","arguments":{"question":"What is agent memory?","limit":6,"include_related":true}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["schema_version"] != "research_pack.v1" {
		t.Fatalf("expected research pack schema version, got %#v", structured["schema_version"])
	}
	if structured["mode"] != "topic_brief_and_evidence" {
		t.Fatalf("expected topic brief mode, got %#v", structured)
	}
	if structured["topic"] != "agent memory" {
		t.Fatalf("expected inferred topic, got %#v", structured)
	}
	if !structured["used_topic_brief"].(bool) {
		t.Fatalf("expected used_topic_brief=true, got %#v", structured)
	}
	if _, ok := structured["topic_brief"].(map[string]interface{}); !ok {
		t.Fatalf("expected topic_brief payload, got %#v", structured)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "research-runs")); !os.IsNotExist(err) {
		t.Fatalf("dbrain_research_pack must remain trace-free, research-runs stat err=%v", err)
	}
}

func TestResearchPackPlannerModelOMLXPassesThroughMCP(t *testing.T) {
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

	var hit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"concepts\":[{\"key\":\"local_models\",\"preferred\":\"local models\",\"terms\":[\"local models\"],\"required\":true}],\"query_variants\":[{\"query\":\"local models\",\"reason\":\"direct question term\"}]}"}}]}`))
	}))
	defer server.Close()

	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
omlx:
  base_url: `+server.URL+`/v1
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	mcp := New(cfg, st)
	result, err := mcp.toolResearchPack(context.Background(), json.RawMessage(`{
		"question": "What do I know about local models?",
		"limit": 2,
		"max_chars_per_doc": 120,
		"planner_model": "omlx/qwen3.5-coder",
		"use_model_planner": true,
		"include_topic_brief": false,
		"disable_semantic": true
	}`))
	if err != nil {
		t.Fatalf("toolResearchPack: %v", err)
	}
	if !hit {
		t.Fatal("expected oMLX planner endpoint to be called")
	}
	pack, ok := result["structuredContent"].(ResearchPack)
	if !ok {
		t.Fatalf("structuredContent = %#v", result["structuredContent"])
	}
	if pack.SchemaVersion != "research_pack.v1" {
		t.Fatalf("schema = %q", pack.SchemaVersion)
	}
	if pack.QueryPlan.PlannerModel != "omlx/qwen3.5-coder" || pack.QueryPlan.Planner != "model_assisted" {
		t.Fatalf("query plan = %#v", pack.QueryPlan)
	}
}

func TestServerResearchPackInfersCollectorQuestionAndTagAlias(t *testing.T) {
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
	longText := strings.Repeat("central banking and climate finance evidence. ", 40)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-research-carney",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-research-carney",
		CanonicalURL: "https://x.com/example/status/test-mcp-research-carney",
		Title:        "Tagged Saved Evidence",
		Text:         longText,
		ContentHash:  "mcp-research-carney-item",
		NotePath:     "items/x/2026/test-mcp-research-carney.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if err := st.SaveItemUserTags(context.Background(), itemResult.ItemID, "mark-carney, climate-finance"); err != nil {
		t.Fatalf("save user tags: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_research_pack","arguments":{"question":"What do I have in my brain about Mark Carney? Include related evidence and use the mark-carney tag if present.","limit":4,"max_chars_per_doc":120}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["mode"] != "topic_brief_and_evidence" {
		t.Fatalf("expected topic brief mode, got %#v", structured)
	}
	if structured["topic"] != "mark carney" {
		t.Fatalf("expected inferred mark carney topic, got %#v", structured["topic"])
	}
	queryPlan := structured["query_plan"].(map[string]interface{})
	if queryPlan["text_query"] != "mark carney" {
		t.Fatalf("expected text query mark carney, got %#v", queryPlan)
	}
	tags := queryPlan["tag_queries"].([]interface{})
	if len(tags) != 1 || tags[0] != "mark-carney" {
		t.Fatalf("expected mark-carney tag query, got %#v", tags)
	}
	coverage := structured["coverage"].(map[string]interface{})
	topTags := coverage["top_user_tags"].([]interface{})
	if len(topTags) == 0 {
		t.Fatalf("expected top user tags in coverage, got %#v", coverage)
	}
	exactTags := coverage["exact_tag_matches"].([]interface{})
	if len(exactTags) != 1 {
		t.Fatalf("expected exact tag matches in coverage, got %#v", coverage)
	}
	exact := exactTags[0].(map[string]interface{})
	if exact["key"] != "mark-carney" || int(exact["count"].(float64)) != 1 {
		t.Fatalf("unexpected exact tag coverage: %#v", exact)
	}
	if !strings.Contains(coverage["recall_note"].(string), "Returned evidence is a capped working set") {
		t.Fatalf("expected recall note, got %#v", coverage)
	}
	evidence := structured["evidence"].([]interface{})
	if len(evidence) == 0 {
		t.Fatalf("expected evidence, got %#v", structured)
	}
	first := evidence[0].(map[string]interface{})
	if first["user_tags"] != "mark-carney, climate-finance" {
		t.Fatalf("expected user tags in evidence, got %#v", first)
	}
	excerpt := first["excerpt"].(string)
	if len([]rune(excerpt)) > 123 {
		t.Fatalf("expected excerpt capped near max_chars_per_doc, got %d chars: %q", len([]rune(excerpt)), excerpt)
	}
	nextSteps := structured["next_steps"].([]interface{})
	if len(nextSteps) != 2 {
		t.Fatalf("expected get and related next steps, got %#v", nextSteps)
	}
	getStep := nextSteps[0].(map[string]interface{})
	if getStep["action"] != "inspect_top_evidence" {
		t.Fatalf("expected inspect_top_evidence next step, got %#v", getStep)
	}
	getParams := getStep["params"].(map[string]interface{})
	if getParams["lookup"] != "x:test-mcp-research-carney" || getParams["query"] != "mark carney" || getParams["content_mode"] != "evidence" {
		t.Fatalf("expected query-windowed inspect params, got %#v", getParams)
	}
	relatedStep := nextSteps[1].(map[string]interface{})
	if relatedStep["action"] != "expand_related" {
		t.Fatalf("expected expand_related next step, got %#v", relatedStep)
	}
	relatedParams := relatedStep["params"].(map[string]interface{})
	if relatedParams["lookup"] != "x:test-mcp-research-carney" {
		t.Fatalf("expected related lookup for top evidence, got %#v", relatedParams)
	}
}

func TestServerResearchPackSurfacesSourceEvidenceWithSourceTypeFilter(t *testing.T) {
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

	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 13; i++ {
		if _, err := st.UpsertItem(ctx, model.Item{
			SourceKey:    fmt.Sprintf("x:test-mcp-research-source-filter-noise-%02d", i),
			SourceType:   "x_bookmark",
			ExternalID:   fmt.Sprintf("test-mcp-research-source-filter-noise-%02d", i),
			CanonicalURL: fmt.Sprintf("https://x.com/example/status/test-mcp-research-source-filter-noise-%02d", i),
			Title:        "Mark Carney item noise",
			Text:         "Mark Carney item evidence that should not satisfy web-only research.",
			ContentHash:  fmt.Sprintf("mcp-research-source-filter-noise-%02d", i),
			NotePath:     fmt.Sprintf("items/x/2026/test-mcp-research-source-filter-noise-%02d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("upsert item %d: %v", i, err)
		}
	}

	sourceResult, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-mcp-research-source-filter",
		OriginalURL:   "https://example.com/mark-carney-research-source",
		CanonicalURL:  "https://example.com/mark-carney-research-source",
		NormalizedURL: "https://example.com/mark-carney-research-source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-research-source-filter.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/mark-carney-research-source",
		FinalURL:     "https://example.com/mark-carney-research-source",
		Title:        "Mark Carney Research Source",
		Content:      "Mark Carney source evidence from a web article about central banking.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test-extractor",
		ToolVersion:  "test",
	}, "mcp-research-source-filter-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_research_pack","arguments":{"question":"What do I have in my brain about Mark Carney? Include related evidence.","limit":2,"source_types":["web"],"max_chars_per_doc":160}}}`

	var out bytes.Buffer
	if err := server.Serve(ctx, strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["topic"] != "mark carney" {
		t.Fatalf("expected collector question to infer mark carney topic, got %#v", structured["topic"])
	}
	evidence := structured["evidence"].([]interface{})
	if len(evidence) == 0 {
		t.Fatalf("expected web source evidence, got %#v", structured)
	}
	first := evidence[0].(map[string]interface{})
	if first["source_key"] != "src:test-mcp-research-source-filter" || first["kind"] != "source" {
		t.Fatalf("expected direct source evidence after web filter, got %#v", first)
	}
	coverage := structured["coverage"].(map[string]interface{})
	if int(coverage["source_text_matches"].(float64)) == 0 {
		t.Fatalf("expected source text match coverage, got %#v", coverage)
	}
	if int(coverage["topic_node_count"].(float64)) == 0 {
		t.Fatalf("expected source-filtered topic brief to include source nodes, got %#v", coverage)
	}
	topicBrief := structured["topic_brief"].(map[string]interface{})
	nodes := topicBrief["nodes"].([]interface{})
	if len(nodes) == 0 || nodes[0].(map[string]interface{})["source_key"] != "src:test-mcp-research-source-filter" {
		t.Fatalf("expected direct source node in topic brief, got %#v", topicBrief)
	}
}

func TestServerReadItemResource(t *testing.T) {
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
	_, err = st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-resource",
		Title:        "MCP Resource Item",
		Text:         "resource body",
		ContentHash:  "mcp-resource-hash",
		NotePath:     "items/x/2026/test-mcp-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	notePath := filepath.Join(cfg.VaultDir, "items/x/2026/test-mcp-resource.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir note dir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("# Resource Note\n\nfull note body"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://item/x%3Atest-mcp-resource"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	if len(contents) != 1 {
		t.Fatalf("expected 1 resource content, got %#v", contents)
	}
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "MCP Resource Item") || !strings.Contains(text, "full note body") {
		t.Fatalf("unexpected resource text: %q", text)
	}
	if strings.Contains(text, cfg.VaultDir) {
		t.Fatalf("resource text exposed absolute vault path: %q", text)
	}
}

func TestServerReadMCPOverviewResource(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://mcp/overview"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "dbrain_search") || !strings.Contains(text, "brain_topic_map") {
		t.Fatalf("unexpected overview text: %q", text)
	}
}

func TestServerReadStatsItemsResource(t *testing.T) {
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
	for i := 0; i < 2; i++ {
		if _, err := st.UpsertItem(context.Background(), model.Item{
			SourceKey:    fmt.Sprintf("x:test-mcp-stats-resource-%d", i),
			SourceType:   "x_bookmark",
			ExternalID:   fmt.Sprintf("test-mcp-stats-resource-%d", i),
			CanonicalURL: fmt.Sprintf("https://x.com/example/status/test-mcp-stats-resource-%d", i),
			Title:        "MCP Stats Resource Item",
			ContentHash:  fmt.Sprintf("mcp-stats-resource-%d", i),
			NotePath:     fmt.Sprintf("items/x/2026/test-mcp-stats-resource-%d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("upsert item %d: %v", i, err)
		}
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://stats/items?source_type=x_bookmark&group_by=none"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"total": 2`) {
		t.Fatalf("unexpected stats resource text: %q", text)
	}
}

func TestServerReadTopicResource(t *testing.T) {
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
		SourceKey:    "x:test-mcp-topic-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic-resource",
		Title:        "Vector Database Note",
		Text:         "vector database and retrieval",
		ContentHash:  "mcp-topic-resource-item",
		NotePath:     "items/x/2026/test-mcp-topic-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://topic/vector%20database?seed_limit=3&related_limit=1"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"topic": "vector database"`) {
		t.Fatalf("unexpected topic resource text: %q", text)
	}
}

func TestServerReadTopicNoteResource(t *testing.T) {
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
		SourceKey:    "x:test-mcp-topic-note-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic-note-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic-note-resource",
		Title:        "Agent Memory Topic Note",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory retrieval context",
		ContentHash:  "mcp-topic-note-resource-item",
		NotePath:     "items/x/2026/test-mcp-topic-note-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://topic-note/agent%20memory?seed_limit=3&related_limit=1"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "# agent memory") || !strings.Contains(text, "## What This Topic Is") || !strings.Contains(text, "## Suggested Starting Notes") {
		t.Fatalf("unexpected topic-note resource text: %q", text)
	}
}

func TestServerReadEntityResource(t *testing.T) {
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
		SourceKey:    "x:test-mcp-entity-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-entity-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-entity-resource",
		Title:        "MCP Entity Resource Item",
		AuthorHandle: "resourceperson",
		AuthorName:   "Resource Person",
		Text:         "entity resource body",
		ContentHash:  "mcp-entity-resource-hash",
		NotePath:     "items/x/2026/test-mcp-entity-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://entity/resourceperson?kind=person&limit=5"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"key": "x-author:resourceperson"`) {
		t.Fatalf("unexpected entity resource text: %q", text)
	}
}

func TestServerReadResearchResource(t *testing.T) {
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
		SourceKey:    "x:test-mcp-research-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-research-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-research-resource",
		Title:        "Agent Memory Resource",
		Text:         "agent memory resource content",
		ContentHash:  "mcp-research-resource-item",
		NotePath:     "items/x/2026/test-mcp-research-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://research/What%20is%20agent%20memory%3F?limit=5"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"mode": "topic_brief_and_evidence"`) {
		t.Fatalf("unexpected research resource text: %q", text)
	}
}

func TestServerPromptGetBrainResearch(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_research","arguments":{"question":"find vector database tools","source_types":"github,web","include_related":"true"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("expected 1 prompt message, got %#v", messages)
	}
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_research_pack") || !strings.Contains(content, `"github", "web"`) || !strings.Contains(content, "Prioritize accuracy over appearing objective") {
		t.Fatalf("unexpected prompt content: %q", content)
	}
}

func TestServerPromptGetBrainTopicMap(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_topic_map","arguments":{"topic":"agent memory","source_types":"github,web","max_nodes":"5"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_topic_map") || !strings.Contains(content, "dbrain_get") {
		t.Fatalf("unexpected topic map prompt: %q", content)
	}
}

func TestServerPromptGetBrainTopicBrief(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_topic_brief","arguments":{"topic":"agent memory","source_types":"github,web","max_nodes":"5"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_topic_brief") || !strings.Contains(content, "markdown preview") {
		t.Fatalf("unexpected topic brief prompt: %q", content)
	}
}

func TestServerPromptGetBrainEntityBrowse(t *testing.T) {
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

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_entity_browse","arguments":{"query":"openai","kind":"org"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_entity_map") || !strings.Contains(content, "openai") {
		t.Fatalf("unexpected entity prompt: %q", content)
	}
}

func framedJSON(payload string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func lineJSON(payload string) string {
	return payload + "\n"
}

func assertToolError(t *testing.T, response map[string]interface{}, wantMessage string, wantSuggestion string) {
	t.Helper()

	result := response["result"].(map[string]interface{})
	if result["isError"] != true {
		t.Fatalf("expected error tool result, got %#v", result)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, wantMessage) {
		t.Fatalf("expected error text containing %q, got %q", wantMessage, text)
	}
	structured := result["structuredContent"].(map[string]interface{})
	errorPayload := structured["error"].(map[string]interface{})
	message := errorPayload["message"].(string)
	if !strings.Contains(message, wantMessage) {
		t.Fatalf("expected structured error message containing %q, got %q", wantMessage, message)
	}
	suggestions := errorPayload["suggestions"].([]interface{})
	if len(suggestions) == 0 {
		t.Fatalf("expected actionable suggestions, got %#v", errorPayload)
	}
	var found bool
	for _, raw := range suggestions {
		if strings.Contains(raw.(string), wantSuggestion) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected suggestion containing %q, got %#v", wantSuggestion, suggestions)
	}
}

func parseResponses(t *testing.T, data []byte) []map[string]interface{} {
	t.Helper()

	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if bytes.HasPrefix(data, []byte("Content-Length:")) {
		return parseHeaderResponses(t, data)
	}

	var responses []map[string]interface{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		responses = append(responses, decoded)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan responses: %v", err)
	}
	return responses
}

func parseHeaderResponses(t *testing.T, data []byte) []map[string]interface{} {
	t.Helper()

	var responses []map[string]interface{}
	for len(data) > 0 {
		sep := []byte("\r\n\r\n")
		idx := bytes.Index(data, sep)
		if idx < 0 {
			t.Fatalf("missing frame separator in %q", string(data))
		}
		headers := string(data[:idx])
		var length int
		for _, line := range strings.Split(headers, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")), "%d", &length); err != nil {
					t.Fatalf("parse content length: %v", err)
				}
			}
		}
		start := idx + len(sep)
		end := start + length
		if end > len(data) {
			t.Fatalf("short frame: want %d bytes, have %d", length, len(data)-start)
		}
		payload := data[start:end]
		var decoded map[string]interface{}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		responses = append(responses, decoded)
		data = data[end:]
	}
	return responses
}

func TestReadNoteHelper(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	const content = "hello"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	got, err := readNote(path)
	if err != nil {
		t.Fatalf("readNote: %v", err)
	}
	if got != content {
		t.Fatalf("unexpected note content: %q", got)
	}
}
