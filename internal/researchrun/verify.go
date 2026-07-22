package researchrun

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

func VerifyCitations(pack brainresearch.Pack, result brainresearch.SynthesisResult) VerificationResult {
	allowed := map[string]struct{}{}
	addEvidenceKeys(allowed, pack.Evidence)
	addEvidenceKeys(allowed, pack.ExactTagEvidence)
	return verifyCitations(allowed, result)
}

// VerifyPreparedCitations verifies against the exact evidence admitted to the
// synthesis prompt, rather than the wider retrieval pack. This rejects citations
// to rows removed by relevance selection or evidence-budget truncation.
func VerifyPreparedCitations(prepared brainresearch.PreparedSynthesis, result brainresearch.SynthesisResult) VerificationResult {
	allowed := map[string]struct{}{}
	for _, citation := range prepared.Citations {
		if key := strings.TrimSpace(citation.SourceKey); key != "" {
			allowed[key] = struct{}{}
		}
	}
	return verifyCitations(allowed, result)
}

func verifyCitations(allowed map[string]struct{}, result brainresearch.SynthesisResult) VerificationResult {
	verification := VerificationResult{Passed: true}
	hasEvidence := len(allowed) > 0
	answer := strings.TrimSpace(result.Answer)
	if answerDiscussesUnrelatedCandidates(result.Question, answer) {
		verification.Passed = false
		verification.Errors = append(verification.Errors, "answer discusses unrelated research-pack candidates")
	}
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
	answerKeys := brainresearch.AnswerSourceKeys(answer)
	if hasEvidence && answer != "" && result.AnswerStatus != "no_evidence" && len(answerKeys) == 0 {
		verification.Passed = false
		verification.Errors = append(verification.Errors, "answer contains no source-key citations")
	}
	answerKeySet := map[string]struct{}{}
	for _, key := range answerKeys {
		answerKeySet[key] = struct{}{}
		if _, ok := allowed[key]; !ok {
			verification.Passed = false
			verification.Errors = append(verification.Errors, sourceKeyMissingError("answer citation", key, allowed))
			continue
		}
		if _, ok := citationKeys[key]; !ok {
			verification.Warnings = append(verification.Warnings, fmt.Sprintf("answer cites %s but citation metadata does not include it", key))
		}
	}
	for key := range citationKeys {
		if _, ok := answerKeySet[key]; !ok {
			verification.Passed = false
			verification.Errors = append(verification.Errors, fmt.Sprintf("citation metadata includes %s but the answer does not cite it", key))
		}
	}
	return verification
}

func answerDiscussesUnrelatedCandidates(question string, answer string) bool {
	question = strings.ToLower(strings.Join(strings.Fields(question), " "))
	if discussesEvidenceRelevance(question) {
		return false
	}
	for _, line := range strings.Split(answer, "\n") {
		line = strings.ToLower(strings.Trim(strings.TrimSpace(line), "#*_` "))
		if relevanceInventoryHeading(line) {
			return true
		}
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(answer), " "))
	return strings.Contains(normalized, "provided corpus also contains unrelated") ||
		strings.Contains(normalized, "research pack also contains unrelated") ||
		strings.Contains(normalized, "remaining results do not pertain") ||
		((strings.Contains(normalized, "corpus contains") || strings.Contains(normalized, "corpus includes") ||
			strings.Contains(normalized, "research pack contains") || strings.Contains(normalized, "research pack includes")) &&
			discussesEvidenceRelevance(normalized))
}

func discussesEvidenceRelevance(text string) bool {
	relevanceTerms := []string{"unrelated", "irrelevant", "off-topic", "off topic"}
	evidenceTerms := []string{"source", "evidence", "candidate", "result", "corpus", "research pack"}
	return containsAny(text, relevanceTerms) && containsAny(text, evidenceTerms)
}

func relevanceInventoryHeading(line string) bool {
	if !discussesEvidenceRelevance(line) || len(strings.Fields(line)) > 12 {
		return false
	}
	for _, prefix := range []string{"note on ", "unrelated ", "irrelevant ", "off-topic ", "off topic ", "other ", "remaining "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func containsAny(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func addEvidenceKeys(dst map[string]struct{}, rows []ask.Evidence) {
	for _, row := range rows {
		if key := strings.TrimSpace(row.SourceKey); key != "" {
			dst[key] = struct{}{}
		}
	}
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
