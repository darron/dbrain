package summarizecli

import (
	"sync"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/model"
)

const ToolName = "summarize"
const DirectSummaryToolName = "ollama-direct"
const DirectOpenRouterToolName = "openrouter-direct"
const DirectLMStudioToolName = "lmstudio-direct"

const (
	commandRetryAttempts     = 4
	commandRetryDelay        = 100 * time.Millisecond
	commandRetryMaxDelay     = 2 * time.Second
	defaultSummaryLanguage   = "en"
	defaultOllamaBaseURL     = "http://127.0.0.1:11434/v1"
	defaultOllamaAPIKey      = "ollama"
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultLMStudioBaseURL   = "http://127.0.0.1:1234/v1"
	defaultLMStudioAPIKey    = "lm-studio"
	directOllamaVersion      = "ollama-direct-v1"
	directOpenRouterVersion  = "openrouter-direct-v1"
	directLMStudioVersion    = "lmstudio-direct-v1"
)

type Options struct {
	Binary    string
	Input     string
	Stdin     string
	Env       map[string]string
	Args      []string
	Summarize bool
	Model     string
	CLI       string
	Prompt    string
	Length    string
	Language  string
	Timeout   time.Duration
	RootDir   string
	Metrics   metrics.RunContext
	// InferenceParams carries optional provider-specific sampler parameters
	// used by parity-aware bakeoff runs. Normal summary callers leave this
	// empty so provider/runtime defaults apply.
	InferenceParams llmprovider.ParityParams
}

type Result struct {
	Extract model.ExtractResult
	Summary model.SummaryResult
}

type outputEnvelope struct {
	Input struct {
		Model string `json:"model"`
	} `json:"input"`
	Extracted struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Description string `json:"description"`
		SiteName    string `json:"siteName"`
		Content     string `json:"content"`
	} `json:"extracted"`
	Summary *string `json:"summary"`
}

type cliState struct {
	LastSuccessfulProvider string `json:"lastSuccessfulProvider"`
}

var (
	versionMu    sync.Mutex
	versionCache = map[string]string{}
)
