package llmprovider

import (
	"fmt"
	"strings"
	"sync"

	"github.com/darron/dbrain/internal/runtimeenv"
)

type Registry struct {
	specs map[Provider]ProviderSpec
	order []Provider
}

var (
	defaultRegistryOnce  sync.Once
	defaultRegistryValue Registry
)

func NewRegistry() Registry {
	reg := Registry{specs: map[Provider]ProviderSpec{}}
	for _, spec := range BuiltInProviderSpecs() {
		if err := reg.Register(spec); err != nil {
			panic(err)
		}
	}
	return reg
}

func RegistryForRoot(rootDir string) (Registry, error) {
	reg := NewRegistry()
	raw, ok := runtimeenv.ConfigMap(rootDir, "llm_backends")
	if !ok {
		return reg, nil
	}
	for alias, value := range raw {
		id := Provider(strings.ToLower(strings.TrimSpace(alias)))
		if id == "" {
			return Registry{}, fmt.Errorf("llm_backends contains an empty alias id")
		}
		entry, ok := value.(map[string]any)
		if !ok {
			return Registry{}, fmt.Errorf("llm_backends.%s must be a map", alias)
		}
		baseURL := strings.TrimSpace(stringValue(entry, "base_url"))
		if baseURL == "" {
			return Registry{}, fmt.Errorf("llm_backends.%s base_url is required", alias)
		}
		transport := Transport(strings.TrimSpace(stringValue(entry, "transport")))
		if transport == "" {
			transport = TransportOpenAIChat
		}
		if transport != TransportOpenAIChat {
			return Registry{}, fmt.Errorf("llm_backends.%s transport %q is not supported in this release", alias, transport)
		}
		local, _ := boolValue(entry, "local")
		spec := ProviderSpec{
			ID:              id,
			DisplayName:     firstString(stringValue(entry, "display_name"), string(id)),
			Transport:       transport,
			Local:           local,
			DefaultBaseURL:  baseURL,
			DefaultAPIKey:   "",
			ToolName:        string(id) + "-direct",
			ToolVersion:     string(id) + "-direct-v1",
			ParamPolicy:     ParamPolicy{OpenAICompatibleLocal: local},
			PromptPolicy:    PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{StatusWithDirectCall: "unknown"},
			Capabilities:    TextOnlyCapabilities(),
			ConfigPath:      []string{"llm_backends", string(id)},
		}
		if err := reg.Register(spec); err != nil {
			return Registry{}, err
		}
	}
	return reg, nil
}

func (r *Registry) Register(spec ProviderSpec) error {
	spec.ID = Provider(strings.ToLower(strings.TrimSpace(string(spec.ID))))
	if spec.ID == "" {
		return fmt.Errorf("provider id is required")
	}
	if _, exists := r.specs[spec.ID]; exists {
		return fmt.Errorf("provider %q already registered", spec.ID)
	}
	if spec.DisplayName == "" {
		spec.DisplayName = string(spec.ID)
	}
	if spec.Capabilities == (Capabilities{}) {
		spec.Capabilities = TextOnlyCapabilities()
	}
	r.specs[spec.ID] = spec
	r.order = append(r.order, spec.ID)
	return nil
}

func (r Registry) Spec(provider Provider) (ProviderSpec, bool) {
	spec, ok := r.specs[provider]
	return spec, ok
}

func (r Registry) ParseModelRef(model string) ModelRef {
	original := strings.TrimSpace(model)
	if original == "" {
		return ModelRef{}
	}
	for _, provider := range r.order {
		spec := r.specs[provider]
		if apiModel, ok := stripProvider(original, provider); ok {
			specCopy := spec
			return ModelRef{
				Original:          original,
				Provider:          provider,
				APIModel:          apiModel,
				ProviderQualified: true,
				Spec:              &specCopy,
			}
		}
		if hasProviderPrefix(original, provider) {
			return ModelRef{
				Original:          original,
				Provider:          ProviderPlain,
				APIModel:          "",
				ProviderQualified: false,
			}
		}
	}
	return ModelRef{
		Original: original,
		Provider: ProviderPlain,
		APIModel: original,
	}
}

func (r Registry) EmptyProviderRef(model string) (Provider, bool) {
	original := strings.TrimSpace(model)
	for _, provider := range r.order {
		if _, ok := stripProvider(original, provider); ok {
			return "", false
		}
		if hasProviderPrefix(original, provider) {
			return provider, true
		}
	}
	return "", false
}

func ParseModelRef(model string) ModelRef {
	return defaultRegistry().ParseModelRef(model)
}

func EmptyProviderRef(model string) (Provider, bool) {
	return defaultRegistry().EmptyProviderRef(model)
}

func defaultRegistry() Registry {
	defaultRegistryOnce.Do(func() {
		defaultRegistryValue = NewRegistry()
	})
	return defaultRegistryValue
}

func stripProvider(model string, provider Provider) (string, bool) {
	prefix := string(provider)
	lower := strings.ToLower(model)
	for _, sep := range []string{"/", ":"} {
		fullPrefix := prefix + sep
		if strings.HasPrefix(lower, fullPrefix) {
			apiModel := strings.TrimSpace(model[len(fullPrefix):])
			return apiModel, apiModel != ""
		}
	}
	return "", false
}

func hasProviderPrefix(model string, provider Provider) bool {
	prefix := string(provider)
	lower := strings.ToLower(model)
	for _, sep := range []string{"/", ":"} {
		if strings.HasPrefix(lower, prefix+sep) {
			return true
		}
	}
	return false
}

func stringValue(values map[string]any, key string) string {
	value, ok := lookupMapValue(values, key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolValue(values map[string]any, key string) (bool, bool) {
	value, ok := lookupMapValue(values, key)
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y":
			return true, true
		case "false", "0", "no", "n":
			return false, true
		}
	}
	return false, false
}

func firstString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func lookupMapValue(values map[string]any, key string) (any, bool) {
	if value, ok := values[key]; ok {
		return value, true
	}
	lower := strings.ToLower(key)
	for k, value := range values {
		if strings.ToLower(k) == lower {
			return value, true
		}
	}
	return nil, false
}
