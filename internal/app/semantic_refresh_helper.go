package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
)

const semanticRefreshCapabilityReasonLimit = 256

type semanticRefreshDeps struct {
	resolve         func(string) (semanticconfig.Config, error)
	capability      func() semanticindex.Capability
	openWritable    func(string) (*store.Store, error)
	provider        func(semanticconfig.Config) (embedding.Provider, error)
	nativeLifecycle func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error)
	runRefresh      func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error)
}

func defaultSemanticRefreshDeps() semanticRefreshDeps {
	return semanticRefreshDeps{
		resolve:      semanticconfig.Resolve,
		capability:   semanticindex.RuntimeCapability,
		openWritable: store.Open,
		provider: func(cfg semanticconfig.Config) (embedding.Provider, error) {
			return embedding.NewOllama(embedding.OllamaOptions{
				BaseURL:    cfg.OllamaBaseURL,
				Model:      cfg.Model,
				Dimensions: cfg.Dimensions,
			})
		},
		nativeLifecycle: newSemanticNativeLifecycle,
		runRefresh:      semanticrefresh.Run,
	}
}

func runConfiguredSemanticRefresh(
	ctx context.Context,
	cfg config.Config,
	deps semanticRefreshDeps,
	progress semanticrefresh.ProgressCallback,
) (semanticrefresh.Result, error) {
	deps = completeSemanticRefreshDeps(deps)
	result := semanticrefresh.Result{}
	if ctx == nil {
		return result, semanticrefresh.NewError(
			semanticrefresh.ErrorBackendBroken,
			store.SemanticRefreshRun{},
			"",
			semanticrefresh.Debt{},
			fmt.Errorf("semantic refresh context is required"),
		)
	}

	semanticCfg, err := deps.resolve(cfg.RootDir)
	if err != nil {
		result.Capability = publicSemanticRefreshCapability(deps.capability())
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorBackendBroken,
			"",
			err,
		)
	}
	mode := semanticCfg.Mode
	capability := deps.capability()
	result.Capability = publicSemanticRefreshCapability(capability)
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}
	if mode == semanticconfig.ModeOff {
		result.Outcome = semanticrefresh.OutcomeSkipped
		result.SkipReason = "semantic_mode_off"
		return result, nil
	}

	admitted, reason := capability.Admit(
		semanticindex.BackendUSearch,
		semanticindex.USearchVersion,
	)
	if !admitted {
		if capability.State == semanticindex.CapabilityUnsupported {
			result.Outcome = semanticrefresh.OutcomeSkipped
			result.SkipReason = "native_backend_unsupported"
			return result, nil
		}
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorBackendBroken,
			"",
			errors.New(boundedSemanticRefreshDiagnostic(reason)),
		)
	}

	profile := semanticbuild.Profile(embedding.Info{
		Provider:   string(semanticCfg.Provider),
		Model:      semanticCfg.Model,
		Dimensions: semanticCfg.Dimensions,
	})
	profileID, err := profile.ID()
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorBackendBroken,
			"",
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}

	st, err := deps.openWritable(cfg.DBPath)
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorBackendBroken,
			"",
			err,
		)
	}
	if st == nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorBackendBroken,
			"",
			fmt.Errorf("semantic refresh writable store is unavailable"),
		)
	}
	defer func() { _ = st.Close() }()
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}

	provider, err := deps.provider(semanticCfg)
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorEmbedding,
			store.SemanticRefreshEmbedding,
			err,
		)
	}
	if provider == nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorEmbedding,
			store.SemanticRefreshEmbedding,
			fmt.Errorf("semantic refresh embedding provider is unavailable"),
		)
	}
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}

	native, err := deps.nativeLifecycle(semanticCfg)
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorNativeRoot,
			store.SemanticRefreshVerify,
			err,
		)
	}
	if native == nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorNativeRoot,
			store.SemanticRefreshVerify,
			fmt.Errorf("semantic refresh native lifecycle is unavailable"),
		)
	}
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}

	purgeEpoch, err := st.RetrievalPurgeEpoch(ctx)
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorProjection,
			store.SemanticRefreshProjection,
			err,
		)
	}
	projectionWatermark, err := st.ProjectionWorkRevision(ctx)
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorProjection,
			store.SemanticRefreshProjection,
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}

	executor, err := semanticrefresh.NewPipeline(st, semanticrefresh.PipelineOptions{
		Profile:        profile,
		Provider:       provider,
		Native:         native,
		CacheDir:       cfg.CacheDir,
		ExactMaxChunks: semanticCfg.ExactFallbackMaxChunks,
	})
	if err != nil {
		return configuredSemanticRefreshError(
			ctx,
			result,
			semanticrefresh.ErrorBackendBroken,
			"",
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return cancelledConfiguredSemanticRefresh(result, err)
	}

	result, err = deps.runRefresh(ctx, st, executor, semanticrefresh.Request{
		ProfileID:           profileID,
		PurgeEpoch:          purgeEpoch,
		ProjectionWatermark: projectionWatermark,
		Capability:          capability,
		Progress:            progress,
		Now:                 time.Now,
	})
	result.Capability = publicSemanticRefreshCapability(capability)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return cancelledConfiguredSemanticRefresh(result, errors.Join(ctxErr, err))
	}
	if err == nil {
		return result, nil
	}
	var refreshErr *semanticrefresh.RefreshError
	if errors.As(err, &refreshErr) {
		return result, refreshErr
	}
	return configuredSemanticRefreshError(
		ctx,
		result,
		semanticrefresh.ErrorBackendBroken,
		"",
		err,
	)
}

func completeSemanticRefreshDeps(deps semanticRefreshDeps) semanticRefreshDeps {
	defaults := defaultSemanticRefreshDeps()
	if deps.resolve == nil {
		deps.resolve = defaults.resolve
	}
	if deps.capability == nil {
		deps.capability = defaults.capability
	}
	if deps.openWritable == nil {
		deps.openWritable = defaults.openWritable
	}
	if deps.provider == nil {
		deps.provider = defaults.provider
	}
	if deps.nativeLifecycle == nil {
		deps.nativeLifecycle = defaults.nativeLifecycle
	}
	if deps.runRefresh == nil {
		deps.runRefresh = defaults.runRefresh
	}
	return deps
}

func configuredSemanticRefreshError(
	ctx context.Context,
	result semanticrefresh.Result,
	code string,
	stage store.SemanticRefreshStage,
	cause error,
) (semanticrefresh.Result, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return cancelledConfiguredSemanticRefresh(result, errors.Join(err, cause))
		}
	}
	run := store.SemanticRefreshRun{Stage: stage}
	if result.Run != nil {
		run = *result.Run
		if run.Stage == "" {
			run.Stage = stage
		}
	}
	return result, semanticrefresh.NewError(code, run, run.ReadinessState, result.Debt, cause)
}

func cancelledConfiguredSemanticRefresh(
	result semanticrefresh.Result,
	cause error,
) (semanticrefresh.Result, error) {
	result.Outcome = ""
	result.SkipReason = ""
	run := store.SemanticRefreshRun{}
	if result.Run != nil {
		run = *result.Run
	}
	return result, semanticrefresh.NewError(
		semanticrefresh.ErrorCancelled,
		run,
		run.ReadinessState,
		result.Debt,
		cause,
	)
}

func publicSemanticRefreshCapability(capability semanticindex.Capability) semanticindex.Capability {
	switch capability.State {
	case semanticindex.CapabilitySupportedBroken:
		_, reason := capability.Admit(
			semanticindex.BackendUSearch,
			semanticindex.USearchVersion,
		)
		reason = strings.TrimPrefix(reason, "native_backend_broken:")
		capability.Reason = boundedSemanticRefreshDiagnostic(reason)
	case semanticindex.CapabilitySupportedReady:
		capability.Reason = ""
	case semanticindex.CapabilityUnsupported:
		capability.Reason = ""
	default:
		capability.Reason = ""
	}
	return capability
}

func boundedSemanticRefreshDiagnostic(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= semanticRefreshCapabilityReasonLimit {
		return value
	}
	end := semanticRefreshCapabilityReasonLimit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
