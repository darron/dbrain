package semanticbuild

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

func TestSemanticStatusConfiguredLoadsSelectedProfileLatestRun(t *testing.T) {
	now := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	profile := Profile(embedding.Info{Provider: "fake", Model: "model", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	latest := &store.SemanticRefreshRun{
		RunID:          "run-profile",
		ProfileID:      profileID,
		State:          store.SemanticRefreshRunFailed,
		Stage:          store.SemanticRefreshEmbedding,
		Checkpoint:     "embedding:revision=9",
		ErrorCode:      "semantic_embedding_failed",
		ErrorText:      `provider body {"vectors":[0.1]} /Users/alice/private/cache`,
		LastProgressAt: now.Add(-time.Minute),
	}
	st := &refreshStatusStore{
		snapshot: semanticreadiness.Snapshot{Available: true},
		latest:   latest,
	}
	status, err := ReadStatus(
		t.Context(),
		st,
		profile,
		true,
		true,
		25_000,
		semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.latestFilters) != 1 || st.latestFilters[0] != profileID {
		t.Fatalf("latest filters=%v want selected profile %q", st.latestFilters, profileID)
	}
	if status.LatestRun == nil ||
		status.LatestRun.RunID != latest.RunID ||
		status.LatestRun.State != store.SemanticRefreshRunFailed ||
		status.LatestRun.Stage != store.SemanticRefreshEmbedding ||
		status.LatestRun.Checkpoint != latest.Checkpoint ||
		status.LatestRun.ErrorCode != latest.ErrorCode ||
		!status.LatestRun.LastProgressAt.Equal(latest.LastProgressAt) {
		t.Fatalf("latest run=%+v want bounded selected run %+v", status.LatestRun, latest)
	}
	if status.LatestRun.ErrorText != "" {
		t.Fatalf("latest run exposed stored error text %q", status.LatestRun.ErrorText)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"provider body", `"[0.1]"`, "/Users/alice", `"history"`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("status JSON leaked %q: %s", forbidden, payload)
		}
	}
	if strings.Count(string(payload), latest.RunID) != 1 {
		t.Fatalf("status JSON does not contain one bounded latest object: %s", payload)
	}
}

func TestSemanticStatusOffOrUnconfiguredLoadsDatabaseLatestRun(t *testing.T) {
	now := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	latest := &store.SemanticRefreshRun{
		RunID:          "run-database-latest",
		ProfileID:      "earlier-profile",
		State:          store.SemanticRefreshRunCancelled,
		Stage:          store.SemanticRefreshProjection,
		LastProgressAt: now,
	}
	profile := Profile(embedding.Info{Provider: "fake", Model: "model", Dimensions: 2})
	for _, test := range []struct {
		name       string
		profile    embedding.Profile
		configured bool
		snapshot   semanticreadiness.Snapshot
		wantReads  int
	}{
		{name: "unconfigured", wantReads: 0},
		{
			name:       "configured off",
			profile:    profile,
			configured: true,
			snapshot:   semanticreadiness.Snapshot{Available: true},
			wantReads:  1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := &refreshStatusStore{latest: latest, snapshot: test.snapshot}
			status, err := ReadStatus(
				t.Context(),
				st,
				test.profile,
				test.configured,
				false,
				25_000,
				semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
				now,
			)
			if err != nil {
				t.Fatal(err)
			}
			if st.readinessCalls != test.wantReads {
				t.Fatalf("status made %d readiness calls want %d", st.readinessCalls, test.wantReads)
			}
			if len(st.latestFilters) != 1 || st.latestFilters[0] != "" {
				t.Fatalf("latest filters=%v want one empty database filter", st.latestFilters)
			}
			if status.LatestRun == nil || status.LatestRun.RunID != latest.RunID {
				t.Fatalf("latest run=%+v want database latest %+v", status.LatestRun, latest)
			}
		})
	}
}

func TestSemanticStatusNoLatestRunEncodesExplicitNull(t *testing.T) {
	status, err := ReadStatus(
		t.Context(),
		&refreshStatusStore{},
		embedding.Profile{},
		false,
		false,
		25_000,
		semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
		time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"latest_run":null`) {
		t.Fatalf("status JSON=%s want explicit latest_run null", payload)
	}
	if strings.Contains(string(payload), `"history"`) {
		t.Fatalf("status JSON unexpectedly contains history: %s", payload)
	}
}

func TestSemanticStatusLatestRunFailurePreservesStatusErrorBehavior(t *testing.T) {
	want := errors.New("latest refresh unavailable")
	_, err := ReadStatus(
		t.Context(),
		&refreshStatusStore{latestErr: want},
		embedding.Profile{},
		false,
		false,
		25_000,
		semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
		time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC),
	)
	if !errors.Is(err, want) {
		t.Fatalf("error=%v want %v", err, want)
	}
}

type refreshStatusStore struct {
	snapshot       semanticreadiness.Snapshot
	readinessErr   error
	readinessCalls int
	latest         *store.SemanticRefreshRun
	latestErr      error
	latestFilters  []string
}

func (s *refreshStatusStore) SemanticReadinessSnapshotAt(
	context.Context,
	embedding.Profile,
	int,
	time.Time,
) (semanticreadiness.Snapshot, error) {
	s.readinessCalls++
	return s.snapshot, s.readinessErr
}

func (s *refreshStatusStore) LatestSemanticRefreshRun(
	_ context.Context,
	profileID string,
) (*store.SemanticRefreshRun, error) {
	s.latestFilters = append(s.latestFilters, profileID)
	return s.latest, s.latestErr
}
