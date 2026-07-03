package llmprovider

// Provider identifies the LLM backend a model string targets.
type Provider string

const (
	ProviderPlain      Provider = ""
	ProviderOllama     Provider = "ollama"
	ProviderOpenRouter Provider = "openrouter"
	ProviderLMStudio   Provider = "lmstudio"
	ProviderOMLX       Provider = "omlx"
)

type Transport string

const (
	TransportOllamaChat        Transport = "ollama_chat"
	TransportOpenAIChat        Transport = "openai_chat_completions"
	TransportAnthropicMessages Transport = "anthropic_messages"
)

type CapabilityStatus string

const (
	CapabilitySupported                  CapabilityStatus = "supported"
	CapabilityUnsupported                CapabilityStatus = "unsupported"
	CapabilityModelDependentOrUnverified CapabilityStatus = "model_dependent_or_unverified"
)

type Task string

const (
	TaskSummary    Task = "summary"
	TaskCategorize Task = "categorize"
	TaskOCR        Task = "ocr"
	TaskBakeoff    Task = "bakeoff"
)

const (
	ToolOllamaDirect     = "ollama-direct"
	ToolOpenRouterDirect = "openrouter-direct"
	ToolLMStudioDirect   = "lmstudio-direct"
	ToolOMLXDirect       = "omlx-direct"

	ToolVersionOllamaDirect     = "ollama-direct-v1"
	ToolVersionOpenRouterDirect = "openrouter-direct-v1"
	ToolVersionLMStudioDirect   = "lmstudio-direct-v1"
	ToolVersionOMLXDirect       = "omlx-direct-v1"
)

type Capabilities struct {
	Text         bool
	Images       CapabilityStatus
	JSONMode     CapabilityStatus
	ToolCalling  CapabilityStatus
	ReasoningCtl CapabilityStatus
}

type HeaderPolicy struct {
	RefererEnvKeys       []string
	TitleEnvKeys         []string
	UserAgentEnvKeys     []string
	DefaultRefererByTask map[Task]string
	DefaultTitleByTask   map[Task]string
}

type ParamPolicy struct {
	LocalModelfileParity  bool
	OpenAICompatibleLocal bool
}

type PromptPolicy struct {
	ParityStatusWithPreset string
}

type ReasoningPolicy struct {
	StatusWithDirectCall string
}

type ProviderSpec struct {
	ID              Provider
	DisplayName     string
	Transport       Transport
	Local           bool
	DefaultBaseURL  string
	DefaultAPIKey   string
	BaseURLEnvKeys  []string
	APIKeyEnvKeys   []string
	RequiresAPIKey  bool
	HeaderPolicy    HeaderPolicy
	ToolName        string
	ToolVersion     string
	ParamPolicy     ParamPolicy
	PromptPolicy    PromptPolicy
	ReasoningPolicy ReasoningPolicy
	Capabilities    Capabilities
	ConfigPath      []string
}

// ModelRef is the parsed form of a provider-qualified model string.
type ModelRef struct {
	Original          string
	Provider          Provider
	APIModel          string
	ProviderQualified bool
	Spec              *ProviderSpec
}

func BuiltInProviderSpecs() []ProviderSpec {
	return []ProviderSpec{
		{
			ID:             ProviderOllama,
			DisplayName:    "Ollama",
			Transport:      TransportOllamaChat,
			Local:          true,
			DefaultBaseURL: "http://127.0.0.1:11434",
			DefaultAPIKey:  "ollama",
			BaseURLEnvKeys: []string{"DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"},
			APIKeyEnvKeys:  []string{"DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"},
			ToolName:       ToolOllamaDirect,
			ToolVersion:    ToolVersionOllamaDirect,
			ParamPolicy:    ParamPolicy{LocalModelfileParity: true},
			PromptPolicy:   PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{
				StatusWithDirectCall: "think-disabled",
			},
			Capabilities: Capabilities{
				Text:         true,
				Images:       CapabilityModelDependentOrUnverified,
				JSONMode:     CapabilityModelDependentOrUnverified,
				ToolCalling:  CapabilityModelDependentOrUnverified,
				ReasoningCtl: CapabilitySupported,
			},
			ConfigPath: []string{"ollama"},
		},
		{
			ID:             ProviderOpenRouter,
			DisplayName:    "OpenRouter",
			Transport:      TransportOpenAIChat,
			Local:          false,
			DefaultBaseURL: "https://openrouter.ai/api/v1",
			BaseURLEnvKeys: []string{"DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"},
			APIKeyEnvKeys:  []string{"DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY"},
			RequiresAPIKey: true,
			HeaderPolicy: HeaderPolicy{
				RefererEnvKeys:   []string{"DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"},
				TitleEnvKeys:     []string{"DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"},
				UserAgentEnvKeys: []string{"DBRAIN_USER_AGENT"},
				DefaultRefererByTask: map[Task]string{
					TaskCategorize: "https://local.dbrain",
					TaskOCR:        "https://local.dbrain",
				},
				DefaultTitleByTask: map[Task]string{
					TaskCategorize: "dbrain categorize",
					TaskOCR:        "dbrain X photo OCR",
				},
			},
			ToolName:    ToolOpenRouterDirect,
			ToolVersion: ToolVersionOpenRouterDirect,
			Capabilities: Capabilities{
				Text:         true,
				Images:       CapabilityModelDependentOrUnverified,
				JSONMode:     CapabilityModelDependentOrUnverified,
				ToolCalling:  CapabilityModelDependentOrUnverified,
				ReasoningCtl: CapabilityModelDependentOrUnverified,
			},
			ConfigPath: []string{"openrouter"},
		},
		{
			ID:             ProviderLMStudio,
			DisplayName:    "LM Studio",
			Transport:      TransportOpenAIChat,
			Local:          true,
			DefaultBaseURL: "http://127.0.0.1:1234/v1",
			DefaultAPIKey:  "lm-studio",
			BaseURLEnvKeys: []string{"DBRAIN_LMSTUDIO_BASE_URL"},
			APIKeyEnvKeys:  []string{"DBRAIN_LMSTUDIO_API_KEY"},
			ToolName:       ToolLMStudioDirect,
			ToolVersion:    ToolVersionLMStudioDirect,
			ParamPolicy:    ParamPolicy{OpenAICompatibleLocal: true},
			PromptPolicy:   PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{
				StatusWithDirectCall: "unknown",
			},
			Capabilities: TextOnlyCapabilities(),
			ConfigPath:   []string{"lmstudio"},
		},
		{
			ID:             ProviderOMLX,
			DisplayName:    "oMLX",
			Transport:      TransportOpenAIChat,
			Local:          true,
			DefaultBaseURL: "http://127.0.0.1:8000/v1",
			DefaultAPIKey:  "",
			BaseURLEnvKeys: []string{"DBRAIN_OMLX_BASE_URL"},
			APIKeyEnvKeys:  []string{"DBRAIN_OMLX_API_KEY"},
			ToolName:       ToolOMLXDirect,
			ToolVersion:    ToolVersionOMLXDirect,
			ParamPolicy:    ParamPolicy{OpenAICompatibleLocal: true},
			PromptPolicy:   PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{
				StatusWithDirectCall: "unknown",
			},
			Capabilities: Capabilities{
				Text:         true,
				Images:       CapabilityModelDependentOrUnverified,
				JSONMode:     CapabilityModelDependentOrUnverified,
				ToolCalling:  CapabilityModelDependentOrUnverified,
				ReasoningCtl: CapabilityModelDependentOrUnverified,
			},
			ConfigPath: []string{"omlx"},
		},
	}
}

func TextOnlyCapabilities() Capabilities {
	return Capabilities{
		Text:         true,
		Images:       CapabilityUnsupported,
		JSONMode:     CapabilityModelDependentOrUnverified,
		ToolCalling:  CapabilityModelDependentOrUnverified,
		ReasoningCtl: CapabilityModelDependentOrUnverified,
	}
}
