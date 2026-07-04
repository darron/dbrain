package llmclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/metrics"
)

func Chat(ctx context.Context, req Request) (resp Response, err error) {
	startedAt := time.Now()
	var target llmprovider.Target
	var targetResolved bool
	defer func() {
		emitModelCallMetric(req, target, targetResolved, resp, err, time.Since(startedAt))
	}()

	if req.Timeout <= 0 {
		req.Timeout = 2 * time.Minute
	}
	if req.Task == "" {
		req.Task = llmprovider.TaskSummary
	}
	resolveOpts := req.Resolve
	resolveOpts.Model = req.Model
	resolveOpts.Task = req.Task
	if strings.TrimSpace(resolveOpts.RootDir) == "" {
		resolveOpts.RootDir = req.RootDir
	}
	if resolveOpts.Env == nil {
		resolveOpts.Env = req.Env
	}
	if resolveOpts.Overrides == nil {
		resolveOpts.Overrides = req.ProviderOverrides
	}
	target, err = llmprovider.ResolveTarget(ctx, resolveOpts)
	if err != nil {
		return Response{}, err
	}
	targetResolved = true
	if err := validateCapabilities(target, req.Messages); err != nil {
		return Response{}, err
	}
	client := req.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	accounting := accountParams(target.Spec, req.SamplerParams)
	switch target.Spec.Transport {
	case llmprovider.TransportOllamaChat:
		return chatOllama(ctx, client, req, target, accounting)
	case llmprovider.TransportOpenAIChat:
		return chatOpenAI(ctx, client, req, target, accounting)
	default:
		return Response{}, fmt.Errorf("provider %s transport %s is not implemented", target.Spec.DisplayName, target.Spec.Transport)
	}
}

func emitModelCallMetric(req Request, target llmprovider.Target, targetResolved bool, resp Response, callErr error, duration time.Duration) {
	run := req.Metrics
	if !run.Enabled() || run.Detail() != metrics.DetailModelCall {
		return
	}
	status := "ok"
	event := metrics.Event{
		"event":       "llm.call.completed",
		"status":      status,
		"task":        string(req.Task),
		"duration_ms": metrics.DurationMillis(duration),
		"model":       firstMetricString(resp.Model, req.Model),
		"request":     messageMetricCounts(req.Messages),
		"output": map[string]any{
			"chars": len(resp.Text),
		},
		"config": map[string]any{
			"timeout_ms":        metrics.DurationMillis(req.Timeout),
			"response_contract": string(req.ResponseContract),
		},
	}
	if targetResolved {
		event["provider"] = string(target.Ref.Provider)
		event["api_model"] = target.Ref.APIModel
		event["transport"] = string(target.Spec.Transport)
		event["tool"] = target.Spec.ToolName
		event["tool_version"] = target.Spec.ToolVersion
		event["local"] = target.Spec.Local
	}
	if callErr != nil {
		event["status"] = "error"
		event["error"] = metrics.ErrorObject(callErr)
	}
	_ = run.Emit(event)
}

func messageMetricCounts(messages []Message) map[string]any {
	var textParts int
	var imageParts int
	var inputChars int
	for _, message := range messages {
		for _, part := range message.Parts {
			switch part.Type {
			case ContentText:
				textParts++
				inputChars += len(part.Text)
			case ContentImage:
				imageParts++
			}
		}
	}
	return map[string]any{
		"messages":    len(messages),
		"text_parts":  textParts,
		"image_parts": imageParts,
		"input_chars": inputChars,
	}
}

func firstMetricString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateCapabilities(target llmprovider.Target, messages []Message) error {
	if target.Spec.Capabilities.Images != llmprovider.CapabilityUnsupported {
		return nil
	}
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type == ContentImage {
				return fmt.Errorf("%s model %q does not support images in this provider path", target.Spec.DisplayName, target.Ref.Original)
			}
		}
	}
	return nil
}

func responseFromTarget(target llmprovider.Target, text string, raw []byte, accounting llmprovider.ParamAccounting) Response {
	return Response{
		Text:            strings.TrimSpace(text),
		RawJSON:         strings.TrimSpace(string(raw)),
		Model:           target.Ref.Original,
		Provider:        target.Ref.Provider,
		APIModel:        target.Ref.APIModel,
		Transport:       target.Spec.Transport,
		Tool:            target.Spec.ToolName,
		ToolVersion:     target.Spec.ToolVersion,
		ParamAccounting: accounting,
	}
}
