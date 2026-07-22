package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/semanticconfig"
)

func (s *server) handleResearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req ResearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeMessage(w, http.StatusBadRequest, "question is required")
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultResearchLimit
	}
	researchCtx, cancel := context.WithTimeout(r.Context(), defaultResearchTimeout)
	defer cancel()
	pack, err := brainresearch.Build(researchCtx, s.cfg, s.store, brainresearch.Options{
		Question:        req.Question,
		Topic:           req.Topic,
		Limit:           clampLimit(limit, 1, maxResearchLimit),
		SourceTypes:     req.SourceTypes,
		IncludeRelated:  req.IncludeRelated,
		RelatedLimit:    req.RelatedLimit,
		SeedLimit:       req.SeedLimit,
		IncludeTopic:    req.IncludeTopicBrief,
		MaxCharsPerDoc:  req.MaxCharsPerDoc,
		PlannerModel:    req.PlannerModel,
		UseModelPlanner: req.UseModelPlanner || !req.DisablePlanner,
		DisablePlanner:  req.DisablePlanner,
		UseSemantic:     req.UseSemantic,
		DisableSemantic: req.DisableSemantic,
	})
	if err != nil {
		if errors.Is(err, semanticconfig.ErrConflictingOverrides) {
			writeMessage(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(researchCtx.Err(), context.DeadlineExceeded) {
			writeMessage(w, http.StatusGatewayTimeout, "research request timed out; try a narrower query or disable model planning")
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, pack)
}

func (s *server) handleResearchSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req ResearchSynthesisRequest
	limitedBody := http.MaxBytesReader(w, r.Body, defaultSynthesisBytes)
	if err := json.NewDecoder(limitedBody).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	recorder := newWebResearchRecorder(req)
	prepared, err := brainresearch.PrepareSynthesis(s.cfg, brainresearch.SynthesisOptions{
		Question:         req.Question,
		Pack:             req.ResearchPack,
		Model:            req.Model,
		CLI:              defaultWebCLI,
		MaxEvidenceChars: req.MaxEvidenceChars,
	})
	if err != nil {
		if recorder != nil {
			stopReason := "synthesis_failed"
			if errors.Is(err, brainresearch.ErrSynthesisUnavailable) {
				stopReason = "synthesis_unavailable"
			}
			recorder.SetFailure("prepare_synthesis", stopReason, err)
			recorder.SetStopReason(stopReason)
			_, _ = writeWebResearchTrace(s.cfg, recorder)
		}
		if errors.Is(err, brainresearch.ErrSynthesisUnavailable) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"error":           err.Error(),
				"answer_status":   "unavailable",
				"answer_warnings": []string{"model_unavailable"},
			})
			return
		}
		writeMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	if recorder != nil {
		recorder.SetPreparedSynthesis(prepared)
		recorder.Event("synthesis_prepared", map[string]interface{}{
			"model":                 prepared.Model,
			"answer_status":         prepared.Status,
			"evidence_budget_chars": prepared.Truncation.EvidenceBudgetChars,
			"evidence_chars_used":   prepared.Truncation.EvidenceCharsUsed,
			"citation_count":        len(prepared.Citations),
		})
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

	writeSSE(w, flusher, "start", map[string]interface{}{
		"schema_version":        prepared.SchemaVersion,
		"model":                 prepared.Model,
		"prompt_version":        prepared.PromptVersion,
		"evidence_budget_chars": prepared.Truncation.EvidenceBudgetChars,
		"truncation":            prepared.Truncation,
		"answer_warnings":       prepared.Warnings,
		"answer_status":         prepared.Status,
	})

	if prepared.Status == "no_evidence" {
		result := brainresearch.SynthesisResult{
			SchemaVersion: prepared.SchemaVersion,
			Question:      prepared.Question,
			AnswerStatus:  "no_evidence",
			Warnings:      prepared.Warnings,
			Truncation:    prepared.Truncation,
			Citations:     prepared.Citations,
			PromptVersion: prepared.PromptVersion,
			Model:         prepared.Model,
		}
		tracePath := ""
		if recorder != nil {
			recorder.SetSynthesis(result)
			recorder.SetStopReason("no_evidence")
			tracePath, err = writeWebResearchTrace(s.cfg, recorder)
			if err != nil {
				writeSSE(w, flusher, "error", map[string]interface{}{
					"answer_status":   "error",
					"answer_warnings": []string{"trace_error"},
					"error":           err.Error(),
				})
				return
			}
		}
		writeSSE(w, flusher, "done", researchSynthesisDone{SynthesisResult: result, TracePath: tracePath})
		return
	}

	type synthesisOutcome struct {
		Result brainresearch.SynthesisResult
		Err    error
	}
	resultCh := make(chan synthesisOutcome, 1)
	go func() {
		result, err := brainresearch.RunPreparedSynthesis(r.Context(), s.cfg, prepared, brainresearch.SynthesisOptions{
			CLI:              defaultWebCLI,
			Model:            req.Model,
			MaxEvidenceChars: req.MaxEvidenceChars,
		})
		resultCh <- synthesisOutcome{Result: result, Err: err}
	}()

	ticker := time.NewTicker(defaultSSEHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]interface{}{
				"ts": time.Now().UTC().Format(time.RFC3339),
			})
		case outcome := <-resultCh:
			if outcome.Err != nil {
				tracePath := ""
				if recorder != nil {
					recorder.SetFailure("synthesis", "synthesis_failed", outcome.Err)
					recorder.SetStopReason("synthesis_failed")
					tracePath, err = writeWebResearchTrace(s.cfg, recorder)
					if err != nil {
						outcome.Err = fmt.Errorf("%w; trace write failed: %v", outcome.Err, err)
					}
				}
				writeSSE(w, flusher, "error", map[string]interface{}{
					"answer_status":   "error",
					"answer_warnings": []string{"model_error"},
					"error":           outcome.Err.Error(),
					"trace_path":      tracePath,
				})
				return
			}
			tracePath := ""
			if recorder != nil {
				recorder.SetSynthesis(outcome.Result)
				recorder.SetStopReason(researchSynthesisStopReason(outcome.Result))
				tracePath, err = writeWebResearchTrace(s.cfg, recorder)
				if err != nil {
					writeSSE(w, flusher, "error", map[string]interface{}{
						"answer_status":   "error",
						"answer_warnings": []string{"trace_error"},
						"error":           err.Error(),
					})
					return
				}
			}
			writeSSE(w, flusher, "answer", map[string]interface{}{
				"text": outcome.Result.Answer,
			})
			for _, citation := range outcome.Result.Citations {
				writeSSE(w, flusher, "citation", citation)
			}
			writeSSE(w, flusher, "done", researchSynthesisDone{SynthesisResult: outcome.Result, TracePath: tracePath})
			return
		}
	}
}

func researchSynthesisStopReason(result brainresearch.SynthesisResult) string {
	switch result.AnswerStatus {
	case "no_evidence":
		return "no_evidence"
	case "error":
		return "synthesis_failed"
	default:
		return "enough_evidence"
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"error":"encode event"}`)
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}
