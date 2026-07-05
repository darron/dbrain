package researchrun

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

var answerSourceKeyPattern = regexp.MustCompile(`(?i)\b(?:src|x|apple-note|feed-entry|gh-star|github_star|yt|youtube|safari-tab|manual|item):[A-Za-z0-9][A-Za-z0-9._~:/@?&=+%#-]*`)

func VerifyCitations(pack brainresearch.Pack, result brainresearch.SynthesisResult) VerificationResult {
	allowed := map[string]struct{}{}
	addEvidenceKeys(allowed, pack.Evidence)
	addEvidenceKeys(allowed, pack.ExactTagEvidence)

	verification := VerificationResult{Passed: true}
	hasEvidence := len(allowed) > 0
	answer := strings.TrimSpace(result.Answer)
	if !hasEvidence && (answer != "" || result.AnswerStatus != "no_evidence") {
		verification.Passed = false
		verification.Errors = append(verification.Errors, "no-evidence research pack produced a normal synthesis answer")
	}

	citationKeys := map[string]struct{}{}
	for _, citation := range result.Citations {
		key := strings.TrimSpace(citation.SourceKey)
		if key == "" {
			continue
		}
		citationKeys[key] = struct{}{}
		if _, ok := allowed[key]; !ok {
			verification.Passed = false
			verification.Errors = append(verification.Errors, sourceKeyMissingError("citation source_key", key, allowed))
		}
	}
	answerKeys := answerSourceKeys(answer)
	if hasEvidence && answer != "" && result.AnswerStatus != "no_evidence" && len(answerKeys) == 0 {
		verification.Passed = false
		verification.Errors = append(verification.Errors, "answer contains no source-key citations")
	}
	for _, key := range answerKeys {
		if _, ok := allowed[key]; !ok {
			verification.Passed = false
			verification.Errors = append(verification.Errors, sourceKeyMissingError("answer citation", key, allowed))
			continue
		}
		if _, ok := citationKeys[key]; !ok {
			verification.Warnings = append(verification.Warnings, fmt.Sprintf("answer cites %s but citation metadata does not include it", key))
		}
	}
	return verification
}

func addEvidenceKeys(dst map[string]struct{}, rows []ask.Evidence) {
	for _, row := range rows {
		if key := strings.TrimSpace(row.SourceKey); key != "" {
			dst[key] = struct{}{}
		}
	}
}

func answerSourceKeys(answer string) []string {
	matches := answerSourceKeyPattern.FindAllString(answer, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	keys := make([]string, 0, len(matches))
	for _, match := range matches {
		key := strings.TrimSpace(match)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func sourceKeyMissingError(label string, candidate string, allowed map[string]struct{}) string {
	if exact := exactAllowedKey(allowed, candidate); exact != "" {
		return fmt.Sprintf("%s %s must use exact source key %s", label, candidate, exact)
	}
	if near := nearestAllowedKey(allowed, candidate); near != "" {
		return fmt.Sprintf("%s %s is not present in final evidence pack; nearest evidence source key is %s", label, candidate, near)
	}
	return fmt.Sprintf("%s %s is not present in final evidence pack", label, candidate)
}

func exactAllowedKey(allowed map[string]struct{}, candidate string) string {
	for key := range allowed {
		if strings.EqualFold(key, candidate) && key != candidate {
			return key
		}
	}
	return ""
}

func nearestAllowedKey(allowed map[string]struct{}, candidate string) string {
	const maxDistance = 2
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	candidateNamespace := sourceKeyNamespace(candidate)
	if candidateNamespace == "" {
		return ""
	}
	bestKey := ""
	bestDistance := maxDistance + 1
	candidateFolded := strings.ToLower(candidate)
	for key := range allowed {
		if sourceKeyNamespace(key) != candidateNamespace {
			continue
		}
		distance := boundedEditDistance(strings.ToLower(key), candidateFolded, maxDistance)
		if distance > maxDistance {
			continue
		}
		if bestKey == "" || distance < bestDistance || (distance == bestDistance && key < bestKey) {
			bestKey = key
			bestDistance = distance
		}
	}
	return bestKey
}

func sourceKeyNamespace(key string) string {
	prefix, _, ok := strings.Cut(strings.TrimSpace(key), ":")
	if !ok || prefix == "" {
		return ""
	}
	return strings.ToLower(prefix)
}

func boundedEditDistance(left string, right string, maxDistance int) int {
	if maxDistance < 0 {
		return maxDistance + 1
	}
	if left == right {
		return 0
	}
	if absInt(len(left)-len(right)) > maxDistance {
		return maxDistance + 1
	}
	prev := make([]int, len(right)+1)
	curr := make([]int, len(right)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(left); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(right); j++ {
			cost := 0
			if left[i-1] != right[j-1] {
				cost = 1
			}
			curr[j] = minInt(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > maxDistance {
			return maxDistance + 1
		}
		prev, curr = curr, prev
	}
	if prev[len(right)] > maxDistance {
		return maxDistance + 1
	}
	return prev[len(right)]
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func minInt(values ...int) int {
	best := values[0]
	for _, value := range values[1:] {
		if value < best {
			best = value
		}
	}
	return best
}
