package llmclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
)

func Chat(ctx context.Context, req Request) (Response, error) {
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
	target, err := llmprovider.ResolveTarget(ctx, resolveOpts)
	if err != nil {
		return Response{}, err
	}
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
