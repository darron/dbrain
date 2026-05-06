package mcpeval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func runCase(ctx context.Context, cfg config.Config, st *store.Store, tc Case) (CaseResult, error) {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = strings.TrimSpace(tc.Question)
	}
	if strings.TrimSpace(tc.Question) == "" {
		return CaseResult{}, fmt.Errorf("eval case %q has empty question", name)
	}

	started := time.Now()
	response, exactTagEvidence, err := runRetrievalCase(ctx, cfg, st, tc)
	if err != nil {
		return CaseResult{}, fmt.Errorf("run eval case %q: %w", name, err)
	}

	result := CaseResult{
		Name:          name,
		Question:      tc.Question,
		DurationMS:    time.Since(started).Milliseconds(),
		EvidenceCount: len(response.Evidence),
	}
	sourceKeys := map[string]struct{}{}
	var evidenceText strings.Builder
	var topEvidenceText string
	for _, ev := range response.Evidence {
		sourceKeys[ev.SourceKey] = struct{}{}
		result.SourceKeys = append(result.SourceKeys, ev.SourceKey)
		evText := strings.Join([]string{ev.SourceKey, ev.Title, ev.Summary, ev.Excerpt, ev.UserTags}, "\n")
		if topEvidenceText == "" {
			topEvidenceText = evText
		}
		score, signals := 0, 0
		var matchedTerms, missingTerms []string
		if ev.Retrieval != nil {
			score = ev.Retrieval.Score
			signals = len(ev.Retrieval.Signals)
			matchedTerms = append([]string(nil), ev.Retrieval.MatchedTerms...)
			missingTerms = append([]string(nil), ev.Retrieval.MissingTerms...)
		}
		result.TopEvidence = append(result.TopEvidence, EvidenceSummary{
			SourceKey:    ev.SourceKey,
			Kind:         ev.Kind,
			Title:        ev.Title,
			Score:        score,
			Signals:      signals,
			MatchedTerms: matchedTerms,
			MissingTerms: missingTerms,
		})
		evidenceText.WriteString(evText)
		evidenceText.WriteByte('\n')
	}
	sort.Strings(result.SourceKeys)

	exactTagSourceKeys := map[string]struct{}{}
	var exactTagEvidenceText strings.Builder
	for _, ev := range exactTagEvidence {
		exactTagSourceKeys[ev.SourceKey] = struct{}{}
		result.ExactTagSourceKeys = append(result.ExactTagSourceKeys, ev.SourceKey)
		evText := strings.Join([]string{ev.SourceKey, ev.Title, ev.Summary, ev.Excerpt, ev.UserTags}, "\n")
		score, signals := 0, 0
		var matchedTerms, missingTerms []string
		if ev.Retrieval != nil {
			score = ev.Retrieval.Score
			signals = len(ev.Retrieval.Signals)
			matchedTerms = append([]string(nil), ev.Retrieval.MatchedTerms...)
			missingTerms = append([]string(nil), ev.Retrieval.MissingTerms...)
		}
		result.ExactTagEvidence = append(result.ExactTagEvidence, EvidenceSummary{
			SourceKey:    ev.SourceKey,
			Kind:         ev.Kind,
			Title:        ev.Title,
			Score:        score,
			Signals:      signals,
			MatchedTerms: matchedTerms,
			MissingTerms: missingTerms,
		})
		exactTagEvidenceText.WriteString(evText)
		exactTagEvidenceText.WriteByte('\n')
	}
	result.ExactTagEvidenceCount = len(exactTagEvidence)
	sort.Strings(result.ExactTagSourceKeys)

	if tc.MinEvidence > 0 && len(response.Evidence) < tc.MinEvidence {
		result.Failures = append(result.Failures, fmt.Sprintf("evidence_count=%d below min_evidence=%d", len(response.Evidence), tc.MinEvidence))
	}
	for _, sourceKey := range tc.ExpectSourceKeys {
		if _, ok := sourceKeys[sourceKey]; !ok {
			result.Failures = append(result.Failures, "missing expected source_key "+sourceKey)
		}
	}
	if len(tc.ExpectAnySourceKeys) > 0 && !containsAnySourceKey(sourceKeys, tc.ExpectAnySourceKeys) {
		result.Failures = append(result.Failures, "missing any expected source_key from "+strings.Join(tc.ExpectAnySourceKeys, ", "))
	}
	if len(tc.ExpectTopSourceKeys) > 0 {
		if len(response.Evidence) == 0 {
			result.Failures = append(result.Failures, "missing top evidence for expected top source_key check")
		} else if !containsString(tc.ExpectTopSourceKeys, response.Evidence[0].SourceKey) {
			result.Failures = append(result.Failures, "top source_key "+response.Evidence[0].SourceKey+" not in expected set "+strings.Join(tc.ExpectTopSourceKeys, ", "))
		}
	}
	for _, sourceKey := range tc.ForbidSourceKeys {
		if _, ok := sourceKeys[sourceKey]; ok {
			result.Failures = append(result.Failures, "forbidden source_key returned "+sourceKey)
		}
	}
	if tc.MinExactTagEvidence > 0 && len(exactTagEvidence) < tc.MinExactTagEvidence {
		result.Failures = append(result.Failures, fmt.Sprintf("exact_tag_evidence_count=%d below min_exact_tag_evidence=%d", len(exactTagEvidence), tc.MinExactTagEvidence))
	}
	for _, sourceKey := range tc.ExpectExactTagEvidenceSourceKeys {
		if _, ok := exactTagSourceKeys[sourceKey]; !ok {
			result.Failures = append(result.Failures, "missing expected exact_tag_evidence source_key "+sourceKey)
		}
	}
	if len(tc.ExpectAnyExactTagEvidenceSourceKeys) > 0 && !containsAnySourceKey(exactTagSourceKeys, tc.ExpectAnyExactTagEvidenceSourceKeys) {
		result.Failures = append(result.Failures, "missing any expected exact_tag_evidence source_key from "+strings.Join(tc.ExpectAnyExactTagEvidenceSourceKeys, ", "))
	}

	text := strings.ToLower(evidenceText.String())
	for _, value := range tc.ExpectText {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !strings.Contains(text, value) {
			result.Failures = append(result.Failures, "missing expected text "+value)
		}
	}
	topText := strings.ToLower(topEvidenceText)
	for _, value := range tc.ExpectTopText {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !strings.Contains(topText, value) {
			result.Failures = append(result.Failures, "missing expected top text "+value)
		}
	}
	for _, value := range tc.ForbidText {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			result.Failures = append(result.Failures, "forbidden text present "+value)
		}
	}
	exactTagText := strings.ToLower(exactTagEvidenceText.String())
	for _, value := range tc.ExpectExactTagEvidenceText {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !strings.Contains(exactTagText, value) {
			result.Failures = append(result.Failures, "missing expected exact_tag_evidence text "+value)
		}
	}
	for _, value := range tc.ForbidExactTagEvidenceText {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(exactTagText, value) {
			result.Failures = append(result.Failures, "forbidden exact_tag_evidence text present "+value)
		}
	}
	if tc.MaxLatencyMS > 0 && result.DurationMS > tc.MaxLatencyMS {
		result.Failures = append(result.Failures, fmt.Sprintf("duration_ms=%d above max_latency_ms=%d", result.DurationMS, tc.MaxLatencyMS))
	}
	if len(response.Evidence) > 0 {
		topRetrieval := response.Evidence[0].Retrieval
		for _, term := range tc.RequireTopMatchedTerms {
			term = strings.ToLower(strings.TrimSpace(term))
			if term != "" && (topRetrieval == nil || !containsStringFold(topRetrieval.MatchedTerms, term)) {
				result.Failures = append(result.Failures, "top evidence missing required matched term "+term)
			}
		}
		for _, term := range tc.ForbidTopMissingTerms {
			term = strings.ToLower(strings.TrimSpace(term))
			if term != "" && topRetrieval != nil && containsStringFold(topRetrieval.MissingTerms, term) {
				result.Failures = append(result.Failures, "top evidence has forbidden missing term "+term)
			}
		}
	}

	result.Passed = len(result.Failures) == 0
	return result, nil
}
