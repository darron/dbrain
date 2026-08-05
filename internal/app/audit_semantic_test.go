package app

import (
	"context"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/testsupport/storefixture"
)

func TestAuditSemanticInspectorUsesClosedConfigCapabilityAndBoundedRuntimeTruth(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	baseConfig := semanticconfig.Config{
		Mode: semanticconfig.ModeOn, Provider: semanticconfig.ProviderOllama, Model: "nomic-embed-text",
		Dimensions: 768, IndexBackend: semanticconfig.IndexBackendExact, ExactFallbackMaxChunks: 25_000,
	}
	expectedProfile := semanticbuild.Profile(embedding.Info{Provider: "ollama", Model: "nomic-embed-text", Dimensions: 768})
	expectedProfileID, err := expectedProfile.ID()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name              string
		config            semanticconfig.Config
		capability        semanticindex.Capability
		runtime           semanticreadiness.Snapshot
		wantConfigured    bool
		wantCapability    bool
		wantBackend       audit.SemanticBackend
		wantReadiness     audit.SemanticReadiness
		wantRuntimeCalled bool
	}{
		{
			name: "disabled", config: func() semanticconfig.Config { value := baseConfig; value.Mode = semanticconfig.ModeOff; return value }(),
			capability:     semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: "usearch", Version: "2.26.0"},
			wantConfigured: true, wantBackend: "none", wantReadiness: "disabled",
		},
		{
			name: "unsupported", config: baseConfig,
			capability:     semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantConfigured: true, wantBackend: "unsupported", wantReadiness: "unavailable",
		},
		{
			name: "supported ready", config: baseConfig,
			capability: semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: "usearch", Version: "2.26.0"},
			runtime: semanticreadiness.Snapshot{
				Available: true, ProfileID: expectedProfileID, ProfileExists: true, ProfileProvenanceValid: true,
				GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1, LatestRevision: 1, ObservedLatestRevision: 1,
				ActiveGenerationID: "generation-current", ActiveGenerationValid: true,
				ActiveGenerationBackend: "usearch", ActiveGenerationBackendVersion: "2.26.0",
				ActiveIndexedCount: 100, L0ReadyCount: 2, ObservedL0ReadyCount: 2,
				ActiveTombstones: 1, ActiveSegmentCount: 7,
			},
			wantConfigured: true, wantCapability: true, wantBackend: "ollama", wantReadiness: "ready", wantRuntimeCalled: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtimeCalled := false
			inspector := auditSemanticInspector{
				rootDir: "ignored", now: func() time.Time { return now },
				resolveDiagnostic: func(string) (semanticconfig.Config, error) { return test.config, nil },
				capability:        func() semanticindex.Capability { return test.capability },
				readRuntime: func(_ context.Context, profile embedding.Profile, exactMaxChunks int, gotNow time.Time) (semanticreadiness.Snapshot, error) {
					runtimeCalled = true
					if profile != expectedProfile || exactMaxChunks != 25_000 || !gotNow.Equal(now) {
						t.Fatalf("bounded runtime args profile=%#v max=%d now=%s", profile, exactMaxChunks, gotNow)
					}
					return test.runtime, nil
				},
			}
			got, err := inspector.InspectAuditSemantic(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got.Configured != test.wantConfigured || got.CapabilityAvailable != test.wantCapability || got.Backend != test.wantBackend || got.Readiness != test.wantReadiness || runtimeCalled != test.wantRuntimeCalled {
				t.Fatalf("semantic inspection=%#v runtime_called=%t", got, runtimeCalled)
			}
			if test.wantRuntimeCalled {
				if string(got.ProfileID) != expectedProfileID || got.ActiveGenerationID != "generation-current" || got.IndexedVectorCount != 100 || got.L0VectorCount != 2 || got.TombstoneCount != 1 || got.SegmentCount != 7 {
					t.Fatalf("bounded semantic runtime evidence=%#v", got)
				}
			}
		})
	}
}

func TestBuildAuditDependenciesBindsSemanticInspectorOnlyToPinnedSnapshot(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st := storefixture.OpenCurrent(t, cfg.DBPath)
	defer func() { _ = st.Close() }()
	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()

	request := audit.Request{Profile: audit.ProfileStandard, CheckIDs: []audit.CheckID{audit.CheckSemanticCurrentReadiness}}
	withoutSnapshot, err := buildAuditDependencies(t.Context(), cfg, nil, request, audit.Features{})
	if err != nil {
		t.Fatal(err)
	}
	withSnapshot, err := buildAuditDependencies(t.Context(), cfg, snapshot, request, audit.Features{})
	if err != nil {
		t.Fatal(err)
	}
	if withoutSnapshot.Semantic != nil || withSnapshot.Semantic == nil {
		t.Fatalf("semantic inspectors without=%#v with=%#v", withoutSnapshot.Semantic, withSnapshot.Semantic)
	}
	if !auditNeedsSnapshot(request) {
		t.Fatal("semantic readiness scope did not request a pinned database snapshot")
	}
}

func TestAuditSemanticInspectorFailsClosedOnInvalidActiveGenerationIdentifier(t *testing.T) {
	semantic := semanticconfig.Config{
		Mode: semanticconfig.ModeOn, Provider: semanticconfig.ProviderOllama, Model: "nomic-embed-text",
		Dimensions: 768, IndexBackend: semanticconfig.IndexBackendExact, ExactFallbackMaxChunks: 25_000,
	}
	profile := semanticbuild.Profile(embedding.Info{Provider: "ollama", Model: semantic.Model, Dimensions: semantic.Dimensions})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	inspector := auditSemanticInspector{
		resolveDiagnostic: func(string) (semanticconfig.Config, error) { return semantic, nil },
		capability: func() semanticindex.Capability {
			return semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: "usearch", Version: "2.26.0"}
		},
		readRuntime: func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
			return semanticreadiness.Snapshot{
				Available: true, ProfileID: profileID, ProfileExists: true, ProfileProvenanceValid: true,
				ActiveGenerationID: "/private/generation", ActiveGenerationValid: true,
				ActiveGenerationBackend: "usearch", ActiveGenerationBackendVersion: "2.26.0",
			}, nil
		},
	}
	got, err := inspector.InspectAuditSemantic(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveGenerationID != "" || got.Readiness != "corrupt" {
		t.Fatalf("invalid active generation did not fail closed: %#v", got)
	}
}

func TestResolveAuditRuntimeSetsSemanticRequirednessFromEnabledConfigAndCapability(t *testing.T) {
	t.Setenv("DBRAIN_RESEARCH_SEMANTIC_MODE", "on")
	t.Setenv("DBRAIN_RESEARCH_SEMANTIC_MODEL", "nomic-embed-text")
	t.Setenv("DBRAIN_RESEARCH_SEMANTIC_DIMENSIONS", "768")
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveAuditRuntime(cfg, auditConfigMeta{Layout: "explicit_root", Source: "flag"})
	if err != nil {
		t.Fatal(err)
	}
	wantCapability := semanticindex.RuntimeCapability().State == semanticindex.CapabilitySupportedReady
	if !resolved.Features.SemanticConfigured || resolved.Features.SemanticCapabilityAvailable != wantCapability {
		t.Fatalf("semantic audit features=%#v want capability=%t", resolved.Features, wantCapability)
	}
}

func TestScheduledAuditRunnerBindsRealSemanticInspector(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	storefixture.PrepareCurrent(t, cfg.DBPath)
	features := audit.Features{Layout: "explicit_root", ConfigSource: "flag", ConfigVerified: true, Sources: map[audit.Source]bool{}, Stages: map[audit.PipelineStage]bool{}}
	runner, err := newScheduledAuditRunner(t.Context(), cfg, features)
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner(t.Context(), audit.ProfileFast, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range report.Checks {
		if check.ID == audit.CheckSemanticCurrentReadiness {
			if check.ErrorCode != "" || check.Evidence["capability"] != "disabled" {
				t.Fatalf("scheduled semantic check did not use real inspector: %#v", check)
			}
			return
		}
	}
	t.Fatal("scheduled report omitted semantic readiness")
}
