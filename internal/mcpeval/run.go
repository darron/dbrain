package mcpeval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"dbrain/internal/ask"
	"dbrain/internal/config"
	"dbrain/internal/store"
)

type Case struct {
	Name                   string   `json:"name"`
	Question               string   `json:"question"`
	Limit                  int      `json:"limit,omitempty"`
	MaxCharsPerDoc         int      `json:"max_chars_per_doc,omitempty"`
	SourceTypes            []string `json:"source_types,omitempty"`
	IncludeRelated         bool     `json:"include_related,omitempty"`
	RelatedLimit           int      `json:"related_limit,omitempty"`
	MinEvidence            int      `json:"min_evidence,omitempty"`
	ExpectSourceKeys       []string `json:"expect_source_keys,omitempty"`
	ExpectTopSourceKeys    []string `json:"expect_top_source_keys,omitempty"`
	ExpectAnySourceKeys    []string `json:"expect_any_source_keys,omitempty"`
	ForbidSourceKeys       []string `json:"forbid_source_keys,omitempty"`
	ExpectText             []string `json:"expect_text,omitempty"`
	ExpectTopText          []string `json:"expect_top_text,omitempty"`
	ForbidText             []string `json:"forbid_text,omitempty"`
	RequireTopMatchedTerms []string `json:"require_top_matched_terms,omitempty"`
	ForbidTopMissingTerms  []string `json:"forbid_top_missing_terms,omitempty"`
	MaxLatencyMS           int64    `json:"max_latency_ms,omitempty"`
}

type Options struct {
	Cases []Case
}

type Report struct {
	StartedAt  string       `json:"started_at"`
	DurationMS int64        `json:"duration_ms"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
	Cases      []CaseResult `json:"cases"`
}

type CaseResult struct {
	Name          string            `json:"name"`
	Question      string            `json:"question"`
	Passed        bool              `json:"passed"`
	DurationMS    int64             `json:"duration_ms"`
	EvidenceCount int               `json:"evidence_count"`
	SourceKeys    []string          `json:"source_keys"`
	TopEvidence   []EvidenceSummary `json:"top_evidence,omitempty"`
	Failures      []string          `json:"failures,omitempty"`
}

type EvidenceSummary struct {
	SourceKey    string   `json:"source_key"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Score        int      `json:"score,omitempty"`
	Signals      int      `json:"signals,omitempty"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
	MissingTerms []string `json:"missing_terms,omitempty"`
}

func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval cases %s: %w", path, err)
	}
	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse eval cases %s: %w", path, err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("eval cases file %s contains no cases", path)
	}
	return cases, nil
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Report, error) {
	started := time.Now().UTC()
	report := Report{
		StartedAt: started.Format(time.RFC3339),
		Cases:     make([]CaseResult, 0, len(opts.Cases)),
	}

	for _, tc := range opts.Cases {
		result, err := runCase(ctx, cfg, st, tc)
		if err != nil {
			return Report{}, err
		}
		if result.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Cases = append(report.Cases, result)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report, nil
}

func runCase(ctx context.Context, cfg config.Config, st *store.Store, tc Case) (CaseResult, error) {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = strings.TrimSpace(tc.Question)
	}
	if strings.TrimSpace(tc.Question) == "" {
		return CaseResult{}, fmt.Errorf("eval case %q has empty question", name)
	}

	started := time.Now()
	response, err := ask.Run(ctx, cfg, st, tc.Question, ask.Options{
		Limit:          tc.Limit,
		RetrieveOnly:   true,
		MaxCharsPerDoc: tc.MaxCharsPerDoc,
		SourceTypes:    tc.SourceTypes,
		IncludeRelated: tc.IncludeRelated,
		RelatedLimit:   tc.RelatedLimit,
	})
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

func containsStringFold(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAnySourceKey(keys map[string]struct{}, candidates []string) bool {
	for _, candidate := range candidates {
		if _, ok := keys[candidate]; ok {
			return true
		}
	}
	return false
}

func ExampleCases() []Case {
	return []Case{{
		Name:                   "known topic retrieves expected saved evidence",
		Question:               "What does my brain know about Example Topic?",
		Limit:                  8,
		SourceTypes:            []string{"web"},
		IncludeRelated:         true,
		RelatedLimit:           2,
		MinEvidence:            3,
		ExpectTopSourceKeys:    []string{"src:replace-with-a-known-good-source"},
		ExpectAnySourceKeys:    []string{"src:replace-with-a-known-good-source"},
		ExpectText:             []string{"replace with a phrase that should appear in retrieved evidence"},
		ExpectTopText:          []string{"replace with a phrase that should appear in the strongest evidence row"},
		ForbidText:             []string{"replace with known boilerplate or noisy phrase"},
		RequireTopMatchedTerms: []string{"replace-with-important-term"},
		ForbidTopMissingTerms:  []string{"replace-with-important-term"},
		MaxLatencyMS:           3000,
	}}
}
