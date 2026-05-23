package web

import (
	"strings"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchtrace"
)

type researchSynthesisDone struct {
	brainresearch.SynthesisResult
	TracePath string `json:"trace_path,omitempty"`
}

func newWebResearchRecorder(req ResearchSynthesisRequest) *researchtrace.Recorder {
	if req.TraceEnabled != nil && !*req.TraceEnabled {
		return nil
	}
	surface := strings.TrimSpace(req.TraceSurface)
	if surface == "" {
		surface = "web_research_api"
	}
	recorder := researchtrace.NewRecorder(surface, req.Question)
	recorder.SetChatContinuity(req.TraceContinuity)
	if strings.TrimSpace(req.ResearchPack.SchemaVersion) != "" {
		recorder.SetPack(req.ResearchPack)
	}
	return recorder
}

func writeWebResearchTrace(cfg config.Config, recorder *researchtrace.Recorder) (string, error) {
	if recorder == nil {
		return "", nil
	}
	trace, artifacts := recorder.Snapshot()
	result, err := researchtrace.Write(cfg, trace, artifacts, researchtrace.WriteOptions{})
	if err != nil {
		return "", err
	}
	return result.RelativePath, nil
}
