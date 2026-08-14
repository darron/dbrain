package researcheval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchrun"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/store"
)

const evalPreparedSynthesisModel = "eval/research/no-call"

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (report Report, err error) {
	started := time.Now().UTC()
	report = Report{
		StartedAt: started.Format(time.RFC3339),
		Cases:     make([]CaseResult, 0, len(opts.Cases)),
	}
	runtime := brainresearch.NewRuntime(cfg, st)
	defer func() {
		err = errors.Join(err, runtime.Close())
	}()

	for _, tc := range opts.Cases {
		result, caseErr := runCase(ctx, cfg, st, runtime, tc)
		if caseErr != nil {
			return Report{}, caseErr
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

func runCase(ctx context.Context, cfg config.Config, st *store.Store, runtime *brainresearch.Runtime, tc Case) (CaseResult, error) {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = strings.TrimSpace(tc.Question)
	}
	if strings.TrimSpace(tc.Question) == "" {
		return CaseResult{}, fmt.Errorf("research eval case %q has empty question", name)
	}
	if tc.RunWithRunner {
		return runCaseWithRunner(ctx, cfg, st, runtime, name, tc)
	}

	started := time.Now()
	pack, err := runtime.Build(ctx, brainOptions(tc))
	if err != nil {
		return CaseResult{}, fmt.Errorf("run research eval case %q: %w", name, err)
	}

	result, sourceKeys := caseResultFromPack(name, tc.Question, started, pack)
	applyEvidenceFlow(&result, brainresearch.BuildEvidenceFlow(&pack, nil, nil, false), "")

	if needsPreparedSynthesis(tc) {
		prepared, prepErr := brainresearch.PrepareSynthesis(cfg, brainresearch.SynthesisOptions{
			Question:         pack.Question,
			Pack:             pack,
			Model:            synthesisModelForCase(tc),
			MaxEvidenceChars: brainresearch.DefaultMaxEvidenceChars,
		})
		if prepErr != nil {
			result.Failures = append(result.Failures, "prepare synthesis failed: "+prepErr.Error())
		} else {
			result.AnswerStatus = prepared.Status
			flow := brainresearch.BuildEvidenceFlow(&pack, &prepared, nil, false)
			applyEvidenceFlow(&result, flow, "prompt_admitted")
			if len(tc.ExpectAnswerText) > 0 {
				result.Failures = append(result.Failures, "expect_answer_text requires a future reviewed synthesis verifier")
			}
		}
	}

	checkCaseAssertions(tc, pack, result.SourceKeys, sourceKeys, &result)
	result.Passed = len(result.Failures) == 0
	return result, nil
}

func runCaseWithRunner(ctx context.Context, cfg config.Config, st *store.Store, runtime *brainresearch.Runtime, name string, tc Case) (CaseResult, error) {
	started := time.Now()
	runResult, err := researchrun.Run(ctx, cfg, st, runnerOptions(tc, runtime))
	if err != nil {
		return CaseResult{}, fmt.Errorf("run research runner eval case %q: %w", name, err)
	}

	result, sourceKeys := caseResultFromPack(name, tc.Question, started, runResult.Pack)
	result.StopReason = runResult.StopReason
	result.JudgeVerdict = string(runResult.Judge.Verdict)
	result.RetryAction = string(runResult.Judge.RetryAction)
	if runResult.Synthesis != nil {
		result.AnswerStatus = runResult.Synthesis.AnswerStatus
		flow := brainresearch.BuildEvidenceFlow(&runResult.Pack, runResult.PreparedSynthesis, runResult.Synthesis, runResult.Retried)
		applyEvidenceFlow(&result, flow, "answer_cited")
	} else if runResult.PreparedSynthesis != nil {
		result.AnswerStatus = runResult.PreparedSynthesis.Status
		flow := brainresearch.BuildEvidenceFlow(&runResult.Pack, runResult.PreparedSynthesis, nil, runResult.Retried)
		applyEvidenceFlow(&result, flow, "prompt_admitted")
	} else {
		flow := brainresearch.BuildEvidenceFlow(&runResult.Pack, nil, nil, runResult.Retried)
		applyEvidenceFlow(&result, flow, "")
	}

	checkCaseAssertions(tc, runResult.Pack, result.SourceKeys, sourceKeys, &result)
	result.Passed = len(result.Failures) == 0
	return result, nil
}

func caseResultFromPack(name string, question string, started time.Time, pack brainresearch.Pack) (CaseResult, map[string]struct{}) {
	result := CaseResult{
		Name:             name,
		Question:         question,
		DurationMS:       time.Since(started).Milliseconds(),
		Planner:          pack.QueryPlan.Planner,
		PlannerModel:     pack.QueryPlan.PlannerModel,
		PlannerError:     pack.QueryPlan.PlannerError,
		RetrievalLanes:   retrievalLaneStatuses(pack),
		SemanticMode:     pack.QueryPlan.SemanticMode,
		ShadowComparison: pack.QueryPlan.ShadowComparison,
		QueryFamily:      pack.QueryPlan.QueryFamily,
		QueryTerms:       append([]string(nil), pack.QueryPlan.QueryTerms...),
		QueryVariants:    queryVariantStrings(pack.QueryPlan.QueryVariants),
		Concepts:         conceptStrings(pack.QueryPlan.Concepts),
	}

	sourceKeys := map[string]struct{}{}
	appendEvidence := func(ev ask.Evidence) {
		if ev.SourceKey == "" {
			return
		}
		if _, exists := sourceKeys[ev.SourceKey]; exists {
			return
		}
		sourceKeys[ev.SourceKey] = struct{}{}
		result.SourceKeys = append(result.SourceKeys, ev.SourceKey)
		summary := EvidenceSummary{
			SourceKey: ev.SourceKey,
			Kind:      ev.Kind,
			Title:     ev.Title,
		}
		if ev.Retrieval != nil {
			summary.Score = ev.Retrieval.Score
			summary.MatchedTerms = append([]string(nil), ev.Retrieval.MatchedTerms...)
			summary.MissingTerms = append([]string(nil), ev.Retrieval.MissingTerms...)
			for _, signal := range ev.Retrieval.Signals {
				result.RetrievalSignalCount++
				summary.Signals = append(summary.Signals, signal.Name)
			}
		}
		result.TopEvidence = append(result.TopEvidence, summary)
	}
	for _, ev := range pack.Evidence {
		appendEvidence(ev)
	}
	for _, ev := range pack.ExactTagEvidence {
		appendEvidence(ev)
	}
	result.EvidenceCount = len(result.SourceKeys)
	sort.Strings(result.SourceKeys)
	return result, sourceKeys
}

func brainOptions(tc Case) brainresearch.Options {
	timeout := time.Duration(tc.PlannerTimeoutMS) * time.Millisecond
	return brainresearch.Options{
		Question:              tc.Question,
		RawQuestion:           tc.RawQuestion,
		Limit:                 tc.Limit,
		SourceTypes:           tc.SourceTypes,
		IncludeRelated:        tc.IncludeRelated,
		RelatedLimit:          tc.RelatedLimit,
		SeedLimit:             tc.SeedLimit,
		IncludeTopic:          tc.IncludeTopicBrief,
		MaxCharsPerDoc:        tc.MaxCharsPerDoc,
		PlannerModel:          tc.PlannerModel,
		PlannerTimeout:        timeout,
		PlannerBinary:         tc.PlannerBinary,
		UseModelPlanner:       !tc.DisablePlanner,
		DisablePlanner:        tc.DisablePlanner,
		UseSemantic:           tc.UseSemantic,
		DisableSemantic:       tc.DisableSemantic,
		EffectiveSemanticMode: tc.EffectiveSemanticMode,
		ContinuityAnchors:     append([]brainresearch.ProtectedAnchor(nil), tc.ContinuityAnchors...),
	}
}

func runnerOptions(tc Case, runtime *brainresearch.Runtime) researchrun.Options {
	timeout := time.Duration(tc.PlannerTimeoutMS) * time.Millisecond
	traceEnabled := false
	return researchrun.Options{
		Runtime:               runtime,
		Question:              tc.Question,
		RawQuestion:           tc.RawQuestion,
		SynthesisQuestion:     firstNonEmpty(tc.RawQuestion, tc.Question),
		Limit:                 tc.Limit,
		SourceTypes:           tc.SourceTypes,
		RelatedLimit:          tc.RelatedLimit,
		SeedLimit:             tc.SeedLimit,
		IncludeTopic:          tc.IncludeTopicBrief,
		MaxCharsPerDoc:        tc.MaxCharsPerDoc,
		PlannerModel:          tc.PlannerModel,
		PlannerTimeout:        timeout,
		PlannerBinary:         tc.PlannerBinary,
		UseModelPlanner:       !tc.DisablePlanner,
		DisablePlanner:        tc.DisablePlanner,
		UseSemantic:           tc.UseSemantic,
		DisableSemantic:       tc.DisableSemantic,
		EffectiveSemanticMode: tc.EffectiveSemanticMode,
		Model:                 synthesisModelForCase(tc),
		StopAfterJudge:        tc.StopAfterJudge,
		TraceEnabled:          &traceEnabled,
		Surface:               "research_eval",
		ChatContinuity:        chatContinuityForCase(tc),
	}
}

func chatContinuityForCase(tc Case) *researchtrace.ChatContinuity {
	if len(tc.ContinuityAnchors) == 0 && strings.TrimSpace(tc.RawQuestion) == "" {
		return nil
	}
	// brainresearch.Build owns the current-turn-anchor precedence rule; evals pass
	// continuity through unchanged so topic-shift tests exercise that boundary.
	return &researchtrace.ChatContinuity{
		OriginalQuestion:  strings.TrimSpace(tc.RawQuestion),
		RetrievalQuestion: strings.TrimSpace(tc.Question),
		ContinuityAnchors: append([]brainresearch.ProtectedAnchor(nil), tc.ContinuityAnchors...),
	}
}

func needsPreparedSynthesis(tc Case) bool {
	return strings.TrimSpace(tc.ExpectAnswerStatus) != "" ||
		len(tc.ExpectCitationSourceKeys) > 0 ||
		len(tc.ForbidCitationSourceKeys) > 0 ||
		len(tc.ExpectRelevanceExcludedSourceKeys) > 0 ||
		len(tc.ForbidRelevanceExcludedSourceKeys) > 0 ||
		len(tc.ExpectPromptAdmittedSourceKeys) > 0 ||
		len(tc.ForbidPromptAdmittedSourceKeys) > 0 ||
		len(tc.ExpectBudgetDroppedSourceKeys) > 0 ||
		len(tc.ForbidBudgetDroppedSourceKeys) > 0 ||
		len(tc.ExpectAnswerCitedSourceKeys) > 0 ||
		len(tc.ForbidAnswerCitedSourceKeys) > 0 ||
		len(tc.ExpectAnswerText) > 0
}

func synthesisModelForCase(tc Case) string {
	if strings.TrimSpace(tc.SynthesisModel) != "" {
		return tc.SynthesisModel
	}
	return evalPreparedSynthesisModel
}

func checkCaseAssertions(tc Case, pack brainresearch.Pack, sortedSourceKeys []string, sourceKeys map[string]struct{}, result *CaseResult) {
	if tc.MinEvidence > 0 && len(pack.Evidence) < tc.MinEvidence {
		result.Failures = append(result.Failures, fmt.Sprintf("evidence_count=%d below min_evidence=%d", len(pack.Evidence), tc.MinEvidence))
	}
	for _, sourceKey := range tc.ExpectSourceKeys {
		if _, ok := sourceKeys[sourceKey]; !ok {
			result.Failures = append(result.Failures, "missing expected source_key "+sourceKey)
		}
	}
	if len(tc.ExpectAnySourceKeys) > 0 && !containsAny(sourceKeys, tc.ExpectAnySourceKeys) {
		result.Failures = append(result.Failures, "missing any expected source_key from "+strings.Join(tc.ExpectAnySourceKeys, ", "))
	}
	if len(tc.ExpectTopSourceKeys) > 0 {
		if len(pack.Evidence) == 0 {
			result.Failures = append(result.Failures, "missing top evidence for expected top source_key check")
		} else if !containsFold(tc.ExpectTopSourceKeys, pack.Evidence[0].SourceKey) {
			result.Failures = append(result.Failures, "top source_key "+pack.Evidence[0].SourceKey+" not in expected set "+strings.Join(tc.ExpectTopSourceKeys, ", "))
		}
	}
	for _, sourceKey := range tc.ForbidSourceKeys {
		if _, ok := sourceKeys[sourceKey]; ok {
			result.Failures = append(result.Failures, "forbidden source_key returned "+sourceKey)
		}
	}
	if expected := strings.TrimSpace(tc.ExpectPlanner); expected != "" && !strings.EqualFold(expected, pack.QueryPlan.Planner) {
		result.Failures = append(result.Failures, fmt.Sprintf("planner=%q, want %q", pack.QueryPlan.Planner, expected))
	}
	if expected := strings.TrimSpace(tc.ExpectSemanticStatus); expected != "" {
		status := retrievalLaneStatuses(pack)["semantic"]
		if !strings.EqualFold(status, expected) {
			result.Failures = append(result.Failures, fmt.Sprintf("semantic_status=%q, want %q", status, expected))
		}
	}
	if tc.ExpectSemanticMode != "" && pack.QueryPlan.SemanticMode != tc.ExpectSemanticMode {
		result.Failures = append(result.Failures, fmt.Sprintf("semantic_mode=%q, want %q", pack.QueryPlan.SemanticMode, tc.ExpectSemanticMode))
	}
	if expected := strings.TrimSpace(tc.ExpectQueryFamily); expected != "" && !strings.EqualFold(expected, pack.QueryPlan.QueryFamily) {
		result.Failures = append(result.Failures, fmt.Sprintf("query_family=%q, want %q", pack.QueryPlan.QueryFamily, expected))
	}
	if strings.TrimSpace(tc.PlannerModel) != "" && !strings.EqualFold(strings.TrimSpace(tc.PlannerModel), pack.QueryPlan.PlannerModel) {
		result.Failures = append(result.Failures, fmt.Sprintf("planner_model=%q, want %q", pack.QueryPlan.PlannerModel, strings.TrimSpace(tc.PlannerModel)))
	}
	for _, term := range tc.ExpectQueryTerms {
		if !containsFold(pack.QueryPlan.QueryTerms, term) {
			result.Failures = append(result.Failures, "missing expected query term "+strings.TrimSpace(term))
		}
	}
	variants := queryVariantStrings(pack.QueryPlan.QueryVariants)
	for _, variant := range tc.ExpectQueryVariants {
		if !containsFold(variants, variant) {
			result.Failures = append(result.Failures, "missing expected query variant "+strings.TrimSpace(variant))
		}
	}
	for _, variant := range tc.ForbidQueryVariants {
		if containsFold(variants, variant) {
			result.Failures = append(result.Failures, "forbidden query variant present "+strings.TrimSpace(variant))
		}
	}
	concepts := conceptStrings(pack.QueryPlan.Concepts)
	for _, concept := range tc.ExpectConcepts {
		if !containsFold(concepts, concept) {
			result.Failures = append(result.Failures, "missing expected concept "+strings.TrimSpace(concept))
		}
	}
	for _, concept := range tc.ForbidConcepts {
		if containsFold(concepts, concept) {
			result.Failures = append(result.Failures, "forbidden concept present "+strings.TrimSpace(concept))
		}
	}
	requiredConcepts := requiredConceptStrings(pack.QueryPlan.Concepts)
	for _, concept := range tc.ExpectRequiredConcepts {
		if !containsFold(requiredConcepts, concept) {
			result.Failures = append(result.Failures, "missing expected required concept "+strings.TrimSpace(concept))
		}
	}
	for _, concept := range tc.ForbidRequiredConcepts {
		if containsFold(requiredConcepts, concept) {
			result.Failures = append(result.Failures, "forbidden required concept present "+strings.TrimSpace(concept))
		}
	}
	if expected := strings.TrimSpace(tc.ExpectPlannerErrorContains); expected != "" && !strings.Contains(strings.ToLower(pack.QueryPlan.PlannerError), strings.ToLower(expected)) {
		result.Failures = append(result.Failures, fmt.Sprintf("planner_error=%q does not contain %q", pack.QueryPlan.PlannerError, expected))
	}
	if tc.MinRetrievalSignals > 0 && result.RetrievalSignalCount < tc.MinRetrievalSignals {
		result.Failures = append(result.Failures, fmt.Sprintf("retrieval_signal_count=%d below min_retrieval_signals=%d", result.RetrievalSignalCount, tc.MinRetrievalSignals))
	}
	if expected := strings.TrimSpace(tc.ExpectJudgeVerdict); expected != "" && !strings.EqualFold(result.JudgeVerdict, expected) {
		result.Failures = append(result.Failures, fmt.Sprintf("judge_verdict=%q, want %q", result.JudgeVerdict, expected))
	}
	if forbidden := strings.TrimSpace(tc.ForbidRetryAction); forbidden != "" && strings.EqualFold(result.RetryAction, forbidden) {
		result.Failures = append(result.Failures, fmt.Sprintf("forbidden retry_action returned %q", result.RetryAction))
	}
	if expected := strings.TrimSpace(tc.ExpectAnswerStatus); expected != "" && !strings.EqualFold(result.AnswerStatus, expected) {
		result.Failures = append(result.Failures, fmt.Sprintf("answer_status=%q, want %q", result.AnswerStatus, expected))
	}
	citationKeys := map[string]struct{}{}
	for _, sourceKey := range result.CitationSourceKeys {
		citationKeys[sourceKey] = struct{}{}
	}
	for _, sourceKey := range tc.ExpectCitationSourceKeys {
		if _, ok := citationKeys[sourceKey]; !ok {
			result.Failures = append(result.Failures, "missing expected citation source_key "+sourceKey)
		}
	}
	for _, sourceKey := range tc.ForbidCitationSourceKeys {
		if _, ok := citationKeys[sourceKey]; ok {
			result.Failures = append(result.Failures, "forbidden citation source_key returned "+sourceKey)
		}
	}
	checkExpectedSourceKeys("relevance-excluded", tc.ExpectRelevanceExcludedSourceKeys, result.EvidenceFlow.RelevanceExcludedSourceKeys, result)
	checkForbiddenSourceKeys("relevance-excluded", tc.ForbidRelevanceExcludedSourceKeys, result.EvidenceFlow.RelevanceExcludedSourceKeys, result)
	checkExpectedSourceKeys("prompt-admitted", tc.ExpectPromptAdmittedSourceKeys, result.EvidenceFlow.PromptAdmittedSourceKeys, result)
	checkForbiddenSourceKeys("prompt-admitted", tc.ForbidPromptAdmittedSourceKeys, result.EvidenceFlow.PromptAdmittedSourceKeys, result)
	checkExpectedSourceKeys("budget-dropped", tc.ExpectBudgetDroppedSourceKeys, result.EvidenceFlow.BudgetDroppedSourceKeys, result)
	checkForbiddenSourceKeys("budget-dropped", tc.ForbidBudgetDroppedSourceKeys, result.EvidenceFlow.BudgetDroppedSourceKeys, result)
	checkExpectedSourceKeys("answer-cited", tc.ExpectAnswerCitedSourceKeys, result.EvidenceFlow.AnswerCitedSourceKeys, result)
	checkForbiddenSourceKeys("answer-cited", tc.ForbidAnswerCitedSourceKeys, result.EvidenceFlow.AnswerCitedSourceKeys, result)
	if len(tc.ExpectAnswerCitedSourceKeys) > 0 && result.EvidenceFlow.SynthesisStatus != "ran" {
		result.Failures = append(result.Failures, "expect_answer_cited_source_keys requires synthesis to run")
	}
	for _, invariantError := range result.EvidenceFlow.InvariantErrors {
		result.Failures = append(result.Failures, "evidence_flow invariant failed: "+invariantError)
	}
	if tc.MaxLatencyMS > 0 && result.DurationMS > tc.MaxLatencyMS {
		result.Failures = append(result.Failures, fmt.Sprintf("duration_ms=%d above max_latency_ms=%d", result.DurationMS, tc.MaxLatencyMS))
	}
	_ = sortedSourceKeys
}

func applyEvidenceFlow(result *CaseResult, flow brainresearch.EvidenceFlow, legacyMode string) {
	result.EvidenceFlow = flow
	result.CitationSourceKeysMode = legacyMode
	switch legacyMode {
	case "answer_cited":
		result.CitationSourceKeys = append([]string(nil), flow.AnswerCitedSourceKeys...)
	case "prompt_admitted":
		result.CitationSourceKeys = append([]string(nil), flow.PromptAdmittedSourceKeys...)
	}
}

func checkExpectedSourceKeys(stage string, expected []string, actual []string, result *CaseResult) {
	set := stringSet(actual)
	for _, key := range expected {
		if _, ok := set[key]; !ok {
			result.Failures = append(result.Failures, "missing expected "+stage+" source_key "+key)
		}
	}
}

func checkForbiddenSourceKeys(stage string, forbidden []string, actual []string, result *CaseResult) {
	set := stringSet(actual)
	for _, key := range forbidden {
		if _, ok := set[key]; ok {
			result.Failures = append(result.Failures, "forbidden "+stage+" source_key returned "+key)
		}
	}
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func conceptStrings(concepts []brainresearch.QueryConcept) []string {
	out := make([]string, 0, len(concepts)*3)
	for _, concept := range concepts {
		if concept.Key != "" {
			out = append(out, concept.Key)
		}
		if concept.Preferred != "" {
			out = append(out, concept.Preferred)
		}
		out = append(out, concept.Terms...)
	}
	return uniqueNonEmpty(out)
}

func requiredConceptStrings(concepts []brainresearch.QueryConcept) []string {
	required := make([]brainresearch.QueryConcept, 0, len(concepts))
	for _, concept := range concepts {
		if concept.Required {
			required = append(required, concept)
		}
	}
	return conceptStrings(required)
}

func retrievalLaneStatuses(pack brainresearch.Pack) map[string]string {
	if len(pack.QueryPlan.RetrievalLanes) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, lane := range pack.QueryPlan.RetrievalLanes {
		name := strings.TrimSpace(lane.Name)
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(lane.Status)
	}
	return out
}

func citationSourceKeys(citations []brainresearch.Citation) []string {
	out := make([]string, 0, len(citations))
	seen := map[string]struct{}{}
	for _, citation := range citations {
		key := strings.TrimSpace(citation.SourceKey)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func containsAny(values map[string]struct{}, candidates []string) bool {
	for _, candidate := range candidates {
		if _, ok := values[strings.TrimSpace(candidate)]; ok {
			return true
		}
	}
	return false
}

func containsFold(values []string, candidate string) bool {
	needle := strings.ToLower(strings.TrimSpace(candidate))
	if needle == "" {
		return true
	}
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == needle {
			return true
		}
	}
	return false
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
