package researchrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/store"
)

const (
	defaultMaxSteps      = 12
	defaultRunnerTimeout = 90 * time.Second
	defaultStageTimeout  = 30 * time.Second
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Result, error) {
	if strings.TrimSpace(opts.Question) == "" {
		return Result{}, fmt.Errorf("question is required")
	}
	if _, err := semanticconfig.EffectiveMode(semanticconfig.ModeOff, opts.UseSemantic, opts.DisableSemantic); err != nil {
		return Result{}, err
	}
	r := newRunner(ctx, cfg, st, opts)
	defer r.cancel()
	return r.run()
}

type runner struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      config.Config
	st       *store.Store
	opts     Options
	recorder *researchtrace.Recorder
	result   Result
	steps    int
}

func newRunner(ctx context.Context, cfg config.Config, st *store.Store, opts Options) *runner {
	timeout := opts.RunnerTimeout
	if timeout <= 0 {
		timeout = defaultRunnerTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	surface := strings.TrimSpace(opts.Surface)
	if surface == "" {
		surface = "research_runner"
	}
	var recorder *researchtrace.Recorder
	if opts.TraceEnabled == nil || *opts.TraceEnabled {
		recorder = researchtrace.NewRecorder(surface, opts.Question)
		recorder.SetChatContinuity(opts.ChatContinuity)
	}
	return &runner{
		ctx:      runCtx,
		cancel:   cancel,
		cfg:      cfg,
		st:       st,
		opts:     opts,
		recorder: recorder,
	}
}

func (r *runner) run() (Result, error) {
	if !r.enter("planning", "building initial research plan") {
		return r.finish()
	}
	if !r.enter("retrieval", "retrieving initial evidence") {
		return r.finish()
	}
	pack, err := r.buildPack(false, r.opts.Question, "initial")
	if err != nil {
		if r.timedOut(err) {
			r.stop(StopTimeoutExceeded, "retrieve", err)
			return r.finish()
		}
		r.stop("retrieval_failed", "retrieve", err)
		_, _ = r.persistTrace()
		return r.result, err
	}
	r.setPack(pack)
	r.emit("retrieval", "done", "initial evidence retrieved", map[string]interface{}{
		"evidence_count": len(pack.Evidence),
		"source_keys":    sourceKeys(pack.Evidence),
	})

	if !r.enter("inspection", "inspecting top evidence") {
		return r.finish()
	}
	inspectionCtx, cancelInspection := r.stageContext()
	pack, inspection := brainresearch.InspectPack(inspectionCtx, r.cfg, r.st, pack, brainresearch.InspectionOptions{
		Limit:    r.opts.InspectionLimit,
		MaxChars: r.opts.InspectionMaxChars,
	})
	cancelInspection()
	r.setPack(pack)
	r.emit("inspection", "done", "top evidence inspected", map[string]interface{}{
		"source_keys":    inspection.InspectedSourceKeys,
		"original_order": inspection.OriginalOrder,
		"final_order":    inspection.FinalOrder,
		"reordered":      inspection.Reordered,
		"errors":         inspection.Errors,
	})

	if !r.enter("judge", "judging evidence coverage") {
		return r.finish()
	}
	judge := Judge(pack, JudgeOptions{
		MinEvidenceForEnough: r.opts.MinEvidenceForEnough,
		AllowRetry:           true,
		FocusQuestion:        r.synthesisQuestion(),
	})
	r.result.Judge = judge
	r.emit("judge", "done", judge.Reason, map[string]interface{}{
		"verdict":      judge.Verdict,
		"retry_action": judge.RetryAction,
	})

	if judge.Verdict == JudgeWeakEvidence && judge.RetryAction != RetryNone {
		if !r.enter("retry", "running one bounded retry") {
			return r.finish()
		}
		retryPack, retryQuestion, mergeDecision, retryErr := r.runRetry(pack, judge)
		if retryErr != nil {
			if r.timedOut(retryErr) {
				r.stop(StopTimeoutExceeded, "retry", retryErr)
				return r.finish()
			}
			r.emit("retry", "error", retryErr.Error(), nil)
		} else {
			pack = retryPack
			r.result.Retried = true
			retryInspectionCtx, cancelRetryInspection := r.stageContext()
			pack, retryInspection := brainresearch.InspectPack(retryInspectionCtx, r.cfg, r.st, pack, brainresearch.InspectionOptions{
				Limit:    r.opts.InspectionLimit,
				MaxChars: r.opts.InspectionMaxChars,
			})
			cancelRetryInspection()
			r.setPack(pack)
			r.emit("retry_inspection", "done", "merged retry evidence inspected", map[string]interface{}{
				"source_keys":    retryInspection.InspectedSourceKeys,
				"original_order": retryInspection.OriginalOrder,
				"final_order":    retryInspection.FinalOrder,
				"reordered":      retryInspection.Reordered,
				"errors":         retryInspection.Errors,
			})
			judge = Judge(pack, JudgeOptions{
				MinEvidenceForEnough: r.opts.MinEvidenceForEnough,
				FocusQuestion:        r.synthesisQuestion(),
			})
			r.result.Judge = judge
			r.emit("retry", "done", "bounded retry completed", map[string]interface{}{
				"evidence_count":                len(pack.Evidence),
				"source_keys":                   sourceKeys(pack.Evidence),
				"verdict":                       judge.Verdict,
				"retry_question":                retryQuestion,
				"preserved_initial_source_keys": mergeDecision.PreservedInitialSourceKeys,
				"accepted_retry_source_keys":    mergeDecision.AcceptedRetrySourceKeys,
				"rejected_retry_source_keys":    mergeDecision.RejectedRetrySourceKeys,
				"final_source_keys":             mergeDecision.FinalSourceKeys,
				"retry_merge_decision_reason":   mergeDecision.Reason,
			})
		}
	}

	stopReason := r.stopReasonFromJudge(pack, judge)
	if r.opts.StopAfterJudge {
		r.result.StopReason = stopReason
		r.emit("judge", "stopped", "stopped after judge", map[string]interface{}{
			"verdict":      judge.Verdict,
			"retry_action": judge.RetryAction,
		})
		return r.finish()
	}
	if !r.enter("prepare_synthesis", "preparing synthesis input") {
		return r.finish()
	}
	prepared, err := brainresearch.PrepareSynthesis(r.cfg, brainresearch.SynthesisOptions{
		Question:         r.synthesisQuestion(),
		Pack:             pack,
		Model:            r.opts.Model,
		CLI:              r.opts.CLI,
		MaxEvidenceChars: r.opts.MaxEvidenceChars,
	})
	if err != nil {
		if errors.Is(err, brainresearch.ErrSynthesisUnavailable) {
			r.result.StopReason = StopSynthesisUnavailable
			r.result.Warnings = append(r.result.Warnings, "model_unavailable")
			r.stop(StopSynthesisUnavailable, "prepare_synthesis", err)
			return r.finish()
		}
		r.stop(StopSynthesisFailed, "prepare_synthesis", err)
		return r.finish()
	}
	r.result.PreparedSynthesis = &prepared
	if r.recorder != nil {
		r.recorder.SetPreparedSynthesis(prepared)
	}
	r.emit("prepare_synthesis", "done", "synthesis input prepared", map[string]interface{}{
		"answer_status":   prepared.Status,
		"citation_count":  len(prepared.Citations),
		"input_chars":     len(prepared.Input),
		"evidence_budget": prepared.Truncation.EvidenceBudgetChars,
		"relevance":       prepared.Relevance,
	})

	if !r.enter("synthesis", "running synthesis") {
		return r.finish()
	}
	synthesis, err := r.runSynthesis(prepared, pack)
	if err != nil {
		if r.timedOut(err) {
			r.stop(StopTimeoutExceeded, "synthesis", err)
			return r.finish()
		}
		r.stop(StopSynthesisFailed, "synthesis", err)
		return r.finish()
	}
	r.result.Synthesis = &synthesis
	r.result.Warnings = appendUniqueStrings(r.result.Warnings, synthesis.Warnings...)
	if r.recorder != nil {
		r.recorder.SetSynthesis(synthesis)
	}
	r.emit("synthesis", "done", "synthesis completed", map[string]interface{}{
		"answer_status":  synthesis.AnswerStatus,
		"citation_count": len(synthesis.Citations),
	})

	if !r.enter("verification", "verifying citations") {
		return r.finish()
	}
	verification := mergeVerificationResults(
		GuardAnchoredAnswer(pack, prepared, synthesis),
		VerifyPreparedCitations(prepared, synthesis),
	)
	r.result.Verification = verification
	r.result.Warnings = appendUniqueStrings(r.result.Warnings, verification.Warnings...)
	if !verification.Passed {
		r.result.StopReason = StopVerificationFailed
		if r.recorder != nil {
			r.recorder.SetFailure("verification", StopVerificationFailed, errors.New(strings.Join(verification.Errors, "; ")))
		}
		r.emit("verification", "failed", "citation verification failed", map[string]interface{}{"errors": verification.Errors})
		return r.finish()
	}
	if len(verification.Warnings) > 0 {
		r.emit("verification", "warning", "answer verification warnings", map[string]interface{}{"warnings": verification.Warnings})
	}
	if r.answerReviewEnabled() {
		if !r.enter("answer_review", "reviewing answer support") {
			return r.finish()
		}
		reviewCtx, cancel := r.stageContext()
		review := RunAnswerReview(reviewCtx, r.cfg, prepared, synthesis, r.opts)
		cancel()
		r.result.AnswerReview = review
		r.result.Warnings = appendUniqueStrings(r.result.Warnings, answerReviewWarnings(review)...)
		status := "done"
		message := "answer review completed"
		if review.Verdict == AnswerReviewWarn || review.Verdict == AnswerReviewFail || review.Verdict == AnswerReviewUnavailable || review.Verdict == AnswerReviewError {
			status = "warning"
			message = "answer review raised warnings"
		}
		r.emit("answer_review", status, message, map[string]interface{}{
			"verdict":  review.Verdict,
			"model":    review.Model,
			"warnings": review.Warnings,
			"errors":   review.Errors,
		})
	}
	if synthesis.AnswerStatus == "no_evidence" {
		stopReason = StopNoEvidence
	}
	r.result.StopReason = stopReason
	r.emit("verification", "done", "citations verified", nil)
	return r.finish()
}

func (r *runner) buildPack(includeRelated bool, question string, attempt string) (brainresearch.Pack, error) {
	ctx, cancel := r.stageContext()
	defer cancel()
	return brainresearch.Build(ctx, r.cfg, r.st, brainresearch.Options{
		Question:              question,
		RawQuestion:           r.rawQuestion(),
		Topic:                 r.opts.Topic,
		Limit:                 r.opts.Limit,
		SourceTypes:           r.opts.SourceTypes,
		IncludeRelated:        includeRelated,
		RelatedLimit:          r.opts.RelatedLimit,
		SeedLimit:             r.opts.SeedLimit,
		IncludeTopic:          r.opts.IncludeTopic,
		MaxCharsPerDoc:        r.opts.MaxCharsPerDoc,
		PlannerModel:          r.opts.PlannerModel,
		PlannerTimeout:        r.opts.PlannerTimeout,
		PlannerBinary:         r.opts.PlannerBinary,
		UseModelPlanner:       r.opts.UseModelPlanner,
		DisablePlanner:        r.opts.DisablePlanner,
		UseSemantic:           r.opts.UseSemantic,
		DisableSemantic:       r.opts.DisableSemantic,
		EffectiveSemanticMode: r.opts.EffectiveSemanticMode,
		ContinuityAnchors:     r.continuityAnchors(),
		Attempt:               attempt,
		Observer:              r.recorder,
	})
}

func (r *runner) runRetry(initialPack brainresearch.Pack, judge JudgeResult) (brainresearch.Pack, string, brainresearch.MergeRetryDecision, error) {
	switch judge.RetryAction {
	case RetryFocusedVariant:
		question := focusedRetryQuestion(initialPack, judge, r.opts.Question)
		if question == "" {
			question = r.opts.Question
		}
		retryPack, err := r.buildPack(false, question, "retry-1")
		if err != nil {
			return initialPack, question, brainresearch.MergeRetryDecision{}, err
		}
		merged, decision := brainresearch.MergeRetryPack(initialPack, retryPack, brainresearch.MergeRetryOptions{
			MissingConcepts: judge.MissingConcepts,
			RetryAction:     string(judge.RetryAction),
			RetryQuestion:   question,
		})
		return merged, question, decision, nil
	case RetryRelatedExpansion:
		question := strings.TrimSpace(r.opts.Question)
		retryPack, err := r.buildPack(true, question, "retry-1")
		if err != nil {
			return initialPack, question, brainresearch.MergeRetryDecision{}, err
		}
		merged, decision := brainresearch.MergeRetryPack(initialPack, retryPack, brainresearch.MergeRetryOptions{
			MissingConcepts: judge.MissingConcepts,
			RetryAction:     string(judge.RetryAction),
			RetryQuestion:   question,
		})
		return merged, question, decision, nil
	default:
		return initialPack, "", brainresearch.MergeRetryDecision{}, nil
	}
}

func focusedRetryQuestion(pack brainresearch.Pack, judge JudgeResult, fallback string) string {
	parts := make([]string, 0, len(pack.QueryPlan.ProtectedAnchors)+len(judge.MissingConcepts))
	for _, anchor := range pack.QueryPlan.ProtectedAnchors {
		if term := protectedAnchorRetryTerm(anchor); term != "" {
			parts = append(parts, term)
		}
	}
	if len(judge.MissingConcepts) > 0 {
		parts = append(parts, judge.MissingConcepts...)
	} else if strings.TrimSpace(judge.RetryVariant) != "" {
		parts = append(parts, strings.Fields(judge.RetryVariant)...)
	}
	parts = uniqueRetryQuestionParts(parts)
	if len(parts) == 0 {
		return strings.TrimSpace(fallback)
	}
	return strings.Join(parts, " ")
}

func protectedAnchorRetryTerm(anchor brainresearch.ProtectedAnchor) string {
	for _, value := range append([]string{anchor.Raw, anchor.ResolvedID, anchor.Canonical}, anchor.ExactTerms...) {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func uniqueRetryQuestionParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}

func (r *runner) runSynthesis(prepared brainresearch.PreparedSynthesis, pack brainresearch.Pack) (brainresearch.SynthesisResult, error) {
	ctx, cancel := r.stageContext()
	defer cancel()
	return brainresearch.RunPreparedSynthesis(ctx, r.cfg, prepared, brainresearch.SynthesisOptions{
		Question:         r.synthesisQuestion(),
		Pack:             pack,
		Model:            r.opts.Model,
		CLI:              r.opts.CLI,
		Binary:           r.opts.Binary,
		Timeout:          r.opts.SynthesisTimeout,
		MaxEvidenceChars: r.opts.MaxEvidenceChars,
	})
}

func (r *runner) synthesisQuestion() string {
	if strings.TrimSpace(r.opts.SynthesisQuestion) != "" {
		return strings.TrimSpace(r.opts.SynthesisQuestion)
	}
	return r.opts.Question
}

func (r *runner) rawQuestion() string {
	if strings.TrimSpace(r.opts.RawQuestion) != "" {
		return strings.TrimSpace(r.opts.RawQuestion)
	}
	return r.synthesisQuestion()
}

func (r *runner) continuityAnchors() []brainresearch.ProtectedAnchor {
	if r.opts.ChatContinuity == nil || len(r.opts.ChatContinuity.ContinuityAnchors) == 0 {
		return nil
	}
	return append([]brainresearch.ProtectedAnchor(nil), r.opts.ChatContinuity.ContinuityAnchors...)
}

func (r *runner) answerReviewEnabled() bool {
	return r.opts.EnableAnswerReview || strings.TrimSpace(r.opts.AnswerReviewModel) != ""
}

func (r *runner) stopReasonFromJudge(pack brainresearch.Pack, judge JudgeResult) string {
	switch judge.Verdict {
	case JudgeNoEvidence:
		return StopNoEvidence
	case JudgeWeakEvidence:
		return StopWeakEvidenceBudgetExhausted
	default:
		if strings.TrimSpace(pack.QueryPlan.PlannerError) != "" {
			return StopPlannerFailedDeterministicUsed
		}
		return StopEnoughEvidence
	}
}

func (r *runner) enter(stage string, message string) bool {
	r.steps++
	maxSteps := r.opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	if r.steps > maxSteps {
		r.result.StopReason = StopMaxStepsReached
		r.emit(stage, "blocked", "maximum runner steps reached", nil)
		return false
	}
	if err := r.ctx.Err(); err != nil {
		r.result.StopReason = StopTimeoutExceeded
		r.emit(stage, "blocked", err.Error(), nil)
		return false
	}
	r.emit(stage, "start", message, nil)
	return true
}

func (r *runner) emit(stage string, status string, message string, data map[string]interface{}) {
	event := ProgressEvent{Stage: stage, Status: status, Message: message, Data: data}
	if r.result.StopReason != "" {
		event.StopReason = r.result.StopReason
	}
	if r.opts.Progress != nil {
		r.opts.Progress(event)
	}
	if r.recorder != nil {
		payload := map[string]interface{}{
			"stage":   stage,
			"status":  status,
			"message": message,
		}
		if event.StopReason != "" {
			payload["stop_reason"] = event.StopReason
		}
		for key, value := range data {
			payload[key] = value
		}
		r.recorder.Event("runner_"+stage+"_"+status, payload)
	}
}

func (r *runner) setPack(pack brainresearch.Pack) {
	r.result.Pack = pack
	if r.recorder != nil {
		r.recorder.SetPack(pack)
	}
}

func (r *runner) stop(reason string, stage string, err error) {
	r.result.StopReason = reason
	r.result.Warnings = append(r.result.Warnings, err.Error())
	if r.recorder != nil {
		r.recorder.SetFailure(stage, reason, err)
		r.recorder.SetStopReason(reason)
	}
	r.emit(stage, "failed", err.Error(), nil)
}

func (r *runner) finish() (Result, error) {
	if r.result.StopReason == "" {
		r.result.StopReason = StopEnoughEvidence
	}
	if r.recorder != nil {
		r.recorder.SetStopReason(r.result.StopReason)
	}
	tracePath, err := r.persistTrace()
	if err != nil {
		r.result.StopReason = StopTraceFailed
		return r.result, err
	}
	r.result.TracePath = tracePath
	return r.result, nil
}

func (r *runner) persistTrace() (string, error) {
	if r.recorder == nil {
		return "", nil
	}
	r.emit("trace", "start", "saving research trace", nil)
	trace, artifacts := r.recorder.Snapshot()
	result, err := researchtrace.Write(r.cfg, trace, artifacts, researchtrace.WriteOptions{
		Retention: researchtrace.RetentionOptions{KeepAll: r.opts.KeepAllTraces},
	})
	if err != nil {
		return "", err
	}
	r.emit("trace", "done", "research trace saved", map[string]interface{}{"trace_path": result.RelativePath})
	return result.RelativePath, nil
}

func (r *runner) stageContext() (context.Context, context.CancelFunc) {
	timeout := r.opts.StageTimeout
	if timeout <= 0 {
		timeout = defaultStageTimeout
	}
	return context.WithTimeout(r.ctx, timeout)
}

func (r *runner) timedOut(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(r.ctx.Err(), context.DeadlineExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

func sourceKeys(rows []ask.Evidence) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		if key := strings.TrimSpace(row.SourceKey); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func appendUniqueStrings(base []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if containsString(base, value) {
			continue
		}
		base = append(base, value)
	}
	return base
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func answerReviewWarnings(review AnswerReviewResult) []string {
	if !review.Enabled {
		return nil
	}
	warnings := append([]string(nil), review.Warnings...)
	switch review.Verdict {
	case AnswerReviewFail:
		warnings = appendUniqueStrings(warnings, "answer_review_failed")
		warnings = appendUniqueStrings(warnings, review.Errors...)
	case AnswerReviewError:
		warnings = appendUniqueStrings(warnings, "answer_review_error")
		warnings = appendUniqueStrings(warnings, review.Errors...)
	case AnswerReviewUnavailable:
		warnings = appendUniqueStrings(warnings, "answer_review_unavailable")
	case AnswerReviewWarn:
		warnings = appendUniqueStrings(warnings, "answer_review_warned")
	}
	return warnings
}
