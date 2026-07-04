package summarizecli

import (
	"context"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/llmclient"
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/model"
)

func UsesDirectSummary(model string) bool {
	ok, _ := usesDirectSummaryForRoot("", model)
	return ok
}

func UsesDirectSummaryForRoot(rootDir string, model string) bool {
	ok, _ := usesDirectSummaryForRoot(rootDir, model)
	return ok
}

func usesDirectSummaryForRoot(rootDir string, model string) (bool, error) {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		if providerPrefix(model) == "" {
			return false, nil
		}
		return false, err
	}
	ref := reg.ParseModelRef(model)
	return ref.ProviderQualified && ref.Spec != nil, nil
}

func SummaryToolName(model string) string {
	return SummaryToolNameForRoot("", model)
}

func SummaryToolNameForRoot(rootDir string, model string) string {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return ToolName
	}
	ref := reg.ParseModelRef(model)
	if ref.ProviderQualified && ref.Spec != nil && strings.TrimSpace(ref.Spec.ToolName) != "" {
		return ref.Spec.ToolName
	}
	return ToolName
}

func SummaryToolVersion(ctx context.Context, binary string, model string) string {
	return SummaryToolVersionForRoot(ctx, "", binary, model)
}

func SummaryToolVersionForRoot(ctx context.Context, rootDir string, binary string, model string) string {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return Version(ctx, binary)
	}
	ref := reg.ParseModelRef(model)
	if ref.ProviderQualified && ref.Spec != nil && strings.TrimSpace(ref.Spec.ToolVersion) != "" {
		return ref.Spec.ToolVersion
	}
	return Version(ctx, binary)
}

func runDirectSummary(ctx context.Context, opts Options, inputText string) (Result, error) {
	messages := make([]llmclient.Message, 0, 2)
	if prompt := strings.TrimSpace(promptWithLengthAndLanguageHints(opts.Prompt, opts.Length, opts.Language)); prompt != "" {
		messages = append(messages, llmclient.SystemMessage(prompt))
	}
	messages = append(messages, llmclient.UserTextMessage(strings.TrimSpace(inputText)))

	response, err := llmclient.Chat(ctx, llmclient.Request{
		Model:            opts.Model,
		Messages:         messages,
		SamplerParams:    opts.InferenceParams.Sent,
		ResponseContract: llmclient.ResponseContractText,
		Timeout:          opts.Timeout,
		Task:             llmprovider.TaskSummary,
		RootDir:          opts.RootDir,
		Env:              opts.Env,
		Metrics:          opts.Metrics,
	})
	if err != nil {
		return Result{}, err
	}

	now := time.Now().UTC()
	return Result{
		Summary: model.SummaryResult{
			Text:        response.Text,
			RawJSON:     response.RawJSON,
			Model:       response.Model,
			Status:      "ok",
			FetchedAt:   now,
			Tool:        response.Tool,
			ToolVersion: response.ToolVersion,
		},
	}, nil
}

func providerPrefix(model string) string {
	value := strings.TrimSpace(model)
	if value == "" {
		return ""
	}
	slash := strings.Index(value, "/")
	colon := strings.Index(value, ":")
	index := -1
	switch {
	case slash >= 0 && colon >= 0 && slash < colon:
		index = slash
	case slash >= 0 && colon >= 0:
		index = colon
	case slash >= 0:
		index = slash
	case colon >= 0:
		index = colon
	}
	if index <= 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value[:index]))
}
