package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/researchrun"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/semanticconfig"
)

var runnerContinuityPronounRE = regexp.MustCompile(`\b(it|its|that|them|there|these|this|those|they|their)\b`)

func (s *server) handleResearchRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req ResearchRunRequest
	limitedBody := http.MaxBytesReader(w, r.Body, defaultSynthesisBytes)
	if err := json.NewDecoder(limitedBody).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeMessage(w, http.StatusBadRequest, "question is required")
		return
	}
	if _, err := semanticconfig.EffectiveMode(semanticconfig.ModeOff, req.UseSemantic, req.DisableSemantic); err != nil {
		writeMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeMessage(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	limit := req.Limit
	if limit <= 0 {
		limit = defaultResearchLimit
	}
	surface := strings.TrimSpace(req.TraceSurface)
	if surface == "" {
		surface = "web_research_run"
	}
	rawQuestion := runnerRawQuestion(req)
	chatContinuity := runnerChatContinuity(req, rawQuestion)
	result, err := researchrun.Run(r.Context(), s.cfg, s.store, researchrun.Options{
		Runtime:            s.researchRuntime,
		Question:           req.Question,
		RawQuestion:        rawQuestion,
		SynthesisQuestion:  runnerSynthesisQuestion(req),
		Topic:              req.Topic,
		Limit:              clampLimit(limit, 1, maxResearchLimit),
		SourceTypes:        req.SourceTypes,
		RelatedLimit:       req.RelatedLimit,
		SeedLimit:          req.SeedLimit,
		IncludeTopic:       req.IncludeTopicBrief,
		MaxCharsPerDoc:     req.MaxCharsPerDoc,
		PlannerModel:       req.PlannerModel,
		UseModelPlanner:    req.UseModelPlanner || !req.DisablePlanner,
		DisablePlanner:     req.DisablePlanner,
		UseSemantic:        req.UseSemantic,
		DisableSemantic:    req.DisableSemantic,
		Model:              req.Model,
		CLI:                defaultWebCLI,
		MaxEvidenceChars:   req.MaxEvidenceChars,
		SynthesisTimeout:   defaultWebResearchSynthesisTimeout,
		RunnerTimeout:      defaultWebResearchRunnerTimeout,
		StageTimeout:       defaultWebResearchStageTimeout,
		EnableAnswerReview: req.AnswerReview,
		AnswerReviewModel:  req.AnswerReviewModel,
		TraceEnabled:       req.TraceEnabled,
		Surface:            surface,
		ChatContinuity:     chatContinuity,
		Progress: func(event researchrun.ProgressEvent) {
			writeSSE(w, flusher, "progress", event)
		},
	})
	if err != nil {
		writeSSE(w, flusher, "error", map[string]interface{}{
			"answer_status": "error",
			"stop_reason":   "runner_failed",
			"error":         err.Error(),
		})
		return
	}
	if result.Synthesis != nil && result.Synthesis.Answer != "" && result.StopReason != researchrun.StopVerificationFailed {
		writeSSE(w, flusher, "answer", map[string]interface{}{"text": result.Synthesis.Answer})
		for _, citation := range result.Synthesis.Citations {
			writeSSE(w, flusher, "citation", citation)
		}
	}
	if result.StopReason == researchrun.StopVerificationFailed {
		writeSSE(w, flusher, "verification_failed", map[string]interface{}{
			"answer_status":   "error",
			"answer_warnings": runnerAnswerWarnings(result),
			"stop_reason":     result.StopReason,
			"errors":          result.Verification.Errors,
			"trace_path":      result.TracePath,
			"error":           runnerVerificationError(result.Verification),
		})
	} else if runnerErrorStop(result.StopReason) {
		writeSSE(w, flusher, "error", map[string]interface{}{
			"answer_status":   "error",
			"answer_warnings": runnerAnswerWarnings(result),
			"stop_reason":     result.StopReason,
			"trace_path":      result.TracePath,
			"error":           strings.Join(result.Warnings, "; "),
		})
	}
	answerStatus := ""
	if result.Synthesis != nil {
		answerStatus = result.Synthesis.AnswerStatus
	}
	writeSSE(w, flusher, "done", map[string]interface{}{
		"schema_version":  brainResearchRunSchemaVersion(),
		"answer_status":   answerStatus,
		"answer_warnings": runnerAnswerWarnings(result),
		"research_pack":   result.Pack,
		"synthesis":       result.Synthesis,
		"citations":       runnerCitations(result),
		"trace_path":      result.TracePath,
		"stop_reason":     result.StopReason,
		"warnings":        result.Warnings,
		"judge":           result.Judge,
		"verification":    result.Verification,
		"answer_review":   result.AnswerReview,
		"completed_at":    time.Now().UTC().Format(time.RFC3339),
	})
}

func runnerErrorStop(stopReason string) bool {
	switch stopReason {
	case researchrun.StopSynthesisUnavailable, researchrun.StopSynthesisFailed, researchrun.StopTimeoutExceeded, researchrun.StopMaxStepsReached, researchrun.StopTraceFailed:
		return true
	default:
		return false
	}
}

func runnerCitations(result researchrun.Result) interface{} {
	if result.StopReason == researchrun.StopVerificationFailed {
		return nil
	}
	if result.Synthesis == nil {
		return nil
	}
	return result.Synthesis.Citations
}

func runnerAnswerWarnings(result researchrun.Result) []string {
	warnings := make([]string, 0, len(result.Warnings))
	seen := map[string]struct{}{}
	appendWarning := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		warnings = append(warnings, value)
	}
	for _, warning := range result.Warnings {
		appendWarning(warning)
	}
	if result.Synthesis != nil {
		for _, warning := range result.Synthesis.Warnings {
			appendWarning(warning)
		}
	}
	for _, warning := range result.Verification.Warnings {
		appendWarning(warning)
	}
	return warnings
}

func runnerVerificationError(verification researchrun.VerificationResult) string {
	if len(verification.Errors) == 0 {
		return "Citation verification failed"
	}
	return strings.Join(verification.Errors, "; ")
}

func brainResearchRunSchemaVersion() string {
	return "research_run.v1"
}

func runnerSynthesisQuestion(req ResearchRunRequest) string {
	return runnerRawQuestion(req)
}

func runnerRawQuestion(req ResearchRunRequest) string {
	if req.TraceContinuity != nil && traceOriginalQuestionUsable(req.TraceContinuity.OriginalQuestion) {
		return strings.TrimSpace(req.TraceContinuity.OriginalQuestion)
	}
	if current := currentQuestionFromComposedRetrieval(req.Question); current != "" {
		return current
	}
	return req.Question
}

func currentQuestionFromComposedRetrieval(question string) string {
	for _, line := range strings.Split(question, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "current question:") {
			return strings.TrimSpace(line[len("current question:"):])
		}
	}
	return ""
}

func traceOriginalQuestionUsable(question string) bool {
	question = strings.TrimSpace(question)
	if question == "" {
		return false
	}
	lower := strings.ToLower(question)
	for _, marker := range []string{
		"current question:",
		"prior evidence titles",
		"recent user questions:",
		"pinned evidence keys:",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func runnerChatContinuity(req ResearchRunRequest, rawQuestion string) *researchtrace.ChatContinuity {
	if req.TraceContinuity == nil {
		return nil
	}
	continuity := *req.TraceContinuity
	if !runnerAllowsContinuityAnchors(rawQuestion) {
		continuity.ContinuityAnchors = nil
	}
	return &continuity
}

func runnerAllowsContinuityAnchors(question string) bool {
	question = strings.TrimSpace(question)
	if question == "" || brainresearch.HasCurrentProtectedAnchor(question) {
		return false
	}
	words := strings.Fields(strings.ToLower(question))
	if len(words) == 0 || len(words) > 12 {
		return false
	}
	return runnerContinuityPronounRE.MatchString(strings.ToLower(question))
}
