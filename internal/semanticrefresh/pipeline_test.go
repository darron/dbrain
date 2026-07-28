package semanticrefresh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

func TestPipelinePinsProjectionAndAdvancesThroughExactStageOrder(t *testing.T) {
	h := newPipelineHarness(t)
	var projectionCalls int
	h.pipeline.runProjection = func(_ context.Context, _ semanticbuild.ChunkStore, opts semanticbuild.ProjectionBatchOptions) (semanticbuild.ChunkProgress, error) {
		projectionCalls++
		if opts.Watermark != 73 || opts.Limit != DefaultProjectionBatch {
			t.Fatalf("projection opts=%+v, want pinned watermark 73 and default limit %d", opts, DefaultProjectionBatch)
		}
		if projectionCalls == 1 {
			return semanticbuild.ChunkProgress{
				Progress: semanticbuild.Progress{Scanned: 1, Generated: 1},
			}, nil
		}
		return semanticbuild.ChunkProgress{}, nil
	}
	h.pipeline.runEmbed = func(context.Context, semanticbuild.EmbedStore, embedding.Provider, semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
		return semanticbuild.EmbedBatchResult{Revision: 19}, nil
	}
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{}, store.ErrRetrievalEmbeddingProfileNotFound
	}
	h.pipeline.runCompact = func(context.Context, semanticbuild.CompactionStore, semanticbuild.StreamingSegmentPayloadBuilder, semanticbuild.CompactionOptions) (semanticbuild.CompactionResult, error) {
		return semanticbuild.CompactionResult{
			Plan: semanticbuild.SegmentCompactionPlan{
				Kind:    semanticbuild.SegmentCompactionNone,
				Inputs:  []semanticbuild.SegmentCompactionInput{},
				Outputs: []semanticbuild.SegmentCompactionOutput{},
			},
		}, nil
	}
	h.pipeline.runVerify = func(context.Context, semanticbuild.VerifyStore, semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		return semanticbuild.VerifyProgress{}, nil
	}
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return pipelineReadySnapshot(h.profileID), nil
	}
	h.store.workRevision = func(context.Context) (int64, error) { return 73, nil }

	run := pipelineRun(store.SemanticRefreshProjection, 73)
	outcome := h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshProjection ||
		outcome.Counters.ProjectedParents != 1 ||
		outcome.Checkpoint != "projection:watermark=73" {
		t.Fatalf("first projection outcome=%+v", outcome)
	}

	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshEmbedding ||
		outcome.Counters.ProjectedParents != 1 {
		t.Fatalf("empty projection outcome=%+v", outcome)
	}

	for _, want := range []store.SemanticRefreshStage{
		store.SemanticRefreshFlush,
		store.SemanticRefreshCompaction,
		store.SemanticRefreshVerify,
		store.SemanticRefreshReadiness,
	} {
		run = applyPipelineOutcome(run, outcome)
		outcome = h.execute(t, run)
		if outcome.NextStage != want {
			t.Fatalf("from stage %s got next=%s, want %s (outcome=%+v)", run.Stage, outcome.NextStage, want, outcome)
		}
	}
	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if !outcome.Complete || outcome.NextStage != store.SemanticRefreshReadiness ||
		outcome.Readiness != string(semanticreadiness.StateReady) {
		t.Fatalf("readiness outcome=%+v", outcome)
	}
}

func TestPipelineUsesDefaultBatchesAndRejectsOutOfBoundsBatches(t *testing.T) {
	h := newPipelineHarness(t)
	if h.pipeline.options.ProjectionBatch != 100 || h.pipeline.options.EmbeddingBatch != 16 {
		t.Fatalf("defaults projection=%d embedding=%d", h.pipeline.options.ProjectionBatch, h.pipeline.options.EmbeddingBatch)
	}

	tests := []struct {
		name string
		edit func(*PipelineOptions)
	}{
		{name: "negative projection", edit: func(opts *PipelineOptions) { opts.ProjectionBatch = -1 }},
		{name: "projection over maximum", edit: func(opts *PipelineOptions) { opts.ProjectionBatch = 5_001 }},
		{name: "negative embedding", edit: func(opts *PipelineOptions) { opts.EmbeddingBatch = -1 }},
		{name: "embedding over maximum", edit: func(opts *PipelineOptions) { opts.EmbeddingBatch = semanticbuild.MaxEmbeddingBatchSize + 1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := h.options
			tc.edit(&opts)
			if _, err := NewPipeline(h.store, opts); err == nil {
				t.Fatal("invalid pipeline bounds accepted")
			}
		})
	}
}

func TestPipelinePersistsExactEmbeddingRevision(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.runEmbed = func(context.Context, semanticbuild.EmbedStore, embedding.Provider, semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
		return semanticbuild.EmbedBatchResult{
			Progress: semanticbuild.Progress{Scanned: 4, Generated: 4},
			Revision: 918,
			HasMore:  true,
		}, nil
	}

	outcome := h.execute(t, pipelineRun(store.SemanticRefreshEmbedding, 1))
	if outcome.EmbeddingRevision != 918 ||
		outcome.Counters.EmbeddedChunks != 4 ||
		outcome.NextStage != store.SemanticRefreshEmbedding {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func TestPipelineEmbeddingPreflushHonorsHardL0Headroom(t *testing.T) {
	tests := []struct {
		name           string
		selected       int
		wantOps        []string
		wantFlushed    int64
		wantGeneration string
	}{
		{
			name:           "would exceed hard limit",
			selected:       501,
			wantOps:        []string{"flush", "provider"},
			wantFlushed:    store.RetrievalSegmentTarget,
			wantGeneration: "semantic-root-v1:preflush",
		},
		{
			name:     "exactly reaches hard limit",
			selected: 500,
			wantOps:  []string{"provider"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newPipelineHarness(t)
			h.pipeline.options.EmbeddingBatch = tc.selected
			var operations []string
			h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
				return store.RetrievalEmbeddingProfileRow{ProfileID: h.profileID, L0ReadyCount: 9_500}, nil
			}
			h.pipeline.runFlush = func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
				operations = append(operations, "flush")
				return semanticbuild.FlushResult{
					GenerationID:     "semantic-root-v1:preflush",
					Indexed:          3 * store.RetrievalSegmentTarget,
					Flushed:          store.RetrievalSegmentTarget,
					SnapshotRevision: 88,
				}, nil
			}
			h.pipeline.runEmbed = func(ctx context.Context, _ semanticbuild.EmbedStore, _ embedding.Provider, opts semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
				if opts.BatchSize != tc.selected {
					t.Fatalf("embedding batch=%d, want %d", opts.BatchSize, tc.selected)
				}
				if err := opts.BeforeProvider(ctx, tc.selected); err != nil {
					return semanticbuild.EmbedBatchResult{}, err
				}
				operations = append(operations, "provider")
				return semanticbuild.EmbedBatchResult{
					Progress: semanticbuild.Progress{Scanned: tc.selected, Generated: tc.selected},
					Revision: 89,
				}, nil
			}

			outcome := h.execute(t, pipelineRun(store.SemanticRefreshEmbedding, 1))
			if !reflect.DeepEqual(operations, tc.wantOps) {
				t.Fatalf("operations=%v, want %v", operations, tc.wantOps)
			}
			if outcome.Counters.FlushedVectors != tc.wantFlushed ||
				outcome.CurrentGenerationID != tc.wantGeneration {
				t.Fatalf("outcome=%+v", outcome)
			}
		})
	}
}

func TestPipelineEmbeddingPreservesCommittedPreflushWhenEmbedReturnsZeroAfterError(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.options.EmbeddingBatch = 501
	providerErr := errors.New("provider failed after committed preflush")
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:    h.profileID,
			L0ReadyCount: 9_500,
		}, nil
	}
	h.pipeline.runFlush = func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
		return semanticbuild.FlushResult{
			GenerationID:     "semantic-root-v1:committed-preflush",
			Indexed:          2 * store.RetrievalSegmentTarget,
			Flushed:          store.RetrievalSegmentTarget,
			SnapshotRevision: 88,
		}, nil
	}
	h.pipeline.runEmbed = func(ctx context.Context, _ semanticbuild.EmbedStore, _ embedding.Provider, opts semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
		if err := opts.BeforeProvider(ctx, 501); err != nil {
			t.Fatalf("before provider err=%v", err)
		}
		return semanticbuild.EmbedBatchResult{}, providerErr
	}

	run := pipelineRun(store.SemanticRefreshEmbedding, 1)
	run.EmbeddingRevision = 41
	run.Checkpoint = embeddingCheckpoint(41)
	outcome, err := h.pipeline.Execute(t.Context(), run)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) ||
		refreshErr.Code != ErrorEmbedding ||
		!errors.Is(err, providerErr) {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if outcome.Counters.FlushedVectors != store.RetrievalSegmentTarget ||
		outcome.CurrentGenerationID != "semantic-root-v1:committed-preflush" ||
		outcome.EmbeddingRevision != 88 ||
		outcome.Checkpoint != embeddingCheckpoint(88) {
		t.Fatalf("committed preflush was not preserved: outcome=%+v", outcome)
	}
}

func TestPipelineEmbeddingRejectsZeroSuccessfulPreflushBeforeProvider(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.options.EmbeddingBatch = 501
	var providerCalled bool
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:    h.profileID,
			L0ReadyCount: 9_500,
		}, nil
	}
	h.pipeline.runFlush = func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
		return semanticbuild.FlushResult{}, nil
	}
	h.pipeline.runEmbed = func(ctx context.Context, _ semanticbuild.EmbedStore, _ embedding.Provider, opts semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
		if err := opts.BeforeProvider(ctx, 501); err != nil {
			return semanticbuild.EmbedBatchResult{}, err
		}
		providerCalled = true
		return semanticbuild.EmbedBatchResult{}, nil
	}

	run := pipelineRun(store.SemanticRefreshEmbedding, 1)
	run.EmbeddingRevision = 41
	run.Checkpoint = embeddingCheckpoint(41)
	run.Counters.FlushedVectors = 7
	run.CurrentGenerationID = "semantic-root-v1:previous"
	outcome, err := h.pipeline.Execute(t.Context(), run)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorEmbedding {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if providerCalled {
		t.Fatal("provider called after an invalid nil-error preflush result")
	}
	if outcome.NextStage != store.SemanticRefreshEmbedding ||
		outcome.Checkpoint != embeddingCheckpoint(41) ||
		outcome.EmbeddingRevision != 41 ||
		outcome.Counters.FlushedVectors != 7 ||
		outcome.CurrentGenerationID != "semantic-root-v1:previous" {
		t.Fatalf("invalid preflush changed prior outcome: %+v", outcome)
	}
}

func TestPipelineEmbeddingPreflushFailurePreventsProvider(t *testing.T) {
	h := newPipelineHarness(t)
	flushErr := errors.New("flush activation failed")
	var providerCalled bool
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{ProfileID: h.profileID, L0ReadyCount: 9_999}, nil
	}
	h.pipeline.runFlush = func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
		return semanticbuild.FlushResult{}, flushErr
	}
	h.pipeline.runEmbed = func(ctx context.Context, _ semanticbuild.EmbedStore, _ embedding.Provider, opts semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
		if err := opts.BeforeProvider(ctx, 2); err != nil {
			return semanticbuild.EmbedBatchResult{}, err
		}
		providerCalled = true
		return semanticbuild.EmbedBatchResult{}, nil
	}

	run := pipelineRun(store.SemanticRefreshEmbedding, 1)
	run.EmbeddingRevision = 41
	run.Checkpoint = embeddingCheckpoint(41)
	run.Counters.FlushedVectors = 7
	run.CurrentGenerationID = "semantic-root-v1:previous"
	outcome, err := h.pipeline.Execute(t.Context(), run)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorEmbedding || !errors.Is(err, flushErr) {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if providerCalled {
		t.Fatal("provider called after headroom preflush failure")
	}
	if outcome.NextStage != store.SemanticRefreshEmbedding ||
		outcome.Checkpoint != embeddingCheckpoint(41) ||
		outcome.EmbeddingRevision != 41 ||
		outcome.Counters.FlushedVectors != 7 ||
		outcome.CurrentGenerationID != "semantic-root-v1:previous" {
		t.Fatalf("failed preflush changed prior outcome: %+v", outcome)
	}
}

func TestPipelineFlushRepeatsAboveTargetAndCountsCommittedDelta(t *testing.T) {
	h := newPipelineHarness(t)
	var l0Reads, flushCalls int
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		l0Reads++
		l0 := 5_001
		if l0Reads > 1 {
			l0 = 5_000
		}
		return store.RetrievalEmbeddingProfileRow{ProfileID: h.profileID, L0ReadyCount: l0}, nil
	}
	h.pipeline.runFlush = func(_ context.Context, _ semanticbuild.FlushStore, _ semanticbuild.SegmentPayloadBuilder, opts semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
		flushCalls++
		if opts.Limit != store.RetrievalSegmentTarget {
			t.Fatalf("flush limit=%d", opts.Limit)
		}
		return semanticbuild.FlushResult{
			GenerationID:     "semantic-root-v1:flush",
			Indexed:          3 * store.RetrievalSegmentTarget,
			Flushed:          store.RetrievalSegmentTarget,
			SnapshotRevision: 61,
		}, nil
	}

	run := pipelineRun(store.SemanticRefreshFlush, 1)
	outcome := h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshFlush ||
		outcome.Counters.FlushedVectors != store.RetrievalSegmentTarget ||
		outcome.EmbeddingRevision != 61 ||
		outcome.CurrentGenerationID != "semantic-root-v1:flush" {
		t.Fatalf("flush outcome=%+v", outcome)
	}
	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshCompaction || flushCalls != 1 {
		t.Fatalf("idle flush outcome=%+v calls=%d", outcome, flushCalls)
	}
}

func TestPipelineFlushRejectsInvalidNilErrorPhysicalResults(t *testing.T) {
	valid := semanticbuild.FlushResult{
		GenerationID:     "semantic-root-v1:valid",
		Indexed:          2 * store.RetrievalSegmentTarget,
		Flushed:          store.RetrievalSegmentTarget,
		SnapshotRevision: 88,
	}
	tests := []struct {
		name         string
		result       semanticbuild.FlushResult
		flushedStart int64
	}{
		{name: "zero result", result: semanticbuild.FlushResult{}},
		{name: "partial committed delta", result: func() semanticbuild.FlushResult {
			result := valid
			result.Flushed = store.RetrievalSegmentTarget - 1
			return result
		}()},
		{name: "negative committed delta", result: func() semanticbuild.FlushResult {
			result := valid
			result.Flushed = -1
			return result
		}()},
		{name: "total membership below committed delta", result: func() semanticbuild.FlushResult {
			result := valid
			result.Indexed = store.RetrievalSegmentTarget - 1
			return result
		}()},
		{name: "missing generation", result: func() semanticbuild.FlushResult {
			result := valid
			result.GenerationID = ""
			return result
		}()},
		{name: "unsafe generation", result: func() semanticbuild.FlushResult {
			result := valid
			result.GenerationID = "unsafe/generation"
			return result
		}()},
		{name: "zero snapshot revision", result: func() semanticbuild.FlushResult {
			result := valid
			result.SnapshotRevision = 0
			return result
		}()},
		{
			name:         "counter overflow",
			result:       valid,
			flushedStart: math.MaxInt64 - store.RetrievalSegmentTarget + 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newPipelineHarness(t)
			h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
				return store.RetrievalEmbeddingProfileRow{
					ProfileID:    h.profileID,
					L0ReadyCount: store.RetrievalSegmentTarget + 1,
				}, nil
			}
			h.pipeline.runFlush = func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
				return tc.result, nil
			}
			run := pipelineRun(store.SemanticRefreshFlush, 1)
			run.EmbeddingRevision = 41
			run.Checkpoint = flushCheckpoint(41)
			run.Counters.FlushedVectors = tc.flushedStart
			run.CurrentGenerationID = "semantic-root-v1:previous"

			outcome, err := h.pipeline.Execute(t.Context(), run)
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorFlush {
				t.Fatalf("outcome=%+v err=%v", outcome, err)
			}
			if outcome.NextStage != store.SemanticRefreshFlush ||
				outcome.Checkpoint != flushCheckpoint(41) ||
				outcome.EmbeddingRevision != 41 ||
				outcome.Counters.FlushedVectors != tc.flushedStart ||
				outcome.CurrentGenerationID != "semantic-root-v1:previous" {
				t.Fatalf("invalid physical flush changed prior outcome: %+v", outcome)
			}
		})
	}
}

func TestPipelineFlushPreservesPriorOutcomeWhenPhysicalFlushFails(t *testing.T) {
	h := newPipelineHarness(t)
	flushErr := errors.New("physical flush failed")
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:    h.profileID,
			L0ReadyCount: store.RetrievalSegmentTarget + 1,
		}, nil
	}
	h.pipeline.runFlush = func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error) {
		return semanticbuild.FlushResult{}, flushErr
	}
	run := pipelineRun(store.SemanticRefreshFlush, 1)
	run.EmbeddingRevision = 41
	run.Checkpoint = flushCheckpoint(41)
	run.Counters.FlushedVectors = 7
	run.CurrentGenerationID = "semantic-root-v1:previous"

	outcome, err := h.pipeline.Execute(t.Context(), run)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) ||
		refreshErr.Code != ErrorFlush ||
		!errors.Is(err, flushErr) {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if outcome.NextStage != store.SemanticRefreshFlush ||
		outcome.Checkpoint != flushCheckpoint(41) ||
		outcome.EmbeddingRevision != 41 ||
		outcome.Counters.FlushedVectors != 7 ||
		outcome.CurrentGenerationID != "semantic-root-v1:previous" {
		t.Fatalf("failed physical flush changed prior outcome: %+v", outcome)
	}
}

func TestPipelineCompactionRunsOneBoundedPlanPerExecute(t *testing.T) {
	h := newPipelineHarness(t)
	var calls int
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{ProfileID: h.profileID}, nil
	}
	h.pipeline.runCompact = func(context.Context, semanticbuild.CompactionStore, semanticbuild.StreamingSegmentPayloadBuilder, semanticbuild.CompactionOptions) (semanticbuild.CompactionResult, error) {
		calls++
		if calls == 1 {
			return semanticbuild.CompactionResult{
				Plan: semanticbuild.SegmentCompactionPlan{
					Kind: semanticbuild.SegmentCompactionPair,
					Inputs: []semanticbuild.SegmentCompactionInput{
						{SegmentHash: "a", LiveCount: 5_000},
						{SegmentHash: "b", LiveCount: 5_000},
					},
				},
				GenerationID:        "semantic-root-v1:compact",
				Indexed:             10_000,
				StreamedLiveMembers: 9_937,
			}, nil
		}
		return semanticbuild.CompactionResult{
			Plan: semanticbuild.SegmentCompactionPlan{
				Kind:    semanticbuild.SegmentCompactionNone,
				Inputs:  []semanticbuild.SegmentCompactionInput{},
				Outputs: []semanticbuild.SegmentCompactionOutput{},
			},
		}, nil
	}

	run := pipelineRun(store.SemanticRefreshCompaction, 1)
	outcome := h.execute(t, run)
	if calls != 1 || outcome.NextStage != store.SemanticRefreshCompaction ||
		outcome.Counters.CompactedVectors != 9_937 ||
		outcome.CurrentGenerationID != "semantic-root-v1:compact" {
		t.Fatalf("calls=%d outcome=%+v", calls, outcome)
	}
	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if calls != 2 || outcome.NextStage != store.SemanticRefreshVerify {
		t.Fatalf("calls=%d outcome=%+v", calls, outcome)
	}
}

func TestPipelineVerifyUsesBoundedCursorPagesWithoutCounterRepair(t *testing.T) {
	h := newPipelineHarness(t)
	var calls int
	h.pipeline.runVerify = func(_ context.Context, _ semanticbuild.VerifyStore, opts semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		calls++
		if opts.Limit != 5_000 || opts.RepairCounters {
			t.Fatalf("verify opts=%+v", opts)
		}
		if calls == 1 {
			if opts.Resume != "" {
				t.Fatalf("first resume=%q", opts.Resume)
			}
			return semanticbuild.VerifyProgress{
				Progress: semanticbuild.Progress{Scanned: 5_000},
				Resume:   "chunk-05000",
				HasMore:  true,
			}, nil
		}
		if opts.Resume != "chunk-05000" {
			t.Fatalf("second resume=%q", opts.Resume)
		}
		return semanticbuild.VerifyProgress{
			Progress: semanticbuild.Progress{Scanned: 7},
			Resume:   "chunk-05007",
		}, nil
	}
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{}, store.ErrRetrievalEmbeddingProfileNotFound
	}

	run := pipelineRun(store.SemanticRefreshVerify, 1)
	outcome := h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshVerify ||
		outcome.Checkpoint != "verify:after=chunk-05000" ||
		outcome.Counters.VerifiedVectors != 5_000 {
		t.Fatalf("first verify outcome=%+v", outcome)
	}
	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshReadiness ||
		outcome.Counters.VerifiedVectors != 5_007 {
		t.Fatalf("final verify outcome=%+v", outcome)
	}
}

func TestPipelineVerifyPersistsOnlyLastSuccessfullyValidatedCursorOnError(t *testing.T) {
	h := newPipelineHarness(t)
	verifyErr := errors.New("chunk-b validation failed")
	h.pipeline.runVerify = func(_ context.Context, _ semanticbuild.VerifyStore, opts semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		if opts.Resume != "" {
			t.Fatalf("unexpected resume=%q", opts.Resume)
		}
		return semanticbuild.VerifyProgress{
			Progress: semanticbuild.Progress{Scanned: 1, Current: 1},
			Resume:   "chunk-a",
		}, verifyErr
	}

	outcome, err := h.pipeline.Execute(
		t.Context(),
		pipelineRun(store.SemanticRefreshVerify, 1),
	)
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) ||
		refreshErr.Code != ErrorVerify ||
		!errors.Is(err, verifyErr) {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if outcome.NextStage != store.SemanticRefreshVerify ||
		outcome.Checkpoint != verifyCheckpoint("chunk-a") ||
		outcome.Counters.VerifiedVectors != 1 {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func TestPipelineVerifyProvesActiveNativeRootFromOneAuthoritativeSnapshot(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.runVerify = func(context.Context, semanticbuild.VerifyStore, semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		return semanticbuild.VerifyProgress{}, nil
	}
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:              h.profileID,
			ActiveGenerationID:     "semantic-root-v1:active",
			ActiveSnapshotRevision: 47,
			PurgeEpoch:             9,
		}, nil
	}
	snapshotCalls := 0
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		snapshotCalls++
		snapshot := pipelineReadySnapshot(h.profileID)
		snapshot.ActiveGenerationID = "semantic-root-v1:active"
		snapshot.ActiveSnapshotRevision = 47
		snapshot.GlobalPurgeEpoch = 9
		snapshot.ProfilePurgeEpoch = 9
		snapshot.ActiveGenerationValid = true
		snapshot.ActiveGenerationRootDescriptorSHA256 = "0123456789abcdef"
		return snapshot, nil
	}
	h.store.databaseID = func(context.Context) (string, error) { return "database-1", nil }
	h.store.purgeEpoch = func(context.Context) (int64, error) { return 9, nil }

	run := pipelineRun(store.SemanticRefreshVerify, 1)
	run.PurgeEpoch = 9
	outcome := h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshReadiness || snapshotCalls != 1 ||
		len(h.native.expectations) != 1 {
		t.Fatalf("outcome=%+v snapshots=%d native=%v", outcome, snapshotCalls, h.native.expectations)
	}
	want := RootExpectation{
		CacheDir:         h.options.CacheDir,
		DatabaseID:       "database-1",
		ProfileID:        h.profileID,
		GenerationID:     "semantic-root-v1:active",
		DescriptorSHA256: "0123456789abcdef",
		SnapshotRevision: 47,
		PurgeEpoch:       9,
		Dimensions:       h.options.Profile.Dimensions,
		BackendVersion:   semanticindex.USearchVersion,
	}
	if got := h.native.expectations[0]; got != want {
		t.Fatalf("root expectation=%+v, want %+v", got, want)
	}
}

func TestPipelineNeverAdvancesPastUnprovenActiveNativeRoot(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.runVerify = func(context.Context, semanticbuild.VerifyStore, semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		return semanticbuild.VerifyProgress{}, nil
	}
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:          h.profileID,
			ActiveGenerationID: "semantic-root-v1:active",
		}, nil
	}
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		snapshot := pipelineReadySnapshot(h.profileID)
		snapshot.ActiveGenerationID = "semantic-root-v1:active"
		snapshot.ActiveGenerationValid = true
		return snapshot, nil
	}

	outcome, err := h.pipeline.Execute(t.Context(), pipelineRun(store.SemanticRefreshVerify, 1))
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorNativeRoot {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if len(h.native.expectations) != 0 || outcome.NextStage != store.SemanticRefreshVerify {
		t.Fatalf("native=%v outcome=%+v", h.native.expectations, outcome)
	}
}

func TestPipelineNativeRootFailureUsesNativeRootError(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.runVerify = func(context.Context, semanticbuild.VerifyStore, semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		return semanticbuild.VerifyProgress{}, nil
	}
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:              h.profileID,
			ActiveGenerationID:     "semantic-root-v1:active",
			ActiveSnapshotRevision: 3,
			PurgeEpoch:             1,
		}, nil
	}
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		snapshot := pipelineReadySnapshot(h.profileID)
		snapshot.ActiveGenerationID = "semantic-root-v1:active"
		snapshot.ActiveSnapshotRevision = 3
		snapshot.ActiveGenerationValid = true
		snapshot.ActiveGenerationRootDescriptorSHA256 = "root-hash"
		return snapshot, nil
	}
	h.native.verifyErr = errors.New("native import rejected root")

	outcome, err := h.pipeline.Execute(t.Context(), pipelineRun(store.SemanticRefreshVerify, 1))
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorNativeRoot ||
		!errors.Is(err, h.native.verifyErr) ||
		outcome.NextStage != store.SemanticRefreshVerify {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

func TestPipelineReadinessRequiresReadyBoundedL0AndNoNewProjectionRevision(t *testing.T) {
	tests := []struct {
		name     string
		snapshot semanticreadiness.Snapshot
		wantCode string
	}{
		{
			name:     "ready",
			snapshot: pipelineReadySnapshot("profile"),
		},
		{
			name: "independent l0 bound",
			snapshot: func() semanticreadiness.Snapshot {
				snapshot := pipelineReadySnapshot("profile")
				snapshot.L0ReadyCount = store.RetrievalSegmentTarget + 1
				return snapshot
			}(),
			wantCode: ErrorReadiness,
		},
		{
			name: "ordinary readiness required",
			snapshot: func() semanticreadiness.Snapshot {
				snapshot := pipelineReadySnapshot("profile")
				snapshot.PendingEmbeddings = 1
				return snapshot
			}(),
			wantCode: ErrorReadiness,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newPipelineHarness(t)
			tc.snapshot.ProfileID = h.profileID
			h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
				return tc.snapshot, nil
			}
			h.store.workRevision = func(context.Context) (int64, error) { return 44, nil }
			run := pipelineRun(store.SemanticRefreshReadiness, 44)

			outcome, err := h.pipeline.Execute(t.Context(), run)
			if tc.wantCode == "" {
				if err != nil || !outcome.Complete || outcome.SuccessorWatermark != nil {
					t.Fatalf("outcome=%+v err=%v", outcome, err)
				}
				return
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) || refreshErr.Code != tc.wantCode ||
				outcome.Complete {
				t.Fatalf("outcome=%+v err=%v", outcome, err)
			}
		})
	}
}

func TestPipelineReadinessReturnsHigherWatermarkSuccessorBeforeCurrentDebtFailure(t *testing.T) {
	h := newPipelineHarness(t)
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		snapshot := pipelineReadySnapshot(h.profileID)
		snapshot.DirtyParents = 1
		snapshot.PendingParents = 1
		return snapshot, nil
	}
	h.store.workRevision = func(context.Context) (int64, error) { return 101, nil }

	outcome, err := h.pipeline.Execute(t.Context(), pipelineRun(store.SemanticRefreshReadiness, 100))
	if err != nil || !outcome.Complete || outcome.SuccessorWatermark == nil ||
		*outcome.SuccessorWatermark != 101 ||
		outcome.Debt.DirtyParents != 1 {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

func TestPipelineReadinessRoutesChangedRootThroughCompactionBeforeNativeProof(t *testing.T) {
	h := newPipelineHarness(t)
	var snapshotCalls, compactionCalls, verifyCalls int
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		snapshotCalls++
		snapshot := pipelineReadySnapshot(h.profileID)
		if snapshotCalls == 1 {
			snapshot.ActiveGenerationID = "semantic-root-v1:concurrent"
			snapshot.ActiveSnapshotRevision = 47
			snapshot.ActiveGenerationRootDescriptorSHA256 = "root-hash-concurrent"
			return snapshot, nil
		}
		snapshot.ActiveGenerationID = "semantic-root-v1:compacted"
		snapshot.ActiveSnapshotRevision = 48
		snapshot.ActiveGenerationValid = true
		snapshot.ActiveGenerationRootDescriptorSHA256 = "root-hash-compacted"
		return snapshot, nil
	}
	h.store.profile = func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error) {
		return store.RetrievalEmbeddingProfileRow{
			ProfileID:              h.profileID,
			ActiveGenerationID:     "semantic-root-v1:compacted",
			ActiveSnapshotRevision: 48,
			PurgeEpoch:             1,
		}, nil
	}
	h.pipeline.runCompact = func(context.Context, semanticbuild.CompactionStore, semanticbuild.StreamingSegmentPayloadBuilder, semanticbuild.CompactionOptions) (semanticbuild.CompactionResult, error) {
		compactionCalls++
		if compactionCalls == 1 {
			return semanticbuild.CompactionResult{
				Plan: semanticbuild.SegmentCompactionPlan{
					Kind: semanticbuild.SegmentCompactionPair,
					Inputs: []semanticbuild.SegmentCompactionInput{
						{SegmentHash: "a", LiveCount: 5_000},
						{SegmentHash: "b", LiveCount: 5_000},
					},
				},
				GenerationID:        "semantic-root-v1:compacted",
				Indexed:             10_000,
				StreamedLiveMembers: 10_000,
			}, nil
		}
		return semanticbuild.CompactionResult{
			Plan: semanticbuild.SegmentCompactionPlan{
				Kind:    semanticbuild.SegmentCompactionNone,
				Inputs:  []semanticbuild.SegmentCompactionInput{},
				Outputs: []semanticbuild.SegmentCompactionOutput{},
			},
		}, nil
	}
	h.pipeline.runVerify = func(context.Context, semanticbuild.VerifyStore, semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error) {
		verifyCalls++
		return semanticbuild.VerifyProgress{}, nil
	}
	h.store.workRevision = func(context.Context) (int64, error) { return 1, nil }

	run := pipelineRun(store.SemanticRefreshReadiness, 1)
	run.CurrentGenerationID = "semantic-root-v1:previously-proven"
	run.Checkpoint = readinessCheckpoint(string(semanticreadiness.StateReady))
	outcome, err := h.pipeline.Execute(t.Context(), run)
	if err != nil ||
		outcome.NextStage != store.SemanticRefreshCompaction ||
		outcome.Complete ||
		outcome.CurrentGenerationID != "" ||
		outcome.Checkpoint != compactionCheckpoint("") ||
		outcome.Readiness != "" ||
		compactionCalls != 0 ||
		verifyCalls != 0 ||
		len(h.native.expectations) != 0 {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}

	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshCompaction ||
		outcome.CurrentGenerationID != "semantic-root-v1:compacted" ||
		compactionCalls != 1 ||
		len(h.native.expectations) != 0 {
		t.Fatalf("replacement compaction outcome=%+v calls=%d native=%v", outcome, compactionCalls, h.native.expectations)
	}

	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshVerify ||
		compactionCalls != 2 ||
		len(h.native.expectations) != 0 {
		t.Fatalf("idle compaction outcome=%+v calls=%d native=%v", outcome, compactionCalls, h.native.expectations)
	}

	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if outcome.NextStage != store.SemanticRefreshReadiness ||
		verifyCalls != 1 ||
		len(h.native.expectations) != 1 ||
		h.native.expectations[0].GenerationID != "semantic-root-v1:compacted" {
		t.Fatalf("verify outcome=%+v calls=%d native=%v", outcome, verifyCalls, h.native.expectations)
	}

	run = applyPipelineOutcome(run, outcome)
	outcome = h.execute(t, run)
	if !outcome.Complete ||
		outcome.NextStage != store.SemanticRefreshReadiness ||
		compactionCalls != 2 ||
		verifyCalls != 1 ||
		len(h.native.expectations) != 1 {
		t.Fatalf("readiness outcome=%+v compaction=%d verify=%d native=%v", outcome, compactionCalls, verifyCalls, h.native.expectations)
	}
}

func TestPipelineReadinessRoutesRemovedRootThroughCompaction(t *testing.T) {
	h := newPipelineHarness(t)
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		return pipelineReadySnapshot(h.profileID), nil
	}
	h.store.workRevision = func(context.Context) (int64, error) {
		t.Fatal("projection revision read before removed root returned to compaction")
		return 0, nil
	}

	run := pipelineRun(store.SemanticRefreshReadiness, 1)
	run.CurrentGenerationID = "semantic-root-v1:removed"
	run.Checkpoint = readinessCheckpoint(string(semanticreadiness.StateReady))
	outcome, err := h.pipeline.Execute(t.Context(), run)
	if err != nil ||
		outcome.NextStage != store.SemanticRefreshCompaction ||
		outcome.Complete ||
		outcome.CurrentGenerationID != "" ||
		outcome.Checkpoint != compactionCheckpoint("") ||
		outcome.Readiness != "" {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

func TestPipelineReadinessFailureReturnsBoundedAggregateDebt(t *testing.T) {
	h := newPipelineHarness(t)
	h.store.readiness = func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error) {
		snapshot := pipelineReadySnapshot(h.profileID)
		snapshot.DirtyParents = 4
		snapshot.PendingEmbeddings = 5
		snapshot.DueRetries = 6
		snapshot.ScheduledRetries = 7
		snapshot.BlockedEmbeddings = 8
		snapshot.ErrorEmbeddings = 9
		snapshot.ActiveIndexedCount = 12
		snapshot.L0ReadyCount = 10
		snapshot.ActiveTombstones = 11
		snapshot.BuildingGenerations = 2
		snapshot.StaleGenerations = 3
		snapshot.ErrorGenerations = 4
		snapshot.PlanningError = "secret/vector/source text must not escape"
		return snapshot, nil
	}
	h.store.workRevision = func(context.Context) (int64, error) { return 1, nil }

	outcome, err := h.pipeline.Execute(t.Context(), pipelineRun(store.SemanticRefreshReadiness, 1))
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorReadiness {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	wantDebt := Debt{
		DirtyParents:      4,
		PendingEmbeddings: 5,
		DueRetries:        6,
		ScheduledRetries:  7,
		BlockedEmbeddings: 8,
		FailedEmbeddings:  9,
		Indexed:           12,
		L0Ready:           10,
		Tombstones:        11,
		Segments:          9,
	}
	if outcome.Debt != wantDebt || refreshErr.Debt != wantDebt ||
		len(outcome.Checkpoint) > 256 || len(outcome.Readiness) > 64 ||
		outcome.Checkpoint == "secret/vector/source text must not escape" {
		t.Fatalf("outcome=%+v err=%+v want debt=%+v", outcome, refreshErr, wantDebt)
	}
}

func TestPipelineEmbeddingCircuitUsesStableTypedCodeAndKeepsOutcome(t *testing.T) {
	h := newPipelineHarness(t)
	h.pipeline.runEmbed = func(context.Context, semanticbuild.EmbedStore, embedding.Provider, semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error) {
		return semanticbuild.EmbedBatchResult{
			Progress: semanticbuild.Progress{Scanned: 1, Failed: 1},
			Revision: 51,
			HasMore:  true,
		}, semanticbuild.ErrEmbedCircuitOpen
	}

	outcome, err := h.pipeline.Execute(t.Context(), pipelineRun(store.SemanticRefreshEmbedding, 1))
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != ErrorEmbeddingCircuit ||
		outcome.EmbeddingRevision != 51 ||
		outcome.NextStage != store.SemanticRefreshEmbedding {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

type pipelineHarness struct {
	pipeline  *pipeline
	store     *pipelineTestStore
	native    *pipelineTestNative
	options   PipelineOptions
	profileID string
}

func newPipelineHarness(t *testing.T) pipelineHarness {
	t.Helper()
	provider := &pipelineTestProvider{
		info: embedding.Info{Provider: "test", Model: "embed-v1", Dimensions: 2},
	}
	profile := semanticbuild.Profile(provider.info)
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	st := &pipelineTestStore{}
	native := &pipelineTestNative{}
	options := PipelineOptions{
		Profile:        profile,
		Provider:       provider,
		Native:         native,
		CacheDir:       t.TempDir(),
		ExactMaxChunks: semanticreadiness.DefaultExactMaxChunks,
		Now:            func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
		Sleep:          func(context.Context, time.Duration) error { return nil },
	}
	executor, err := NewPipeline(st, options)
	if err != nil {
		t.Fatal(err)
	}
	concrete, ok := executor.(*pipeline)
	if !ok {
		t.Fatalf("executor type=%T, want *pipeline", executor)
	}
	return pipelineHarness{
		pipeline: concrete, store: st, native: native, options: concrete.options, profileID: profileID,
	}
}

func (h pipelineHarness) execute(t *testing.T, run store.SemanticRefreshRun) StageOutcome {
	t.Helper()
	outcome, err := h.pipeline.Execute(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func pipelineRun(stage store.SemanticRefreshStage, watermark int64) store.SemanticRefreshRun {
	return store.SemanticRefreshRun{
		RunID:               "run-1",
		ProjectionWatermark: watermark,
		PurgeEpoch:          1,
		Stage:               stage,
		State:               store.SemanticRefreshRunRunning,
		Version:             1,
	}
}

func applyPipelineOutcome(run store.SemanticRefreshRun, outcome StageOutcome) store.SemanticRefreshRun {
	run.Stage = outcome.NextStage
	run.Checkpoint = outcome.Checkpoint
	run.EmbeddingRevision = outcome.EmbeddingRevision
	run.Counters = outcome.Counters
	run.CurrentGenerationID = outcome.CurrentGenerationID
	run.ReadinessState = outcome.Readiness
	return run
}

func pipelineReadySnapshot(profileID string) semanticreadiness.Snapshot {
	return semanticreadiness.Snapshot{
		Available:                true,
		ProfileID:                profileID,
		ProfileExists:            true,
		ProfileProvenanceValid:   true,
		ActiveGenerationValid:    true,
		ActiveGenerationID:       "",
		GlobalPurgeEpoch:         1,
		ProfilePurgeEpoch:        1,
		ExpectedParents:          0,
		CurrentParents:           0,
		EmptyParents:             0,
		ChunkableParents:         0,
		ParentsWithReadyChunk:    0,
		ChunkCount:               0,
		ReadyEmbeddings:          0,
		ObservedLatestRevision:   0,
		LatestRevision:           0,
		ObservedL0ReadyCount:     0,
		L0ReadyCount:             0,
		ActiveSnapshotRevision:   0,
		ActiveIndexedCount:       0,
		ActiveTombstones:         0,
		BuildingGenerations:      0,
		StaleGenerations:         0,
		ErrorGenerations:         0,
		EstimatedNotReadyChunks:  0,
		AggregateCountersCorrupt: false,
	}
}

type pipelineTestStore struct {
	PipelineStore
	profile      func(context.Context, string) (store.RetrievalEmbeddingProfileRow, error)
	workRevision func(context.Context) (int64, error)
	purgeEpoch   func(context.Context) (int64, error)
	databaseID   func(context.Context) (string, error)
	readiness    func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
}

func (s *pipelineTestStore) RetrievalEmbeddingProfile(ctx context.Context, profileID string) (store.RetrievalEmbeddingProfileRow, error) {
	if s.profile == nil {
		return store.RetrievalEmbeddingProfileRow{}, store.ErrRetrievalEmbeddingProfileNotFound
	}
	return s.profile(ctx, profileID)
}

func (s *pipelineTestStore) ProjectionWorkRevision(ctx context.Context) (int64, error) {
	if s.workRevision == nil {
		return 0, nil
	}
	return s.workRevision(ctx)
}

func (s *pipelineTestStore) RetrievalPurgeEpoch(ctx context.Context) (int64, error) {
	if s.purgeEpoch == nil {
		return 1, nil
	}
	return s.purgeEpoch(ctx)
}

func (s *pipelineTestStore) RetrievalDatabaseID(ctx context.Context) (string, error) {
	if s.databaseID == nil {
		return "database", nil
	}
	return s.databaseID(ctx)
}

func (s *pipelineTestStore) SemanticReadinessSnapshotAt(ctx context.Context, profile embedding.Profile, exactMaxChunks int, now time.Time) (semanticreadiness.Snapshot, error) {
	if s.readiness == nil {
		profileID, _ := profile.ID()
		return pipelineReadySnapshot(profileID), nil
	}
	return s.readiness(ctx, profile, exactMaxChunks, now)
}

type pipelineTestProvider struct {
	info      embedding.Info
	responses []embedding.Response
	errs      []error
	calls     int
}

func (p *pipelineTestProvider) Info() embedding.Info { return p.info }

func (p *pipelineTestProvider) Embed(_ context.Context, request embedding.Request) (embedding.Response, error) {
	index := p.calls
	p.calls++
	if index < len(p.errs) && p.errs[index] != nil {
		return embedding.Response{}, p.errs[index]
	}
	if index < len(p.responses) {
		return p.responses[index], nil
	}
	vectors := make([][]float32, len(request.Texts))
	for vectorIndex := range vectors {
		vectors[vectorIndex] = []float32{0.6, 0.8}
	}
	return embedding.Response{
		Vectors: vectors, Provider: p.info.Provider, Model: p.info.Model, Dimensions: p.info.Dimensions,
	}, nil
}

type pipelineTestNative struct {
	expectations []RootExpectation
	verifyErr    error
}

func (*pipelineTestNative) Build(_ context.Context, rows []store.RetrievalEmbeddingRow) (func(io.Writer) error, error) {
	if len(rows) == 0 {
		return nil, errors.New("missing rows")
	}
	return func(writer io.Writer) error {
		_, err := io.WriteString(writer, "opaque-test-payload")
		return err
	}, nil
}

func (*pipelineTestNative) Begin(context.Context, int) (semanticbuild.StreamingSegmentPayloadSession, error) {
	return pipelineTestNativeSession{}, nil
}

func (n *pipelineTestNative) VerifyRoot(_ context.Context, expectation RootExpectation) error {
	n.expectations = append(n.expectations, expectation)
	return n.verifyErr
}

type pipelineTestNativeSession struct{}

func (pipelineTestNativeSession) Add(context.Context, store.RetrievalEmbeddingRow) error {
	return nil
}

func (pipelineTestNativeSession) Finish(context.Context) (func(io.Writer) error, error) {
	return func(writer io.Writer) error {
		_, err := fmt.Fprint(writer, "opaque-test-payload")
		return err
	}, nil
}

func (pipelineTestNativeSession) Close() error { return nil }
