package linkextract

import (
	"encoding/json"

	"github.com/darron/dbrain/internal/model"
)

func collectCandidates(item model.Item) ([]model.SourceCandidate, error) {
	var rawLinks []string
	if err := json.Unmarshal([]byte(item.LinksJSON), &rawLinks); err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	candidates := make([]model.SourceCandidate, 0, len(rawLinks))
	for _, raw := range rawLinks {
		candidate, ok := NormalizeCandidate(raw)
		if !ok {
			continue
		}
		if _, exists := seen[candidate.NormalizedURL]; exists {
			continue
		}
		seen[candidate.NormalizedURL] = struct{}{}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}
