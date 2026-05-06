package itemcategorize

import (
	"time"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/model"
)

const (
	defaultModel          = "openrouter/google/gemini-2.5-flash"
	defaultOllamaBase     = "http://127.0.0.1:11434"
	defaultOllamaKey      = "ollama"
	defaultOpenRouterBase = "https://openrouter.ai/api/v1"
	defaultTimeout        = 90 * time.Second
	maxArticleChars       = 3000
	maxTranscriptChars    = 3000
)

const systemPrompt = `You are categorizing items or linked sources for a personal knowledge base.
Analyze the provided content and respond with ONLY valid JSON — no markdown, no code fences, no explanation.

Required format:
{
  "categories": ["broad-theme"],
  "tags": ["specific-tag"],
  "primary_category": "most-relevant-category"
}

Rules:
- ALL values must be lowercase with hyphens replacing spaces — no exceptions
- categories: 2-5 broad topical themes, 1-3 words each
  Good: "canadian-politics", "economic-policy", "software-development"
  Bad:  "Canadian Politics", "Economic Policy", "Software Development"
- tags: 5-10 specific concepts, people, places, or events
  Good: "matt-gurney", "sovereign-wealth-fund", "canadian-economy"
  Bad:  "Matt Gurney", "sovereign wealth fund", "Canadian Economy"
- primary_category: single best match from the categories list, same lowercase-hyphen format
- Be specific and accurate based on the actual content, not generic`

// Options configures a categorize run.
type Options struct {
	Model           string
	Timeout         time.Duration
	Concurrency     int
	Limit           int
	Force           bool
	Apply           bool // save result back to user_tags
	IncludeImages   bool // embed local or archived photos as base64 for vision models
	OpenRouterBase  string
	OpenRouterKey   string
	OpenRouterRef   string
	OpenRouterTitle string
	UserAgent       string
	OllamaBase      string
	OllamaKey       string
	// R2/S3 credentials for fetching archived photos not present locally.
	S3Endpoint  string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	// Vocab is the optional canonical vocabulary loaded from categories.yaml.
	Vocab categoryvocab.Vocab
	// OnStart is called after item selection, before worker goroutines start.
	OnStart func(total int)
	// OnResult is called from worker goroutines as each item completes.
	// It must be safe to call concurrently. Leave nil to skip streaming output.
	OnResult func(ItemResult)
	// OnSourceResult is called from worker goroutines as each source completes.
	// It must be safe to call concurrently. Leave nil to skip streaming output.
	OnSourceResult func(SourceResult)
}

// Result holds the LLM categorization output for one item.
type Result struct {
	Categories      []string `json:"categories"`
	Tags            []string `json:"tags"`
	PrimaryCategory string   `json:"primary_category"`
	Model           string   `json:"model"`
	RawResponse     string   `json:"raw_response,omitempty"`
}

// ItemResult pairs an item with its categorization result or error.
type ItemResult struct {
	Item   model.Item `json:"item"`
	Result Result     `json:"result,omitempty"`
	Error  string     `json:"error,omitempty"`
}

// SourceResult pairs a source with its categorization result or error.
type SourceResult struct {
	Source model.SourceDocument `json:"source"`
	Result Result               `json:"result,omitempty"`
	Error  string               `json:"error,omitempty"`
}

// Stats summarises a batch categorize run.
type Stats struct {
	Queued    int `json:"queued"`
	Succeeded int `json:"succeeded"`
	Applied   int `json:"applied"`
	Skipped   int `json:"skipped"`
	Errors    int `json:"errors"`
}

// chatContent is either a plain string (text-only) or a []contentPart (multimodal).
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentPart
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Think    *bool           `json:"think,omitempty"`
}

type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}
