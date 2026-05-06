package summarizecli

import (
	"sync"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const ToolName = "summarize"
const DirectSummaryToolName = "ollama-direct"
const DirectOpenRouterToolName = "openrouter-direct"

const (
	commandRetryAttempts     = 4
	commandRetryDelay        = 100 * time.Millisecond
	commandRetryMaxDelay     = 2 * time.Second
	defaultSummaryLanguage   = "en"
	defaultOllamaBaseURL     = "http://127.0.0.1:11434/v1"
	defaultOllamaAPIKey      = "ollama"
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	directOllamaVersion      = "ollama-direct-v1"
	directOpenRouterVersion  = "openrouter-direct-v1"
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

type chatCompletionsRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ollamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Think    *bool         `json:"think,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionsResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type ollamaChatResponse struct {
	Model   string      `json:"model"`
	Message chatMessage `json:"message"`
}

type directSummaryTarget struct {
	model        string
	displayName  string
	baseURL      string
	apiKey       string
	toolName     string
	toolVersion  string
	headers      map[string]string
	label        string
	nativeOllama bool
}

var (
	versionMu    sync.Mutex
	versionCache = map[string]string{}
)
