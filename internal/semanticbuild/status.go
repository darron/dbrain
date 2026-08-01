package semanticbuild

import (
	"context"
	"errors"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

type Status struct {
	Status            string                     `json:"status"`
	Reason            string                     `json:"reason"`
	Searchable        bool                       `json:"searchable"`
	Mode              string                     `json:"mode"`
	ProfileID         string                     `json:"profile_id"`
	BackendCapability semanticindex.Capability   `json:"backend_capability"`
	Store             semanticreadiness.Snapshot `json:"store"`
	LatestRun         *store.SemanticRefreshRun  `json:"latest_run"`
	Problems          []string                   `json:"problems"`
	Next              []string                   `json:"next_steps"`
}

type StatusStore interface {
	SemanticReadinessSnapshotAt(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
	LatestSemanticRefreshRun(context.Context, string) (*store.SemanticRefreshRun, error)
}

func ReadStatus(
	ctx context.Context,
	st StatusStore,
	profile embedding.Profile,
	configured bool,
	enabled bool,
	exactMaxChunks int,
	capability semanticindex.Capability,
	now time.Time,
) (Status, error) {
	return ReadStatusWithNativeValidation(ctx, st, profile, configured, enabled, exactMaxChunks, capability, now, nil)
}

// ReadStatusWithNativeValidation reports status after optionally checking that
// an admitted native root can be opened by the runtime.
func ReadStatusWithNativeValidation(
	ctx context.Context,
	st StatusStore,
	profile embedding.Profile,
	configured bool,
	enabled bool,
	exactMaxChunks int,
	capability semanticindex.Capability,
	now time.Time,
	validateNativeRoot func(context.Context, semanticreadiness.Snapshot) error,
) (Status, error) {
	result := Status{BackendCapability: capability, Problems: make([]string, 0), Next: make([]string, 0)}
	exactMaxChunks = semanticreadiness.EffectiveExactMaxChunks(exactMaxChunks)
	if configured {
		profileID, err := profile.ID()
		if err != nil {
			return result, err
		}
		result.ProfileID = profileID
	}
	snapshot := semanticreadiness.Snapshot{Configured: configured, Enabled: enabled, ExactMaxChunks: exactMaxChunks, Now: now.UTC()}
	if !configured {
		result = statusFromDecision(result, snapshot, capability)
		return statusWithLatestRun(ctx, st, result, "")
	}
	if st != nil {
		storage, err := st.SemanticReadinessSnapshotAt(ctx, profile, exactMaxChunks, now)
		if err == nil {
			snapshot = storage
			snapshot.Configured, snapshot.Enabled = true, enabled
			snapshot.ExactMaxChunks = exactMaxChunks
			snapshot.Now = now.UTC()
		} else if !errors.Is(err, store.ErrRetrievalUnavailable) {
			return result, err
		}
	}
	result = statusFromDecision(result, snapshot, capability)
	if result.Searchable && snapshot.ActiveGenerationID != "" && validateNativeRoot != nil {
		if err := validateNativeRoot(ctx, snapshot); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result, err
			}
			result.Status = string(semanticreadiness.StateUnavailable)
			result.Reason = "native_root_artifacts_unavailable"
			result.Searchable = false
			result.Problems = append(result.Problems, "native root artifacts failed validation")
		}
	}
	latestProfileID := result.ProfileID
	if !enabled {
		latestProfileID = ""
	}
	if !result.Store.Available {
		return result, nil
	}
	return statusWithLatestRun(ctx, st, result, latestProfileID)
}

func statusWithLatestRun(
	ctx context.Context,
	st StatusStore,
	result Status,
	profileID string,
) (Status, error) {
	if st == nil {
		return result, nil
	}
	latest, err := st.LatestSemanticRefreshRun(ctx, profileID)
	if err != nil {
		if errors.Is(err, store.ErrRetrievalUnavailable) {
			return result, nil
		}
		return result, err
	}
	if latest == nil {
		return result, nil
	}
	bounded := *latest
	bounded.RunID = boundedStatusProtocolField(bounded.RunID, 64)
	bounded.ProfileID = boundedStatusProtocolField(bounded.ProfileID, 192)
	bounded.Checkpoint = boundedStatusProtocolField(bounded.Checkpoint, 256)
	bounded.CurrentGenerationID = boundedStatusProtocolField(bounded.CurrentGenerationID, 64)
	bounded.ErrorCode = boundedStatusProtocolField(bounded.ErrorCode, 64)
	bounded.ErrorText = ""
	bounded.ReadinessState = boundedStatusProtocolField(bounded.ReadinessState, 64)
	result.LatestRun = &bounded
	return result, nil
}

func boundedStatusProtocolField(value string, limit int) string {
	if value == "" || len(value) > limit {
		return ""
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '.', character == '_', character == ':',
			character == '=', character == '+', character == '-':
		default:
			return ""
		}
	}
	return value
}

func statusFromDecision(result Status, snapshot semanticreadiness.Snapshot, capability semanticindex.Capability) Status {
	decision := semanticreadiness.Evaluate(snapshot)
	if decision.Searchable && snapshot.ActiveGenerationID != "" {
		if ok, reason := capability.Admit(snapshot.ActiveGenerationBackend, snapshot.ActiveGenerationBackendVersion); !ok {
			decision = semanticreadiness.Decision{
				State:      semanticreadiness.StateUnavailable,
				Reason:     reason,
				Searchable: false,
			}
		}
	}
	result.Status, result.Reason, result.Searchable = string(decision.State), decision.Reason, decision.Searchable
	result.Store = snapshot
	return result
}
