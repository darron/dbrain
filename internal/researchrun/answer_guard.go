package researchrun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

func GuardAnchoredAnswer(pack brainresearch.Pack, prepared brainresearch.PreparedSynthesis, synthesis brainresearch.SynthesisResult) VerificationResult {
	verification := VerificationResult{Passed: true}
	if len(pack.QueryPlan.ProtectedAnchors) == 0 || len(prepared.Citations) == 0 {
		return verification
	}
	answer := strings.TrimSpace(synthesis.Answer)
	if answer == "" {
		return verification
	}

	for _, anchor := range pack.QueryPlan.ProtectedAnchors {
		supportingKeys := preparedCitationKeysForAnchor(pack, prepared, anchor)
		if len(supportingKeys) == 0 {
			continue
		}
		if !answerDeniesAnchoredEvidence(answer, anchor) {
			continue
		}
		verification.Passed = false
		verification.Errors = append(verification.Errors, fmt.Sprintf(
			"answer claims no source material for protected anchor %s despite prepared anchored evidence citations: %s",
			anchorDisplayName(anchor),
			strings.Join(supportingKeys, ", "),
		))
	}
	return verification
}

func preparedCitationKeysForAnchor(pack brainresearch.Pack, prepared brainresearch.PreparedSynthesis, anchor brainresearch.ProtectedAnchor) []string {
	cited := map[string]struct{}{}
	for _, citation := range prepared.Citations {
		key := strings.TrimSpace(citation.SourceKey)
		if key != "" {
			cited[key] = struct{}{}
		}
	}
	if len(cited) == 0 {
		return nil
	}

	matching := map[string]struct{}{}
	addMatchingEvidenceKeys(matching, cited, pack.Evidence, anchor)
	addMatchingEvidenceKeys(matching, cited, pack.ExactTagEvidence, anchor)
	if len(matching) == 0 {
		return nil
	}
	keys := make([]string, 0, len(matching))
	for key := range matching {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addMatchingEvidenceKeys(dst map[string]struct{}, cited map[string]struct{}, rows []ask.Evidence, anchor brainresearch.ProtectedAnchor) {
	for _, row := range rows {
		key := strings.TrimSpace(row.SourceKey)
		if key == "" {
			continue
		}
		if _, ok := cited[key]; !ok {
			continue
		}
		if brainresearch.EvidenceMatchesProtectedAnchor(row, anchor) {
			dst[key] = struct{}{}
		}
	}
}

func answerDeniesAnchoredEvidence(answer string, anchor brainresearch.ProtectedAnchor) bool {
	lower := strings.ToLower(answer)
	for _, term := range anchorDenialTerms(anchor) {
		if term == "" {
			continue
		}
		if denialNearTerm(lower, term) {
			return true
		}
	}
	return false
}

func anchorDenialTerms(anchor brainresearch.ProtectedAnchor) []string {
	values := []string{
		anchor.Raw,
		anchor.Canonical,
		anchor.ResolvedID,
	}
	values = append(values, anchor.ExactTerms...)
	values = append(values, anchor.PhraseTerms...)
	if anchor.Canonical != "" {
		values = append(values, strings.NewReplacer("_", " ", "-", " ").Replace(anchor.Canonical))
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func denialNearTerm(answer string, term string) bool {
	// Keep the guard conservative: it should catch direct false no-evidence
	// claims without treating a distant caveat elsewhere in a long answer as one.
	const maxDistance = 180
	denials := []string{
		"no sources",
		"no source material",
		"no evidence",
		"does not contain",
		"do not contain",
		"corpus lacks",
		"not present",
		"not in the corpus",
		"no such sources",
	}
	termIndexes := allStringIndexes(answer, term)
	if len(termIndexes) == 0 {
		return false
	}
	for _, denial := range denials {
		for _, denialIndex := range allStringIndexes(answer, denial) {
			for _, termIndex := range termIndexes {
				if absInt(denialIndex-termIndex) <= maxDistance {
					return true
				}
			}
		}
	}
	return false
}

func allStringIndexes(value string, needle string) []int {
	if needle == "" {
		return nil
	}
	var indexes []int
	offset := 0
	for {
		idx := strings.Index(value[offset:], needle)
		if idx < 0 {
			return indexes
		}
		indexes = append(indexes, offset+idx)
		offset += idx + len(needle)
	}
}

func anchorDisplayName(anchor brainresearch.ProtectedAnchor) string {
	for _, value := range []string{anchor.Raw, anchor.ResolvedID, anchor.Canonical} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "unknown"
}

func mergeVerificationResults(results ...VerificationResult) VerificationResult {
	merged := VerificationResult{Passed: true}
	for _, result := range results {
		if !result.Passed {
			merged.Passed = false
		}
		merged.Errors = append(merged.Errors, result.Errors...)
		merged.Warnings = append(merged.Warnings, result.Warnings...)
	}
	return merged
}
