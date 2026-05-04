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
	})
	if err != nil {
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

	prepared, err := brainresearch.PrepareSynthesis(s.cfg, brainresearch.SynthesisOptions{
		Question:         req.Question,
		Pack:             req.ResearchPack,
		Model:            req.Model,
		CLI:              defaultWebCLI,
		MaxEvidenceChars: req.MaxEvidenceChars,
	})
	if err != nil {
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
		writeSSE(w, flusher, "done", brainresearch.SynthesisResult{
			SchemaVersion: prepared.SchemaVersion,
			Question:      prepared.Question,
			AnswerStatus:  "no_evidence",
			Warnings:      prepared.Warnings,
			Truncation:    prepared.Truncation,
			Citations:     prepared.Citations,
			PromptVersion: prepared.PromptVersion,
			Model:         prepared.Model,
		})
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
				writeSSE(w, flusher, "error", map[string]interface{}{
					"answer_status":   "error",
					"answer_warnings": []string{"model_error"},
					"error":           outcome.Err.Error(),
				})
				return
			}
			writeSSE(w, flusher, "answer", map[string]interface{}{
				"text": outcome.Result.Answer,
			})
			for _, citation := range outcome.Result.Citations {
				writeSSE(w, flusher, "citation", citation)
			}
			writeSSE(w, flusher, "done", outcome.Result)
			return
		}
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
