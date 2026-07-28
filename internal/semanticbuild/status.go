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
	Problems          []string                   `json:"problems"`
	Next              []string                   `json:"next_steps"`
}

type StatusStore interface {
	SemanticReadinessSnapshotAt(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
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
		return statusFromDecision(result, snapshot, capability), nil
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
			result.Status = string(semanticreadiness.StateUnavailable)
			result.Reason = "native_root_artifacts_unavailable"
			result.Searchable = false
			result.Problems = append(result.Problems, "native root artifacts failed validation")
		}
	}
	return result, nil
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
