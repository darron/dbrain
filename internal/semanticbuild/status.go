package semanticbuild

import (
	"context"
	"errors"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

type Status struct {
	Status     string                     `json:"status"`
	Reason     string                     `json:"reason"`
	Searchable bool                       `json:"searchable"`
	Mode       string                     `json:"mode"`
	ProfileID  string                     `json:"profile_id"`
	Store      semanticreadiness.Snapshot `json:"store"`
	Problems   []string                   `json:"problems"`
	Next       []string                   `json:"next_steps"`
}

type StatusStore interface {
	SemanticReadinessSnapshotAt(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
}

func ReadStatus(ctx context.Context, st StatusStore, profile embedding.Profile, configured bool, enabled bool, exactMaxChunks int, now time.Time) (Status, error) {
	result := Status{Problems: make([]string, 0), Next: make([]string, 0)}
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
		return statusFromDecision(result, snapshot), nil
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
	return statusFromDecision(result, snapshot), nil
}

func statusFromDecision(result Status, snapshot semanticreadiness.Snapshot) Status {
	decision := semanticreadiness.Evaluate(snapshot)
	result.Status, result.Reason, result.Searchable = string(decision.State), decision.Reason, decision.Searchable
	result.Store = snapshot
	return result
}
