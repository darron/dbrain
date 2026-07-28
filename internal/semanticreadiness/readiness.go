package semanticreadiness

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

type State string

const (
	StateNotConfigured   State = "not_configured"
	StateDisabled        State = "disabled"
	StateNeedsProjection State = "needs_projection"
	StateNeedsEmbeddings State = "needs_embeddings"
	StateRetryScheduled  State = "retry_scheduled"
	StateNeedsIndex      State = "needs_index"
	StateBuilding        State = "building"
	StateCatchingUp      State = "catching_up"
	StateDegradedBlocked State = "degraded_blocked"
	StateStale           State = "stale"
	StateCorrupt         State = "corrupt"
	StateReady           State = "ready"
	StateUnavailable     State = "unavailable"
)

const (
	MaxDirtyParents       = 500
	MaxNotReadyChunks     = 2_500
	MaxDirtyAge           = 30 * time.Minute
	ReadyL0Target         = 5_000
	CatchUpL0Limit        = 10_000
	DefaultExactMaxChunks = 25_000
)

type Decision struct {
	State      State  `json:"state"`
	Reason     string `json:"reason"`
	Searchable bool   `json:"searchable"`
}

type DirtyParent struct {
	Parent                retrievalchunk.Parent `json:"-"`
	LastCurrentChunkCount int                   `json:"last_current_chunk_count"`
}

// Snapshot is a complete, immutable view of readiness facts. Configuration
// flags are supplied by the caller; every storage fact is read by Store from
// one SQLite read transaction.
type Snapshot struct {
	Configured    bool   `json:"configured"`
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	PlanningError string `json:"planning_error,omitempty"`
	// AggregateCountersCorrupt is set when an authoritative maintenance scan
	// disproves the transactionally maintained runtime counters, or when the
	// runtime path observes a structurally impossible counter combination.
	AggregateCountersCorrupt bool   `json:"aggregate_counters_corrupt,omitempty"`
	ProfileID                string `json:"profile_id"`

	ProfileExists           bool      `json:"profile_exists"`
	ProfileProvenanceValid  bool      `json:"profile_provenance_valid"`
	ExpectedParents         int       `json:"expected_parents"`
	CurrentParents          int       `json:"current_parents"`
	EmptyParents            int       `json:"empty_parents"`
	PendingParents          int       `json:"pending_parents"`
	BlockedParents          int       `json:"blocked_parents"`
	ErrorParents            int       `json:"error_parents"`
	DirtyParents            int       `json:"dirty_parents"`
	EstimatedNotReadyChunks int       `json:"estimated_not_ready_chunks"`
	OldestDirtyAt           time.Time `json:"oldest_dirty_at,omitempty"`

	ChunkableParents       int `json:"chunkable_parents"`
	ParentsWithReadyChunk  int `json:"parents_with_ready_chunk"`
	ChunkCount             int `json:"chunk_count"`
	ReadyEmbeddings        int `json:"ready_embeddings"`
	PendingEmbeddings      int `json:"pending_embeddings"`
	BlockedEmbeddings      int `json:"blocked_embeddings"`
	ErrorEmbeddings        int `json:"error_embeddings"`
	DueRetries             int `json:"due_retries"`
	ScheduledRetries       int `json:"scheduled_retries"`
	UnclassifiedErrors     int `json:"unclassified_errors"`
	CorruptEmbeddings      int `json:"corrupt_embeddings"`
	RevisionZeroEmbeddings int `json:"revision_zero_embeddings"`

	GlobalPurgeEpoch                     int64     `json:"global_purge_epoch"`
	ProfilePurgeEpoch                    int64     `json:"profile_purge_epoch"`
	LatestRevision                       int64     `json:"latest_revision"`
	ObservedLatestRevision               int64     `json:"observed_latest_revision"`
	L0ReadyCount                         int       `json:"l0_ready_count"`
	ObservedL0ReadyCount                 int       `json:"observed_l0_ready_count"`
	ActiveIndexedCount                   int       `json:"active_indexed_count"`
	ActiveTombstones                     int       `json:"active_tombstones"`
	ActiveGenerationID                   string    `json:"active_generation_id"`
	ActiveGenerationValid                bool      `json:"active_generation_valid"`
	ActiveSnapshotRevision               int64     `json:"active_snapshot_revision"`
	ActiveGenerationBackend              string    `json:"active_generation_backend,omitempty"`
	ActiveGenerationBackendVersion       string    `json:"active_generation_backend_version,omitempty"`
	ActiveGenerationDistanceMetric       string    `json:"active_generation_distance_metric,omitempty"`
	ActiveGenerationDimensions           int       `json:"active_generation_dimensions,omitempty"`
	ActiveGenerationRootDescriptorSHA256 string    `json:"active_generation_root_descriptor_sha256,omitempty"`
	ActiveGenerationProblem              string    `json:"active_generation_problem,omitempty"`
	BuildingGenerations                  int       `json:"building_generations"`
	StaleGenerations                     int       `json:"stale_generations"`
	ErrorGenerations                     int       `json:"error_generations"`
	ExactMaxChunks                       int       `json:"exact_max_chunks"`
	Now                                  time.Time `json:"-"`
}

func Evaluate(s Snapshot) Decision {
	decision := func(state State, reason string, searchable bool) Decision {
		return Decision{State: state, Reason: reason, Searchable: searchable}
	}
	if !s.Configured {
		return decision(StateNotConfigured, "embedding model and positive dimensions are required", false)
	}
	if !s.Enabled {
		return decision(StateDisabled, "semantic retrieval mode is off", false)
	}
	if !s.Available {
		return decision(StateUnavailable, "retrieval schema is unavailable", false)
	}
	if s.PlanningError != "" {
		return decision(StateUnavailable, "semantic readiness planning failed: "+s.PlanningError, false)
	}
	if s.AggregateCountersCorrupt {
		return decision(StateCorrupt, "semantic runtime aggregate counters are inconsistent", false)
	}
	if s.ProfileExists && (!s.ProfileProvenanceValid || s.ProfilePurgeEpoch != s.GlobalPurgeEpoch || s.RevisionZeroEmbeddings > 0 || s.CorruptEmbeddings > 0 || s.ObservedLatestRevision > s.LatestRevision || s.L0ReadyCount != s.ObservedL0ReadyCount) {
		return decision(StateCorrupt, "semantic profile provenance, revision, or aggregate counters are inconsistent", false)
	}
	if s.ActiveGenerationID != "" && !s.ActiveGenerationValid {
		reason := "active semantic generation provenance is unproven"
		if s.ActiveGenerationProblem != "" {
			reason += ": " + s.ActiveGenerationProblem
		}
		return decision(StateCorrupt, reason, false)
	}
	if s.ErrorGenerations > 0 {
		return decision(StateCorrupt, "semantic index generation failed and requires explicit repair", false)
	}
	if s.ActiveGenerationID != "" && (s.L0ReadyCount > CatchUpL0Limit || !ratioAtMost(s.ActiveTombstones, max(s.ActiveIndexedCount, 1), 2, 100)) {
		return decision(StateNeedsIndex, "active semantic generation exceeds the L0 or tombstone safety limit", false)
	}
	if s.UnclassifiedErrors > 0 || s.ErrorParents > 0 {
		return decision(StateDegradedBlocked, "semantic work contains an unclassified or terminal error", false)
	}
	if s.BlockedParents > 0 || !ratioAtMost(s.BlockedEmbeddings, s.ChunkCount, 1, 1000) {
		return decision(StateDegradedBlocked, "terminal blocked coverage exceeds the readiness gate", false)
	}
	complete := projectionComplete(s) && embeddingsComplete(s) && coverageReady(s)
	exactCap := EffectiveExactMaxChunks(s.ExactMaxChunks)
	if complete {
		if s.ActiveGenerationID == "" && s.ReadyEmbeddings > exactCap {
			return decision(StateNeedsIndex, fmt.Sprintf("%d ready embeddings exceed exact-search cap %d", s.ReadyEmbeddings, exactCap), false)
		}
		if s.ActiveGenerationID != "" && (s.L0ReadyCount > ReadyL0Target || !ratioAtMost(s.ActiveTombstones, max(s.ActiveIndexedCount, 1), 1, 100)) {
			return decision(StateCatchingUp, "active generation is valid with bounded L0 or tombstone debt", true)
		}
		return decision(StateReady, "semantic profile is complete and eligible", true)
	}
	if catchUpEligible(s, exactCap) {
		return decision(StateCatchingUp, "semantic profile has bounded projection or embedding debt", true)
	}
	if s.BuildingGenerations > 0 {
		return decision(StateBuilding, "semantic index generation is building", false)
	}
	if s.StaleGenerations > 0 {
		return decision(StateStale, "semantic index generation is stale", false)
	}
	if s.PendingParents > 0 || s.DirtyParents > 0 || s.CurrentParents+s.EmptyParents < s.ExpectedParents {
		return decision(StateNeedsProjection, "projection work exceeds the bounded catch-up gate", false)
	}
	if s.ScheduledRetries > 0 && s.DueRetries == 0 {
		return decision(StateRetryScheduled, "embedding retry is scheduled outside the bounded catch-up gate", false)
	}
	if s.PendingEmbeddings > 0 || s.ErrorEmbeddings > 0 || s.DueRetries > 0 || !s.ProfileExists {
		return decision(StateNeedsEmbeddings, "embedding work exceeds the bounded catch-up gate", false)
	}
	if !coverageReady(s) {
		return decision(StateDegradedBlocked, "terminal blocked or parent coverage exceeds the readiness gate", false)
	}
	return decision(StateNeedsIndex, "semantic profile requires a validated index", false)
}

// EffectiveExactMaxChunks applies the measured foundation safety ceiling. The
// configured value may reduce exact-search admission, but it cannot expand the
// maximum corpus that the runtime will scan without a validated ANN root.
func EffectiveExactMaxChunks(configured int) int {
	if configured <= 0 || configured > DefaultExactMaxChunks {
		return DefaultExactMaxChunks
	}
	return configured
}

func projectionComplete(s Snapshot) bool {
	return s.ExpectedParents == s.CurrentParents+s.EmptyParents && s.PendingParents == 0 && s.DirtyParents == 0 && s.BlockedParents == 0 && s.ErrorParents == 0
}

func embeddingsComplete(s Snapshot) bool {
	return s.ProfileExists && s.PendingEmbeddings == 0 && s.ErrorEmbeddings == 0 && s.DueRetries == 0 && s.ScheduledRetries == 0 && s.ReadyEmbeddings+s.BlockedEmbeddings == s.ChunkCount
}

func coverageReady(s Snapshot) bool {
	return ratioAtLeast(s.ReadyEmbeddings, s.ChunkCount, 999, 1000) && ratioAtMost(s.BlockedEmbeddings, s.ChunkCount, 1, 1000) && ratioAtLeast(s.ParentsWithReadyChunk, s.ChunkableParents, 999, 1000)
}

func catchUpEligible(s Snapshot, exactCap int) bool {
	if s.ChunkCount > exactCap || max(s.DirtyParents, s.PendingParents) > MaxDirtyParents || s.EstimatedNotReadyChunks > MaxNotReadyChunks || s.EstimatedNotReadyChunks < 0 {
		return false
	}
	if !s.OldestDirtyAt.IsZero() && s.Now.Sub(s.OldestDirtyAt) > MaxDirtyAge {
		return false
	}
	if s.ActiveGenerationID == "" {
		if s.ReadyEmbeddings == 0 || s.ReadyEmbeddings+s.EstimatedNotReadyChunks > exactCap {
			return false
		}
	} else if !s.ActiveGenerationValid || s.L0ReadyCount > CatchUpL0Limit || !ratioAtMost(s.ActiveTombstones, max(s.ActiveIndexedCount, 1), 2, 100) {
		return false
	}
	return s.PendingParents > 0 || s.DirtyParents > 0 || s.PendingEmbeddings > 0 || s.DueRetries > 0 || s.ScheduledRetries > 0
}

func ratioAtLeast(numerator, denominator, wantNumerator, wantDenominator int) bool {
	if denominator == 0 {
		return numerator == 0
	}
	return int64(numerator)*int64(wantDenominator) >= int64(denominator)*int64(wantNumerator)
}

func ratioAtMost(numerator, denominator, wantNumerator, wantDenominator int) bool {
	if denominator == 0 {
		return numerator == 0
	}
	return int64(numerator)*int64(wantDenominator) <= int64(denominator)*int64(wantNumerator)
}

// EstimateDirtyParents performs exact v3 occurrence planning but stops at the
// first value beyond limit. Returning limit+1 is a stable over-budget sentinel.
func EstimateDirtyParents(ctx context.Context, parents []DirtyParent, limit int) (int, error) {
	next := 0
	return EstimateDirtyParentStream(ctx, limit, func() (DirtyParent, bool, error) {
		if next >= len(parents) {
			return DirtyParent{}, false, nil
		}
		parent := parents[next]
		next++
		return parent, true, nil
	})
}

// EstimateDirtyParentStream loads and plans one parent at a time. It stops
// calling next as soon as the over-budget sentinel is known, so a fresh corpus
// does not materialize every dirty parent merely to decide admission.
func EstimateDirtyParentStream(ctx context.Context, limit int, next func() (DirtyParent, bool, error)) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("readiness occurrence limit must not be negative")
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		dirty, ok, err := next()
		if err != nil {
			return 0, err
		}
		if !ok {
			return total, nil
		}
		remaining := limit - total
		if remaining < 0 {
			return limit + 1, nil
		}
		count, err := retrievalchunk.CountOccurrencesCappedContext(ctx, dirty.Parent, retrievalchunk.DefaultOptions(), remaining)
		if err != nil {
			return 0, fmt.Errorf("plan dirty parent %s %s: %w", dirty.Parent.Kind, dirty.Parent.SourceKey, err)
		}
		if dirty.LastCurrentChunkCount > count {
			count = dirty.LastCurrentChunkCount
		}
		if count > remaining {
			return limit + 1, nil
		}
		total += count
	}
}
