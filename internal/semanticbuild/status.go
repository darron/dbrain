package semanticbuild

import (
	"context"
	"errors"
	"time"

	"github.com/darron/dbrain/internal/store"
)

type Status struct {
	Status    string                `json:"status"`
	Reason    string                `json:"reason"`
	Mode      string                `json:"mode"`
	ProfileID string                `json:"profile_id"`
	Store     store.RetrievalStatus `json:"store"`
	Problems  []string              `json:"problems"`
	Next      []string              `json:"next_steps"`
}

type StatusStore interface {
	RetrievalStatusAt(context.Context, string, time.Time) (store.RetrievalStatus, error)
}

func ReadStatus(ctx context.Context, st StatusStore, profileID string, configured bool, enabled bool, now time.Time) (Status, error) {
	result := Status{Problems: make([]string, 0), Next: make([]string, 0)}
	if !configured {
		result.Status, result.Reason = "not_configured", "embedding model and positive dimensions are required"
		return result, nil
	}
	storage, err := st.RetrievalStatusAt(ctx, profileID, now)
	if !enabled {
		if err != nil && !errors.Is(err, store.ErrRetrievalUnavailable) {
			return result, err
		}
		if err == nil {
			result.Store = storage
		}
		result.Status, result.Reason = "disabled", "semantic retrieval mode is off"
		return result, nil
	}
	if errors.Is(err, store.ErrRetrievalUnavailable) {
		result.Status, result.Reason = "unavailable", "retrieval schema is unavailable"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Store = storage
	result.Status = "ready"
	return result, nil
}
