package xphotoocr

import (
	"log/slog"
	"time"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultOCRModel          = "openrouter/google/gemini-3.1-flash-lite-preview"
	defaultOllamaBaseURL     = "http://127.0.0.1:11434"
	defaultOllamaAPIKey      = "ollama"
	openRouterVisionTool     = "openrouter-vision"
	openRouterVisionVersion  = "openrouter-vision-v1"
	ollamaVisionTool         = "ollama-vision"
	ollamaVisionVersion      = "ollama-vision-v1"
	tesseractTool            = "tesseract"
	tesseractVersion         = "tesseract-v1"
	ocrPrompt                = "Extract readable text from this image. Return only the visible text, preserving obvious line breaks. If there is no readable text, return one concise factual sentence describing the image. Do not add markdown, labels, commentary, or object descriptions when readable text is present."
)

type Options struct {
	Limit           int
	Force           bool
	Concurrency     int
	Timeout         time.Duration
	Model           string
	TesseractBinary string
	OpenRouterBase  string
	OpenRouterKey   string
	OpenRouterTitle string
	OpenRouterRef   string
	UserAgent       string
	OllamaBase      string
	OllamaKey       string
	Logger          *slog.Logger
}

type Stats struct {
	ItemsQueued     int `json:"items_queued"`
	ItemsProcessed  int `json:"items_processed"`
	ItemsUpdated    int `json:"items_updated"`
	ItemsUnchanged  int `json:"items_unchanged"`
	ItemsSkipped    int `json:"items_skipped"`
	PhotoCandidates int `json:"photo_candidates"`
	PhotosOCRed     int `json:"photos_ocred"`
	HostedAttempts  int `json:"hosted_attempts"`
	HostedFallbacks int `json:"hosted_fallbacks"`
	Errors          int `json:"errors"`
}

type ocrRequest struct {
	Model    string       `json:"model"`
	Messages []ocrMessage `json:"messages"`
	Stream   bool         `json:"stream,omitempty"`
}

type ocrMessage struct {
	Role    string       `json:"role"`
	Content []ocrMsgPart `json:"content"`
}

type ocrMsgPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *ocrImageURL `json:"image_url,omitempty"`
}

type ocrImageURL struct {
	URL string `json:"url"`
}

type ocrResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type ollamaOCRRequest struct {
	Model    string             `json:"model"`
	Messages []ollamaOCRMessage `json:"messages"`
	Stream   bool               `json:"stream"`
	Think    *bool              `json:"think,omitempty"`
}

type ollamaOCRMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type ollamaOCRResponse struct {
	Model   string `json:"model"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type ocrBlock struct {
	Heading     string `json:"heading"`
	LocalPath   string `json:"local_path"`
	RemoteURL   string `json:"remote_url"`
	ExpandedURL string `json:"expanded_url"`
	Tool        string `json:"tool"`
	Model       string `json:"model"`
	Text        string `json:"text"`
}

type ocrItemOutcome struct {
	itemsUpdated    int
	itemsUnchanged  int
	itemsSkipped    int
	photoCandidates int
	photosOCRed     int
	hostedAttempts  int
	hostedFallbacks int
	errors          int
}
