package itemcategorize

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/llmclient"
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/runtimeenv"
)

func effectiveSystemPrompt(opts Options) string {
	vocab := opts.Vocab.PromptSection()
	if vocab == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + vocab
}

func parseLMStudioModel(model string) (string, bool) {
	ref := llmprovider.ParseModelRef(model)
	return ref.APIModel, ref.Provider == llmprovider.ProviderLMStudio && ref.ProviderQualified
}

func callLLM(ctx context.Context, bundle string, photoData [][]byte, opts Options) (Result, error) {
	if err := unsupportedProviderModelErrorForRoot(opts.RootDir, opts.Model); err != nil {
		return Result{}, err
	}
	reg, err := llmprovider.RegistryForRoot(opts.RootDir)
	if err != nil {
		if modelHasRegisteredProviderPrefix(opts.RootDir, opts.Model) {
			return Result{}, err
		}
	} else {
		ref := reg.ParseModelRef(opts.Model)
		if ref.ProviderQualified && ref.Spec != nil {
			return callProvider(ctx, bundle, photoData, ref.Original, ref.Original, opts)
		}
	}
	// Fall back: treat the model as a plain OpenRouter model name. Use the
	// raw opts.Model for both the API model id and the result provenance.
	return callOpenRouter(ctx, bundle, photoData, opts.Model, opts.Model, opts)
}

func unsupportedProviderModelErrorForRoot(rootDir string, model string) error {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		if modelHasRegisteredProviderPrefix(rootDir, model) {
			return err
		}
		return nil
	}
	provider, ok := reg.EmptyProviderRef(model)
	if !ok {
		return nil
	}
	return fmt.Errorf("unsupported %s model %q", providerDisplayName(reg, provider), strings.TrimSpace(model))
}

func providerDisplayName(reg llmprovider.Registry, provider llmprovider.Provider) string {
	if spec, ok := reg.Spec(provider); ok && strings.TrimSpace(spec.DisplayName) != "" {
		return spec.DisplayName
	}
	switch provider {
	case llmprovider.ProviderLMStudio:
		return "LM Studio"
	case llmprovider.ProviderOpenRouter:
		return "OpenRouter"
	case llmprovider.ProviderOllama:
		return "Ollama"
	default:
		return string(provider)
	}
}

func callOllama(ctx context.Context, bundle string, photoData [][]byte, ollamaModel string, resultModel string, opts Options) (Result, error) {
	return callProvider(ctx, bundle, photoData, providerModelRef(llmprovider.ProviderOllama, ollamaModel, resultModel), resultModel, opts)
}

func callOpenRouter(ctx context.Context, bundle string, photoData [][]byte, openrouterModel string, resultModel string, opts Options) (Result, error) {
	return callProvider(ctx, bundle, photoData, providerModelRef(llmprovider.ProviderOpenRouter, openrouterModel, resultModel), resultModel, opts)
}

func callProvider(ctx context.Context, bundle string, photoData [][]byte, modelName string, resultModel string, opts Options) (Result, error) {
	if err := validateCategorizeProviderCapabilities(opts.RootDir, modelName, photoData); err != nil {
		return Result{}, err
	}

	userParts := []llmclient.ContentPart{{Type: llmclient.ContentText, Text: bundle}}
	for _, data := range photoData {
		userParts = append(userParts, llmclient.ContentPart{
			Type:      llmclient.ContentImage,
			ImageData: data,
			MIMEType:  "image/jpeg",
		})
	}
	response, err := llmclient.Chat(ctx, llmclient.Request{
		Model:             modelName,
		Messages:          []llmclient.Message{llmclient.SystemMessage(effectiveSystemPrompt(opts)), {Role: "user", Parts: userParts}},
		SamplerParams:     opts.InferenceParams.Sent,
		Timeout:           opts.Timeout,
		Task:              llmprovider.TaskCategorize,
		RootDir:           opts.RootDir,
		ProviderOverrides: providerOverrides(opts),
		Metrics:           opts.Metrics,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%s categorize: %w", providerLabel(modelName), err)
	}

	displayModel := strings.TrimSpace(resultModel)
	if displayModel == "" {
		displayModel = modelName
	}
	result, err := parseCategorizationJSON(response.Text, displayModel, opts.Vocab)
	if err != nil {
		return Result{}, err
	}
	result.Provider = string(response.Provider)
	result.APIModel = response.APIModel
	result.Transport = string(response.Transport)
	result.Tool = response.Tool
	result.ToolVersion = response.ToolVersion
	return result, nil
}

func validateCategorizeProviderCapabilities(rootDir string, modelName string, photoData [][]byte) error {
	if len(photoData) == 0 {
		return nil
	}
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return err
	}
	ref := reg.ParseModelRef(modelName)
	if ref.ProviderQualified && ref.Spec != nil && ref.Spec.Capabilities.Images == llmprovider.CapabilityUnsupported {
		return fmt.Errorf("%s categorization with images is not supported for this provider path: %s", ref.Provider, ref.Original)
	}
	return nil
}

func providerModelRef(provider llmprovider.Provider, apiModel string, resultModel string) string {
	resultModel = strings.TrimSpace(resultModel)
	if hasProviderPrefix(resultModel, provider) {
		return resultModel
	}
	return string(provider) + "/" + strings.TrimSpace(apiModel)
}

func hasProviderPrefix(model string, provider llmprovider.Provider) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	prefix := string(provider)
	return strings.HasPrefix(lower, prefix+"/") || strings.HasPrefix(lower, prefix+":")
}

func providerLabel(model string) string {
	prefix := modelProviderPrefix(model)
	if prefix == "" {
		return "openrouter"
	}
	return prefix
}

func modelHasRegisteredProviderPrefix(rootDir string, model string) bool {
	prefix := modelProviderPrefix(model)
	if prefix == "" {
		return false
	}
	switch llmprovider.Provider(prefix) {
	case llmprovider.ProviderOllama, llmprovider.ProviderOpenRouter, llmprovider.ProviderLMStudio, llmprovider.ProviderOMLX:
		return true
	}
	raw, ok := runtimeenv.ConfigMap(rootDir, "llm_backends")
	if !ok {
		return false
	}
	for alias := range raw {
		if strings.EqualFold(alias, prefix) {
			return true
		}
	}
	return false
}

func modelProviderPrefix(model string) string {
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
