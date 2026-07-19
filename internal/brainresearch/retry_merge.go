package brainresearch

import (
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

type MergeRetryOptions struct {
	MissingConcepts []string
	RetryAction     string
	RetryQuestion   string
}

type MergeRetryDecision struct {
	PreservedInitialSourceKeys []string `json:"preserved_initial_source_keys,omitempty"`
	AcceptedRetrySourceKeys    []string `json:"accepted_retry_source_keys,omitempty"`
	RejectedRetrySourceKeys    []string `json:"rejected_retry_source_keys,omitempty"`
	FinalSourceKeys            []string `json:"final_source_keys,omitempty"`
	Reason                     string   `json:"reason,omitempty"`
}

func MergeRetryPack(initial Pack, retry Pack, opts MergeRetryOptions) (Pack, MergeRetryDecision) {
	decision := MergeRetryDecision{
		PreservedInitialSourceKeys: uniqueEvidenceSourceKeys(initial.Evidence),
		Reason:                     "merged retry evidence into initial pack",
	}
	acceptedRetry := make([]ask.Evidence, 0, len(retry.Evidence))
	seenInitial := evidenceIdentitySet(initial.Evidence)
	for _, row := range retry.Evidence {
		if strings.TrimSpace(row.SourceKey) == "" {
			continue
		}
		if _, exists := seenInitial[evidenceIdentity(row)]; exists {
			continue
		}
		if retryRowAccepted(initial, row, opts) {
			acceptedRetry = append(acceptedRetry, row)
			decision.AcceptedRetrySourceKeys = append(decision.AcceptedRetrySourceKeys, row.SourceKey)
			continue
		}
		decision.RejectedRetrySourceKeys = append(decision.RejectedRetrySourceKeys, row.SourceKey)
	}

	merged := initial
	if len(initial.QueryPlan.ProtectedAnchors) == 0 {
		merged.Evidence = mergeEvidenceRows(acceptedRetry, initial.Evidence)
	} else {
		merged.Evidence = mergeEvidenceRows(initial.Evidence, acceptedRetry)
	}
	merged.ExactTagEvidence = mergeEvidenceRowsBySource(initial.ExactTagEvidence, retry.ExactTagEvidence)
	merged.Coverage = mergeCoverage(buildCoverage(merged.Evidence), initial.Coverage)
	merged.Coverage.RecallNote = recallNote(merged.Coverage)
	decision.AcceptedRetrySourceKeys = uniqueStrings(decision.AcceptedRetrySourceKeys)
	decision.RejectedRetrySourceKeys = uniqueStrings(decision.RejectedRetrySourceKeys)
	decision.FinalSourceKeys = uniqueEvidenceSourceKeys(merged.Evidence)
	return merged, decision
}

func retryRowAccepted(initial Pack, row ask.Evidence, opts MergeRetryOptions) bool {
	plan := initial.QueryPlan
	if EvidenceMatchesAnyProtectedAnchor(row, plan.ProtectedAnchors) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(opts.RetryAction), "related_expansion") {
		if len(plan.ProtectedAnchors) == 0 {
			return true
		}
		if relatedExpansionFromAnchoredSource(initial, row) {
			return true
		}
	}
	return evidenceFillsMissingConcept(row, plan.Concepts, opts.MissingConcepts)
}

func relatedExpansionFromAnchoredSource(initial Pack, row ask.Evidence) bool {
	relatedKeys := trimNonEmpty([]string{row.RelatedTo})
	if row.Chunk != nil {
		relatedKeys = append(relatedKeys, strings.TrimSpace(row.Chunk.ParentSourceKey))
	}
	if len(relatedKeys) == 0 || len(initial.QueryPlan.ProtectedAnchors) == 0 {
		return false
	}
	related := map[string]struct{}{}
	for _, key := range relatedKeys {
		related[key] = struct{}{}
	}
	for _, source := range append(initial.Evidence, initial.ExactTagEvidence...) {
		if _, ok := related[strings.TrimSpace(source.SourceKey)]; ok && EvidenceMatchesAnyProtectedAnchor(source, initial.QueryPlan.ProtectedAnchors) {
			return true
		}
	}
	return false
}

func evidenceFillsMissingConcept(row ask.Evidence, concepts []QueryConcept, missing []string) bool {
	if len(missing) == 0 {
		return false
	}
	text := researchEvidenceText(row)
	for _, name := range missing {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if concept, ok := missingConceptDefinition(concepts, name); ok {
			if !conceptCanBeFilledByRetry(concept) {
				continue
			}
			if conceptMatchesText(concept, text) {
				return true
			}
			continue
		}
		if containsTerm(text, name) {
			return true
		}
	}
	return false
}

func missingConceptDefinition(concepts []QueryConcept, name string) (QueryConcept, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, concept := range concepts {
		values := append([]string{concept.Key, concept.Preferred}, concept.Terms...)
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(value), name) {
				return concept, true
			}
		}
	}
	return QueryConcept{}, false
}

func conceptCanBeFilledByRetry(concept QueryConcept) bool {
	role := strings.ToLower(strings.TrimSpace(concept.Role))
	return role == "" || role == conceptRoleContent || role == conceptRoleAnchor
}

func mergeEvidenceRows(first []ask.Evidence, second []ask.Evidence) []ask.Evidence {
	out := make([]ask.Evidence, 0, len(first)+len(second))
	seen := map[string]struct{}{}
	appendRows := func(rows []ask.Evidence) {
		for _, row := range rows {
			key := evidenceIdentity(row)
			if key == "" {
				// Research evidence must be citeable by source_key; unciteable rows are not carried into synthesized packs.
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, row)
		}
	}
	appendRows(first)
	appendRows(second)
	return out
}

func evidenceIdentity(row ask.Evidence) string {
	if row.Chunk != nil && strings.TrimSpace(row.Chunk.ID) != "" {
		return "chunk:" + strings.TrimSpace(row.Chunk.ID)
	}
	key := strings.TrimSpace(row.SourceKey)
	if key == "" {
		return ""
	}
	return "source:" + key
}
func evidenceIdentitySet(rows []ask.Evidence) map[string]struct{} {
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if key := evidenceIdentity(row); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}
func mergeEvidenceRowsBySource(first, second []ask.Evidence) []ask.Evidence {
	out := make([]ask.Evidence, 0, len(first)+len(second))
	seen := map[string]struct{}{}
	for _, rows := range [][]ask.Evidence{first, second} {
		for _, row := range rows {
			key := strings.TrimSpace(row.SourceKey)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, row)
		}
	}
	return out
}
func uniqueEvidenceSourceKeys(rows []ask.Evidence) []string {
	out := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		key := strings.TrimSpace(row.SourceKey)
		if row.Chunk != nil && strings.TrimSpace(row.Chunk.ParentSourceKey) != "" {
			key = strings.TrimSpace(row.Chunk.ParentSourceKey)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}
