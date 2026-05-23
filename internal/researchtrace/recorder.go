package researchtrace

import (
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
)

type Recorder struct {
	mu sync.Mutex

	trace     ResearchTrace
	artifacts ArtifactContents
	now       func() time.Time
}

func NewRecorder(surface string, question string) *Recorder {
	now := time.Now
	started := now().UTC()
	return &Recorder{
		now: now,
		trace: ResearchTrace{
			SchemaVersion: SchemaVersion,
			Surface:       strings.TrimSpace(surface),
			Question:      strings.TrimSpace(question),
			StartedAt:     started,
		},
	}
}

func (r *Recorder) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
	if r.trace.StartedAt.IsZero() {
		r.trace.StartedAt = now().UTC()
	}
}

func (r *Recorder) SetChatContinuity(continuity *ChatContinuity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.ChatContinuity = continuity
}

func (r *Recorder) SetPack(pack brainresearch.Pack) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Pack = &pack
}

func (r *Recorder) SetPreparedSynthesis(prepared brainresearch.PreparedSynthesis) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.PreparedSynthesis = &prepared
	r.artifacts.SynthesisInput = prepared.Input
}

func (r *Recorder) SetSynthesis(result brainresearch.SynthesisResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Synthesis = &result
}

func (r *Recorder) SetStopReason(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.StopReason = strings.TrimSpace(reason)
}

func (r *Recorder) SetFailure(stage string, code string, err error) {
	if err == nil && strings.TrimSpace(code) == "" {
		return
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Failure = &TraceFailure{
		Stage:   strings.TrimSpace(stage),
		Code:    strings.TrimSpace(code),
		Message: strings.TrimSpace(message),
	}
}

func (r *Recorder) Event(name string, data map[string]interface{}) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Events = append(r.trace.Events, ResearchTraceEvent{
		At:   r.now().UTC(),
		Name: name,
		Data: data,
	})
}

func (r *Recorder) PlannerInput(input string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifacts.PlannerInput = input
}

func (r *Recorder) PlannerOutput(output string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifacts.PlannerOutput = output
}

func (r *Recorder) Snapshot() (ResearchTrace, ArtifactContents) {
	r.mu.Lock()
	defer r.mu.Unlock()
	trace := r.trace
	artifacts := r.artifacts
	if trace.CompletedAt.IsZero() {
		trace.CompletedAt = r.now().UTC()
	}
	if trace.StopReason == "" {
		trace.StopReason = inferStopReason(trace)
	}
	trace.Metrics = computeMetrics(trace, artifacts)
	return trace, artifacts
}

func inferStopReason(trace ResearchTrace) string {
	if trace.Failure != nil {
		if trace.Failure.Code != "" {
			return trace.Failure.Code
		}
		return "failed"
	}
	if trace.Synthesis != nil {
		switch trace.Synthesis.AnswerStatus {
		case "no_evidence":
			return "no_evidence"
		case "error":
			return "synthesis_failed"
		default:
			return "enough_evidence"
		}
	}
	if trace.Pack != nil && len(trace.Pack.Evidence) == 0 && len(trace.Pack.ExactTagEvidence) == 0 {
		return "no_evidence"
	}
	return "retrieval_only"
}

func computeMetrics(trace ResearchTrace, artifacts ArtifactContents) TraceMetrics {
	metrics := TraceMetrics{
		StageDurationsMS:          map[string]int64{},
		CharactersSentToPlanner:   len(artifacts.PlannerInput),
		CharactersSentToSynthesis: len(artifacts.SynthesisInput),
		TraceArtifactBytes:        int64(len(artifacts.PlannerInput) + len(artifacts.PlannerOutput) + len(artifacts.SynthesisInput)),
	}
	if !trace.StartedAt.IsZero() && !trace.CompletedAt.IsZero() {
		metrics.TotalDurationMS = trace.CompletedAt.Sub(trace.StartedAt).Milliseconds()
	}
	if trace.Pack != nil {
		metrics.QueryVariantCount = len(trace.Pack.QueryPlan.QueryVariants)
		metrics.EvidenceCountAfterDedupe = len(trace.Pack.Evidence)
		metrics.Planner = trace.Pack.QueryPlan.Planner
		metrics.PlannerEnabled = trace.Pack.QueryPlan.PlannerModel != ""
		if trace.Pack.QueryPlan.PlannerError != "" {
			metrics.PlannerFallbackStatus = "error_deterministic_used"
		} else if trace.Pack.QueryPlan.Planner == "model_assisted" {
			metrics.PlannerFallbackStatus = "model_assisted"
		} else {
			metrics.PlannerFallbackStatus = "deterministic"
		}
	}
	for _, event := range trace.Events {
		if event.Name == "variant_retrieved" {
			metrics.CandidateCountBeforeDedupe += intFromAny(event.Data["candidate_count"])
		}
	}
	if strings.TrimSpace(artifacts.PlannerOutput) != "" {
		metrics.ModelCallCount++
	}
	if trace.Synthesis != nil && trace.Synthesis.AnswerStatus != "no_evidence" && trace.Synthesis.Model != "" {
		metrics.ModelCallCount++
	}
	for i := 1; i < len(trace.Events); i++ {
		prev := trace.Events[i-1]
		cur := trace.Events[i]
		if prev.At.IsZero() || cur.At.IsZero() || cur.At.Before(prev.At) {
			continue
		}
		metrics.StageDurationsMS[prev.Name] += cur.At.Sub(prev.At).Milliseconds()
	}
	return metrics
}

func intFromAny(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}
