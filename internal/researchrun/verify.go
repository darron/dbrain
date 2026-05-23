package researchrun

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

var answerSourceKeyPattern = regexp.MustCompile(`(?i)\b(?:src|x|apple-note|gh-star|yt|item):[A-Za-z0-9][A-Za-z0-9._~:/@?&=+%#-]*`)

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
			verification.Errors = append(verification.Errors, fmt.Sprintf("citation source_key %s is not present in final evidence pack", key))
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
			if exact := exactAllowedKey(allowed, key); exact != "" {
				verification.Errors = append(verification.Errors, fmt.Sprintf("answer citation %s must use exact source key %s", key, exact))
			} else {
				verification.Errors = append(verification.Errors, fmt.Sprintf("answer citation %s is not present in final evidence pack", key))
			}
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

func exactAllowedKey(allowed map[string]struct{}, candidate string) string {
	for key := range allowed {
		if strings.EqualFold(key, candidate) && key != candidate {
			return key
		}
	}
	return ""
}
