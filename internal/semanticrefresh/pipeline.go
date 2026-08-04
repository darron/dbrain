package semanticrefresh

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

const (
	DefaultProjectionBatch = 100
	DefaultEmbeddingBatch  = 16

	maxProjectionBatch = 5_000
	maxVerifyBatch     = 5_000

	projectionCheckpointPrefix = "projection:watermark="
	embeddingCheckpointPrefix  = "embedding:revision="
	flushCheckpointPrefix      = "flush:snapshot="
	compactionCheckpointPrefix = "compaction:generation="
	verifyCheckpointPrefix     = "verify:after="
	readinessCheckpointPrefix  = "readiness:state="
)

var errDanglingNativeRootVerification = errors.New("dangling semantic root verification failed")

type PipelineOptions struct {
	Profile         embedding.Profile
	Provider        embedding.Provider
	Native          NativeLifecycle
	CacheDir        string
	ExactMaxChunks  int
	ProjectionBatch int
	EmbeddingBatch  int
	Now             func() time.Time
	Sleep           func(context.Context, time.Duration) error
}

type PipelineStore interface {
	semanticbuild.ChunkStore
	semanticbuild.EmbedStore
	semanticbuild.FlushStore
	semanticbuild.CompactionStore
	semanticbuild.VerifyStore
	ProjectionWorkRevision(context.Context) (int64, error)
	RetrievalPurgeEpoch(context.Context) (int64, error)
	RetrievalDatabaseID(context.Context) (string, error)
	RetrievalEmbeddingProfile(context.Context, string) (store.RetrievalEmbeddingProfileRow, error)
	RetrievalDanglingGenerationRecovery(context.Context, string) (*store.RetrievalDanglingGenerationRecovery, error)
	ReactivateRetrievalDanglingGeneration(context.Context, store.RetrievalDanglingGenerationRecovery) error
	SemanticReadinessSnapshotAt(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
}

type pipeline struct {
	store                     PipelineStore
	options                   PipelineOptions
	profileID                 string
	effectiveSegmentTarget    int
	effectiveSegmentHardLimit int

	runProjection func(context.Context, semanticbuild.ChunkStore, semanticbuild.ProjectionBatchOptions) (semanticbuild.ChunkProgress, error)
	runEmbed      func(context.Context, semanticbuild.EmbedStore, embedding.Provider, semanticbuild.EmbedBatchOptions) (semanticbuild.EmbedBatchResult, error)
	runFlush      func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error)
	runCompact    func(context.Context, semanticbuild.CompactionStore, semanticbuild.StreamingSegmentPayloadBuilder, semanticbuild.CompactionOptions) (semanticbuild.CompactionResult, error)
	runVerify     func(context.Context, semanticbuild.VerifyStore, semanticbuild.VerifyOptions) (semanticbuild.VerifyProgress, error)
}

func NewPipeline(st PipelineStore, options PipelineOptions) (StageExecutor, error) {
	if st == nil {
		return nil, fmt.Errorf("semantic refresh pipeline store is required")
	}
	if options.Provider == nil {
		return nil, fmt.Errorf("semantic refresh embedding provider is required")
	}
	if options.Native == nil {
		return nil, fmt.Errorf("semantic refresh native lifecycle is required")
	}
	if strings.TrimSpace(options.CacheDir) == "" {
		return nil, fmt.Errorf("semantic refresh cache directory is required")
	}
	if err := options.Profile.Validate(); err != nil {
		return nil, fmt.Errorf("semantic refresh profile is invalid: %w", err)
	}
	if providerProfile := semanticbuild.Profile(options.Provider.Info()); providerProfile != options.Profile {
		return nil, fmt.Errorf("semantic refresh provider does not match requested profile")
	}
	profileID, err := options.Profile.ID()
	if err != nil {
		return nil, fmt.Errorf("semantic refresh profile ID: %w", err)
	}
	if options.ProjectionBatch == 0 {
		options.ProjectionBatch = DefaultProjectionBatch
	}
	if options.ProjectionBatch < 1 || options.ProjectionBatch > maxProjectionBatch {
		return nil, fmt.Errorf("semantic refresh projection batch must be between 1 and %d", maxProjectionBatch)
	}
	if options.EmbeddingBatch == 0 {
		options.EmbeddingBatch = DefaultEmbeddingBatch
	}
	if options.EmbeddingBatch < 1 || options.EmbeddingBatch > semanticbuild.MaxEmbeddingBatchSize {
		return nil, fmt.Errorf(
			"semantic refresh embedding batch must be between 1 and %d",
			semanticbuild.MaxEmbeddingBatchSize,
		)
	}
	options.ExactMaxChunks = semanticreadiness.EffectiveExactMaxChunks(options.ExactMaxChunks)
	if options.Now == nil {
		options.Now = time.Now
	}
	return &pipeline{
		store:                     st,
		options:                   options,
		profileID:                 profileID,
		effectiveSegmentTarget:    store.RetrievalSegmentTarget,
		effectiveSegmentHardLimit: store.RetrievalSegmentHardLimit,
		runProjection:             semanticbuild.RunProjectionBatch,
		runEmbed:                  semanticbuild.RunEmbedBatch,
		runFlush:                  semanticbuild.Flush,
		runCompact:                semanticbuild.Compact,
		runVerify:                 semanticbuild.RunVerify,
	}, nil
}

func (p *pipeline) Execute(ctx context.Context, run store.SemanticRefreshRun) (StageOutcome, error) {
	if ctx == nil {
		return StageOutcome{}, fmt.Errorf("semantic refresh pipeline context is required")
	}
	if err := ctx.Err(); err != nil {
		return StageOutcome{}, err
	}
	if err := p.validateEffectiveSegmentBoundaries(); err != nil {
		return StageOutcome{}, err
	}
	if run.ProfileID != "" && run.ProfileID != p.profileID {
		return StageOutcome{}, NewError(
			ErrorRunConflict,
			run,
			run.ReadinessState,
			Debt{},
			fmt.Errorf("semantic refresh run profile does not match pipeline profile"),
		)
	}
	switch run.Stage {
	case store.SemanticRefreshProjection:
		return p.executeProjection(ctx, run)
	case store.SemanticRefreshEmbedding:
		return p.executeEmbedding(ctx, run)
	case store.SemanticRefreshFlush:
		return p.executeFlush(ctx, run)
	case store.SemanticRefreshCompaction:
		return p.executeCompaction(ctx, run)
	case store.SemanticRefreshVerify:
		return p.executeVerify(ctx, run)
	case store.SemanticRefreshReadiness:
		return p.executeReadiness(ctx, run)
	default:
		return StageOutcome{}, NewError(
			ErrorRunConflict,
			run,
			run.ReadinessState,
			Debt{},
			fmt.Errorf("semantic refresh run has invalid stage"),
		)
	}
}

func (p *pipeline) executeProjection(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	outcome := nextPipelineOutcome(run, store.SemanticRefreshProjection)
	outcome.Checkpoint = projectionCheckpoint(run.ProjectionWatermark)
	page, err := p.runProjection(ctx, p.store, semanticbuild.ProjectionBatchOptions{
		Watermark:          run.ProjectionWatermark,
		ExpectedPurgeEpoch: run.PurgeEpoch,
		Limit:              p.options.ProjectionBatch,
		Now:                p.options.Now,
	})
	if page.Current < 0 || page.Generated < 0 {
		err = errors.Join(err, fmt.Errorf("semantic projection returned a negative committed count"))
	} else {
		committed := page.Current
		if page.Generated > math.MaxInt-committed {
			err = errors.Join(err, fmt.Errorf("semantic projection committed count overflow"))
		} else if counterErr := addPipelineCounter(
			&outcome.Counters.ProjectedParents,
			committed+page.Generated,
		); counterErr != nil {
			err = errors.Join(err, counterErr)
		}
	}
	if err != nil {
		return outcome, pipelineStageError(ErrorProjection, run, outcome, err)
	}
	if page.Scanned == 0 && page.Checkpoint == nil && !page.HasMore && page.Remaining == 0 {
		outcome.NextStage = store.SemanticRefreshEmbedding
		outcome.Checkpoint = embeddingCheckpoint(run.EmbeddingRevision)
	}
	return outcome, nil
}

func (p *pipeline) executeEmbedding(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	outcome := nextPipelineOutcome(run, store.SemanticRefreshEmbedding)
	outcome.Checkpoint = embeddingCheckpoint(run.EmbeddingRevision)
	beforeProvider := func(ctx context.Context, selected int) error {
		if selected < 0 || selected > p.options.EmbeddingBatch {
			return fmt.Errorf("semantic embedding selected count is outside the bounded batch")
		}
		profile, err := p.store.RetrievalEmbeddingProfile(ctx, p.profileID)
		if err != nil {
			if errors.Is(err, store.ErrRetrievalEmbeddingProfileNotFound) {
				profile = store.RetrievalEmbeddingProfileRow{}
			} else {
				return err
			}
		}
		if profile.L0ReadyCount < 0 {
			return fmt.Errorf("semantic embedding profile has a negative L0 count")
		}
		if profile.L0ReadyCount <= p.effectiveSegmentHardLimit-selected {
			return nil
		}
		var result semanticbuild.FlushResult
		err = withRefreshGenerationExclusive(
			ctx,
			refreshLockMetadata(run, "embedding_l0_generation"),
			func(ctx context.Context) error {
				var flushErr error
				result, flushErr = p.runFlush(
					ctx,
					p.store,
					p.options.Native,
					p.flushOptions(),
				)
				return flushErr
			},
		)
		if err != nil {
			return err
		}
		if err := applyFlushCount(&outcome, result, p.effectiveSegmentTarget); err != nil {
			return err
		}
		if result.GenerationID != "" {
			outcome.CurrentGenerationID = result.GenerationID
		}
		if result.SnapshotRevision > 0 {
			outcome.EmbeddingRevision = result.SnapshotRevision
			outcome.Checkpoint = embeddingCheckpoint(result.SnapshotRevision)
		}
		return nil
	}
	result, err := p.runEmbed(ctx, p.store, p.options.Provider, semanticbuild.EmbedBatchOptions{
		BatchSize:      p.options.EmbeddingBatch,
		Now:            p.options.Now,
		Sleep:          p.options.Sleep,
		BeforeProvider: beforeProvider,
	})
	if result.Revision < 0 {
		err = errors.Join(err, fmt.Errorf("semantic embedding returned a negative revision"))
	} else if result.Revision > 0 {
		outcome.EmbeddingRevision = result.Revision
		outcome.Checkpoint = embeddingCheckpoint(result.Revision)
	}
	if result.Generated < 0 {
		err = errors.Join(err, fmt.Errorf("semantic embedding returned a negative generated count"))
	} else if counterErr := addPipelineCounter(
		&outcome.Counters.EmbeddedChunks,
		result.Generated,
	); counterErr != nil {
		err = errors.Join(err, counterErr)
	}
	if !result.HasMore && err == nil {
		outcome.NextStage = store.SemanticRefreshFlush
		outcome.Checkpoint = flushCheckpoint(outcome.EmbeddingRevision)
	}
	if err != nil {
		code := ErrorEmbedding
		if errors.Is(err, semanticbuild.ErrEmbedCircuitOpen) {
			code = ErrorEmbeddingCircuit
		}
		return outcome, pipelineStageError(code, run, outcome, err)
	}
	return outcome, nil
}

func (p *pipeline) executeFlush(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	outcome := nextPipelineOutcome(run, store.SemanticRefreshFlush)
	outcome.Checkpoint = flushCheckpoint(run.EmbeddingRevision)
	profile, err := p.store.RetrievalEmbeddingProfile(ctx, p.profileID)
	if err != nil {
		if errors.Is(err, store.ErrRetrievalEmbeddingProfileNotFound) {
			outcome.NextStage = store.SemanticRefreshCompaction
			outcome.Checkpoint = compactionCheckpoint(run.CurrentGenerationID)
			return outcome, nil
		}
		return outcome, pipelineStageError(ErrorFlush, run, outcome, err)
	}
	if profile.L0ReadyCount < 0 {
		err := fmt.Errorf("semantic embedding profile has a negative L0 count")
		return outcome, pipelineStageError(ErrorFlush, run, outcome, err)
	}
	if profile.L0ReadyCount <= p.effectiveSegmentTarget {
		outcome.NextStage = store.SemanticRefreshCompaction
		outcome.Checkpoint = compactionCheckpoint(run.CurrentGenerationID)
		return outcome, nil
	}
	result, err := p.runFlush(ctx, p.store, p.options.Native, p.flushOptions())
	if err != nil {
		return outcome, pipelineStageError(ErrorFlush, run, outcome, err)
	}
	if err := applyFlushCount(&outcome, result, p.effectiveSegmentTarget); err != nil {
		return outcome, pipelineStageError(ErrorFlush, run, outcome, err)
	}
	if result.GenerationID != "" {
		outcome.CurrentGenerationID = result.GenerationID
	}
	if result.SnapshotRevision > 0 {
		outcome.EmbeddingRevision = result.SnapshotRevision
		outcome.Checkpoint = flushCheckpoint(result.SnapshotRevision)
	}
	return outcome, nil
}

func (p *pipeline) executeCompaction(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	outcome := nextPipelineOutcome(run, store.SemanticRefreshCompaction)
	outcome.Checkpoint = compactionCheckpoint(run.CurrentGenerationID)
	_, profileErr := p.store.RetrievalEmbeddingProfile(ctx, p.profileID)
	if profileErr != nil {
		if errors.Is(profileErr, store.ErrRetrievalEmbeddingProfileNotFound) {
			outcome.NextStage = store.SemanticRefreshVerify
			outcome.Checkpoint = verifyCheckpoint("")
			return outcome, nil
		}
		return outcome, pipelineStageError(
			ErrorCompaction,
			run,
			outcome,
			profileErr,
		)
	}
	if err := p.recoverDanglingGeneration(ctx, run); err != nil {
		code := ErrorCompaction
		if errors.Is(err, errDanglingNativeRootVerification) {
			code = ErrorNativeRoot
		}
		return outcome, pipelineStageError(code, run, outcome, err)
	}
	result, err := p.runCompact(
		ctx,
		p.store,
		p.options.Native,
		semanticbuild.CompactionOptions{
			Profile:        p.options.Profile,
			Backend:        semanticindex.BackendUSearch,
			BackendVersion: semanticindex.USearchVersion,
			DistanceMetric: "cosine",
			CacheDir:       p.options.CacheDir,
		},
	)
	if err != nil {
		return outcome, pipelineStageError(ErrorCompaction, run, outcome, err)
	}
	if result.Plan.Kind == semanticbuild.SegmentCompactionNone {
		outcome.NextStage = store.SemanticRefreshVerify
		outcome.Checkpoint = verifyCheckpoint("")
		return outcome, nil
	}
	if result.GenerationID == "" {
		err := fmt.Errorf("semantic compaction completed without a replacement generation")
		return outcome, pipelineStageError(ErrorCompaction, run, outcome, err)
	}
	if result.StreamedLiveMembers < 0 {
		err := fmt.Errorf("semantic compaction returned a negative live-vector count")
		return outcome, pipelineStageError(ErrorCompaction, run, outcome, err)
	}
	outcome.CurrentGenerationID = result.GenerationID
	outcome.Checkpoint = compactionCheckpoint(result.GenerationID)
	if err := addPipelineCounter(
		&outcome.Counters.CompactedVectors,
		result.StreamedLiveMembers,
	); err != nil {
		return outcome, pipelineStageError(ErrorCompaction, run, outcome, err)
	}
	return outcome, nil
}

func (p *pipeline) recoverDanglingGeneration(ctx context.Context, run store.SemanticRefreshRun) error {
	candidate, err := p.store.RetrievalDanglingGenerationRecovery(ctx, p.profileID)
	if err != nil || candidate == nil {
		return err
	}
	if candidate.ProfileID != p.profileID || candidate.PurgeEpoch != run.PurgeEpoch ||
		candidate.Dimensions != p.options.Profile.Dimensions ||
		candidate.BackendVersion != semanticindex.USearchVersion {
		return fmt.Errorf("dangling semantic root does not match the active refresh run")
	}
	databaseID, err := p.store.RetrievalDatabaseID(ctx)
	if err != nil {
		return err
	}
	if err := p.options.Native.VerifyRoot(ctx, RootExpectation{
		CacheDir:         p.options.CacheDir,
		DatabaseID:       databaseID,
		ProfileID:        candidate.ProfileID,
		GenerationID:     candidate.GenerationID,
		DescriptorSHA256: candidate.DescriptorSHA256,
		SnapshotRevision: candidate.SnapshotRevision,
		PurgeEpoch:       candidate.PurgeEpoch,
		Dimensions:       candidate.Dimensions,
		BackendVersion:   candidate.BackendVersion,
	}); err != nil {
		return errors.Join(
			errDanglingNativeRootVerification,
			fmt.Errorf("verify dangling semantic root before recovery: %w", err),
		)
	}
	if err := p.store.ReactivateRetrievalDanglingGeneration(ctx, *candidate); err != nil {
		return fmt.Errorf("reactivate verified dangling semantic root: %w", err)
	}
	return nil
}

func (p *pipeline) executeVerify(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	outcome := nextPipelineOutcome(run, store.SemanticRefreshVerify)
	resume, err := parseVerifyCheckpoint(run.Checkpoint)
	if err != nil {
		return outcome, pipelineStageError(ErrorVerify, run, outcome, err)
	}
	outcome.Checkpoint = verifyCheckpoint(resume)
	page, err := p.runVerify(ctx, p.store, semanticbuild.VerifyOptions{
		Profile:        p.options.Profile,
		Limit:          maxVerifyBatch,
		Resume:         resume,
		RepairCounters: false,
	})
	if page.Scanned < 0 {
		err = errors.Join(err, fmt.Errorf("semantic verify returned a negative scanned count"))
	} else if counterErr := addPipelineCounter(
		&outcome.Counters.VerifiedVectors,
		page.Scanned,
	); counterErr != nil {
		err = errors.Join(err, counterErr)
	}
	if page.Resume != "" {
		checkpoint, checkpointErr := checkedVerifyCheckpoint(page.Resume)
		if checkpointErr != nil {
			err = errors.Join(err, checkpointErr)
		} else {
			outcome.Checkpoint = checkpoint
		}
	}
	if err != nil {
		return outcome, pipelineStageError(ErrorVerify, run, outcome, err)
	}
	if page.HasMore {
		if page.Resume == "" {
			err := fmt.Errorf("semantic verify has more work without a resume cursor")
			return outcome, pipelineStageError(ErrorVerify, run, outcome, err)
		}
		return outcome, nil
	}
	profile, err := p.store.RetrievalEmbeddingProfile(ctx, p.profileID)
	if err != nil {
		if errors.Is(err, store.ErrRetrievalEmbeddingProfileNotFound) {
			outcome.CurrentGenerationID = ""
			outcome.NextStage = store.SemanticRefreshReadiness
			outcome.Checkpoint = readinessCheckpoint("")
			return outcome, nil
		}
		return outcome, pipelineStageError(ErrorVerify, run, outcome, err)
	}
	if profile.ActiveGenerationID != "" {
		if err := p.verifyActiveRoot(ctx, run, profile); err != nil {
			return outcome, pipelineStageError(ErrorNativeRoot, run, outcome, err)
		}
		outcome.CurrentGenerationID = profile.ActiveGenerationID
	} else {
		outcome.CurrentGenerationID = ""
	}
	outcome.NextStage = store.SemanticRefreshReadiness
	outcome.Checkpoint = readinessCheckpoint("")
	return outcome, nil
}

func (p *pipeline) verifyActiveRoot(
	ctx context.Context,
	run store.SemanticRefreshRun,
	profile store.RetrievalEmbeddingProfileRow,
) error {
	snapshot, err := p.store.SemanticReadinessSnapshotAt(
		ctx,
		p.options.Profile,
		p.options.ExactMaxChunks,
		p.options.Now(),
	)
	if err != nil {
		return err
	}
	if snapshot.ActiveGenerationID != profile.ActiveGenerationID ||
		!snapshot.ActiveGenerationValid ||
		strings.TrimSpace(snapshot.ActiveGenerationRootDescriptorSHA256) == "" ||
		snapshot.ActiveSnapshotRevision != profile.ActiveSnapshotRevision ||
		snapshot.ProfilePurgeEpoch != profile.PurgeEpoch {
		return fmt.Errorf("active semantic root is not proven by the authoritative readiness snapshot")
	}
	purgeEpoch, err := p.store.RetrievalPurgeEpoch(ctx)
	if err != nil {
		return err
	}
	if purgeEpoch != profile.PurgeEpoch ||
		purgeEpoch != snapshot.GlobalPurgeEpoch ||
		purgeEpoch != snapshot.ProfilePurgeEpoch ||
		purgeEpoch != run.PurgeEpoch {
		return fmt.Errorf("active semantic root purge epoch changed")
	}
	databaseID, err := p.store.RetrievalDatabaseID(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(databaseID) == "" {
		return fmt.Errorf("active semantic root database ID is empty")
	}
	return p.options.Native.VerifyRoot(ctx, RootExpectation{
		CacheDir:         p.options.CacheDir,
		DatabaseID:       databaseID,
		ProfileID:        p.profileID,
		GenerationID:     snapshot.ActiveGenerationID,
		DescriptorSHA256: snapshot.ActiveGenerationRootDescriptorSHA256,
		SnapshotRevision: snapshot.ActiveSnapshotRevision,
		PurgeEpoch:       purgeEpoch,
		Dimensions:       p.options.Profile.Dimensions,
		BackendVersion:   semanticindex.USearchVersion,
	})
}

func (p *pipeline) executeReadiness(
	ctx context.Context,
	run store.SemanticRefreshRun,
) (StageOutcome, error) {
	outcome := nextPipelineOutcome(run, store.SemanticRefreshReadiness)
	snapshot, err := p.store.SemanticReadinessSnapshotAt(
		ctx,
		p.options.Profile,
		p.options.ExactMaxChunks,
		p.options.Now(),
	)
	if err != nil {
		return outcome, pipelineStageError(ErrorReadiness, run, outcome, err)
	}
	outcome.Debt = pipelineDebt(snapshot)

	// An active generation is eligible for readiness evaluation only when the
	// immediately preceding verify stage proved that exact immutable root.
	if snapshot.ActiveGenerationID != run.CurrentGenerationID {
		outcome.NextStage = store.SemanticRefreshCompaction
		outcome.CurrentGenerationID = ""
		outcome.Checkpoint = compactionCheckpoint("")
		outcome.Readiness = ""
		return outcome, nil
	}

	snapshot.Configured = true
	snapshot.Enabled = true
	decision := semanticreadiness.Evaluate(snapshot)
	outcome.Readiness = boundedProtocolField(string(decision.State), errorReadinessLimit)
	outcome.Checkpoint = readinessCheckpoint(outcome.Readiness)

	revision, err := p.store.ProjectionWorkRevision(ctx)
	if err != nil {
		return outcome, pipelineStageError(ErrorReadiness, run, outcome, err)
	}
	if revision < run.ProjectionWatermark {
		err := fmt.Errorf("semantic projection revision predates the run watermark")
		return outcome, pipelineStageError(ErrorReadiness, run, outcome, err)
	}
	if revision > run.ProjectionWatermark {
		successor := revision
		outcome.SuccessorWatermark = &successor
		outcome.Complete = true
		return outcome, nil
	}
	if decision.State != semanticreadiness.StateReady ||
		snapshot.L0ReadyCount > p.effectiveSegmentTarget {
		err := fmt.Errorf("semantic readiness gates are not satisfied")
		return outcome, pipelineStageError(ErrorReadiness, run, outcome, err)
	}
	outcome.Complete = true
	return outcome, nil
}

func (p *pipeline) flushOptions() semanticbuild.FlushOptions {
	return semanticbuild.FlushOptions{
		Profile:        p.options.Profile,
		Backend:        semanticindex.BackendUSearch,
		BackendVersion: semanticindex.USearchVersion,
		DistanceMetric: "cosine",
		CacheDir:       p.options.CacheDir,
		Limit:          p.effectiveSegmentTarget,
	}
}

func (p *pipeline) validateEffectiveSegmentBoundaries() error {
	if p.effectiveSegmentTarget < 1 ||
		p.effectiveSegmentTarget > store.RetrievalSegmentTarget {
		return fmt.Errorf(
			"semantic refresh effective segment target must be between 1 and %d",
			store.RetrievalSegmentTarget,
		)
	}
	if p.effectiveSegmentHardLimit < p.effectiveSegmentTarget ||
		p.effectiveSegmentHardLimit > store.RetrievalSegmentHardLimit {
		return fmt.Errorf(
			"semantic refresh effective segment hard limit must be between target and %d",
			store.RetrievalSegmentHardLimit,
		)
	}
	if p.options.EmbeddingBatch > p.effectiveSegmentTarget {
		return fmt.Errorf("semantic refresh embedding batch must not exceed effective segment target")
	}
	return nil
}

func nextPipelineOutcome(
	run store.SemanticRefreshRun,
	stage store.SemanticRefreshStage,
) StageOutcome {
	return StageOutcome{
		NextStage:           stage,
		Checkpoint:          boundedProtocolField(run.Checkpoint, errorCheckpointLimit),
		EmbeddingRevision:   run.EmbeddingRevision,
		Counters:            run.Counters,
		CurrentGenerationID: boundedProtocolField(run.CurrentGenerationID, generationIDLimit),
		Readiness:           boundedProtocolField(run.ReadinessState, errorReadinessLimit),
	}
}

func applyFlushCount(outcome *StageOutcome, result semanticbuild.FlushResult, target int) error {
	if result.Flushed != target {
		return fmt.Errorf(
			"semantic flush committed delta must equal %d",
			target,
		)
	}
	if result.Indexed < result.Flushed {
		return fmt.Errorf("semantic flush total membership is below its committed delta")
	}
	if result.GenerationID == "" || !validGenerationID(result.GenerationID) {
		return fmt.Errorf("semantic flush generation ID is invalid")
	}
	if result.SnapshotRevision <= 0 {
		return fmt.Errorf("semantic flush snapshot revision must be positive")
	}
	return addPipelineCounter(&outcome.Counters.FlushedVectors, result.Flushed)
}

func addPipelineCounter(counter *int64, delta int) error {
	if delta < 0 {
		return fmt.Errorf("semantic refresh counter delta must not be negative")
	}
	if *counter < 0 || int64(delta) > math.MaxInt64-*counter {
		return fmt.Errorf("semantic refresh counter overflow")
	}
	*counter += int64(delta)
	return nil
}

func pipelineDebt(snapshot semanticreadiness.Snapshot) Debt {
	return Debt{
		DirtyParents:      nonnegativeCount(snapshot.DirtyParents),
		PendingEmbeddings: nonnegativeCount(snapshot.PendingEmbeddings),
		DueRetries:        nonnegativeCount(snapshot.DueRetries),
		ScheduledRetries:  nonnegativeCount(snapshot.ScheduledRetries),
		BlockedEmbeddings: nonnegativeCount(snapshot.BlockedEmbeddings),
		FailedEmbeddings:  nonnegativeCount(snapshot.ErrorEmbeddings),
		Indexed:           nonnegativeCount(snapshot.ActiveIndexedCount),
		L0Ready:           nonnegativeCount(snapshot.L0ReadyCount),
		Tombstones:        nonnegativeCount(snapshot.ActiveTombstones),
		Segments: saturatingCountSum(
			snapshot.BuildingGenerations,
			snapshot.StaleGenerations,
			snapshot.ErrorGenerations,
		),
	}
}

func nonnegativeCount(value int) int {
	return max(value, 0)
}

func saturatingCountSum(values ...int) int {
	total := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if value > math.MaxInt-total {
			return math.MaxInt
		}
		total += value
	}
	return total
}

func pipelineStageError(
	code string,
	run store.SemanticRefreshRun,
	outcome StageOutcome,
	cause error,
) *RefreshError {
	var lockFailure semanticLockFailure
	if errors.As(cause, &lockFailure) {
		code = ErrorLockUnavailable
	}
	errorState := run
	errorState.Checkpoint = boundedProtocolField(outcome.Checkpoint, errorCheckpointLimit)
	errorState.ReadinessState = boundedProtocolField(outcome.Readiness, errorReadinessLimit)
	return NewError(code, errorState, outcome.Readiness, outcome.Debt, cause)
}

func projectionCheckpoint(watermark int64) string {
	return fmt.Sprintf("%s%d", projectionCheckpointPrefix, max(watermark, 0))
}

func embeddingCheckpoint(revision int64) string {
	return fmt.Sprintf("%s%d", embeddingCheckpointPrefix, max(revision, 0))
}

func flushCheckpoint(revision int64) string {
	return fmt.Sprintf("%s%d", flushCheckpointPrefix, max(revision, 0))
}

func compactionCheckpoint(generationID string) string {
	generationID = boundedProtocolField(generationID, generationIDLimit)
	if generationID == "" {
		return strings.TrimSuffix(compactionCheckpointPrefix, "=")
	}
	return compactionCheckpointPrefix + generationID
}

func verifyCheckpoint(resume string) string {
	if resume == "" {
		return strings.TrimSuffix(verifyCheckpointPrefix, "=")
	}
	return verifyCheckpointPrefix + resume
}

func checkedVerifyCheckpoint(resume string) (string, error) {
	resume = boundedProtocolField(
		resume,
		errorCheckpointLimit-len(verifyCheckpointPrefix),
	)
	if resume == "" {
		return "", fmt.Errorf("semantic verify resume cursor is not a bounded protocol value")
	}
	return verifyCheckpoint(resume), nil
}

func parseVerifyCheckpoint(checkpoint string) (string, error) {
	if checkpoint == "" ||
		checkpoint == strings.TrimSuffix(verifyCheckpointPrefix, "=") {
		return "", nil
	}
	if !strings.HasPrefix(checkpoint, verifyCheckpointPrefix) {
		// A first verify unit follows the compaction checkpoint.
		if strings.HasPrefix(checkpoint, compactionCheckpointPrefix) ||
			checkpoint == strings.TrimSuffix(compactionCheckpointPrefix, "=") {
			return "", nil
		}
		return "", fmt.Errorf("semantic verify checkpoint is invalid")
	}
	resume := strings.TrimPrefix(checkpoint, verifyCheckpointPrefix)
	checked, err := checkedVerifyCheckpoint(resume)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(checked, verifyCheckpointPrefix), nil
}

func readinessCheckpoint(state string) string {
	state = boundedProtocolField(state, errorReadinessLimit)
	if state == "" {
		return strings.TrimSuffix(readinessCheckpointPrefix, "=")
	}
	return readinessCheckpointPrefix + state
}
