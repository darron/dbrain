package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/researcheval"
	"github.com/darron/dbrain/internal/researchrun"
	"github.com/darron/dbrain/internal/researchtrace"
)

func (s *server) handleResearchTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	limit := parseIntParam(r, "limit", 25)
	if limit > 100 {
		limit = 100
	}
	traces, err := researchtrace.List(s.cfg, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"traces": traces})
}

func (s *server) handleResearchTraceCompare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var req ResearchTraceCompareRequest
	limitedBody := http.MaxBytesReader(w, r.Body, defaultSynthesisBytes)
	if err := json.NewDecoder(limitedBody).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	req.TracePath = strings.TrimSpace(req.TracePath)
	if req.TracePath == "" {
		writeMessage(w, http.StatusBadRequest, "trace_path is required")
		return
	}
	trace, resolved, err := researchtrace.Load(s.cfg, req.TracePath)
	if err != nil {
		writeMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	diff, err := researcheval.DiffTrace(r.Context(), s.cfg, s.store, req.TracePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	response := map[string]interface{}{
		"trace":         trace,
		"resolved_path": resolved,
		"diff":          diff,
		"old_answer":    traceAnswer(trace),
		"old_status":    traceAnswerStatus(trace),
	}
	if req.RunCurrent {
		current, err := runTraceCurrentHarness(r, s, trace)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		response["current"] = current
	}
	writeJSON(w, http.StatusOK, response)
}

func runTraceCurrentHarness(r *http.Request, s *server, trace researchtrace.ResearchTrace) (map[string]interface{}, error) {
	opts := researcheval.OptionsFromTrace(trace)
	traceDisabled := false
	result, err := researchrun.Run(r.Context(), s.cfg, s.store, researchrun.Options{
		Question:         opts.Question,
		Topic:            opts.Topic,
		Limit:            opts.Limit,
		SourceTypes:      opts.SourceTypes,
		RelatedLimit:     opts.RelatedLimit,
		SeedLimit:        opts.SeedLimit,
		IncludeTopic:     opts.IncludeTopic,
		MaxCharsPerDoc:   opts.MaxCharsPerDoc,
		PlannerModel:     opts.PlannerModel,
		PlannerTimeout:   opts.PlannerTimeout,
		PlannerBinary:    opts.PlannerBinary,
		UseModelPlanner:  opts.UseModelPlanner,
		DisablePlanner:   opts.DisablePlanner,
		UseSemantic:      opts.UseSemantic,
		DisableSemantic:  opts.DisableSemantic,
		TraceEnabled:     &traceDisabled,
		Surface:          "web_harness_lab",
		RunnerTimeout:    90 * time.Second,
		SynthesisTimeout: 2 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	answer := ""
	answerStatus := ""
	var citations interface{}
	if result.Synthesis != nil {
		answer = result.Synthesis.Answer
		answerStatus = result.Synthesis.AnswerStatus
		citations = result.Synthesis.Citations
	}
	return map[string]interface{}{
		"answer":        answer,
		"answer_status": answerStatus,
		"citations":     citations,
		"stop_reason":   result.StopReason,
		"research_pack": result.Pack,
		"warnings":      result.Warnings,
		"verification":  result.Verification,
	}, nil
}

func traceAnswer(trace researchtrace.ResearchTrace) string {
	if trace.Synthesis == nil {
		return ""
	}
	return trace.Synthesis.Answer
}

func traceAnswerStatus(trace researchtrace.ResearchTrace) string {
	if trace.Synthesis == nil {
		return ""
	}
	return trace.Synthesis.AnswerStatus
}

func parseIntParam(r *http.Request, name string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
