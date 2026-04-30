package itemcategorize

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
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

// ---- public API ----------------------------------------------------------------

// Run categorizes a single item and optionally saves the result.
func Run(ctx context.Context, cfg config.Config, st *store.Store, item model.Item, opts Options) (Result, error) {
	resolveOpts(cfg, &opts)

	refs, _ := st.ListItemMediaRefs(ctx, item.ID)

	s3client := buildS3Client(opts)
	bundle := buildContentBundle(item)
	photoData := loadPhotoBytes(ctx, cfg, refs, s3client, opts.IncludeImages)

	result, err := callLLM(ctx, bundle, photoData, opts)
	if err != nil {
		return Result{}, err
	}

	if opts.Apply {
		tags := MergeUserTags(item.UserTags, result)
		if err := st.SaveItemUserTags(ctx, item.ID, tags); err != nil {
			return result, fmt.Errorf("save user_tags: %w", err)
		}
	}

	return result, nil
}

// RunSource categorizes a single source and optionally saves the result.
func RunSource(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options) (Result, error) {
	resolveOpts(cfg, &opts)

	result, err := callLLM(ctx, buildSourceContentBundle(source), nil, opts)
	if err != nil {
		return Result{}, err
	}

	if opts.Apply {
		tags := MergeUserTags(source.UserTags, result)
		if err := st.SaveSourceUserTags(ctx, source.ID, tags); err != nil {
			return result, fmt.Errorf("save source user_tags: %w", err)
		}
	}

	return result, nil
}

// Batch categorizes multiple items (those without user_tags unless force is set).
func Batch(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []ItemResult, error) {
	resolveOpts(cfg, &opts)

	items, err := st.ListItemsForCategorize(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, nil, fmt.Errorf("list items: %w", err)
	}

	stats := Stats{Queued: len(items)}
	if opts.OnStart != nil {
		opts.OnStart(len(items))
	}
	if len(items) == 0 {
		return stats, nil, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}

	var mu sync.Mutex
	var results []ItemResult
	var wg sync.WaitGroup
	jobs := make(chan model.Item)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-jobs:
					if !ok {
						return
					}
					ir := ItemResult{Item: item}
					res, runErr := Run(ctx, cfg, st, item, opts)
					if runErr != nil {
						ir.Error = runErr.Error()
						mu.Lock()
						stats.Errors++
						results = append(results, ir)
						mu.Unlock()
					} else {
						ir.Result = res
						mu.Lock()
						stats.Succeeded++
						if opts.Apply {
							stats.Applied++
						}
						results = append(results, ir)
						mu.Unlock()
					}
					if opts.OnResult != nil {
						opts.OnResult(ir)
					}
				}
			}
		}()
	}

dispatch:
	for _, item := range items {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()

	return stats, results, ctx.Err()
}

// BatchSources categorizes multiple sources (those without user_tags unless force is set).
func BatchSources(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, []SourceResult, error) {
	resolveOpts(cfg, &opts)

	sources, err := st.ListSourcesForCategorize(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, nil, fmt.Errorf("list sources: %w", err)
	}

	stats := Stats{Queued: len(sources)}
	if opts.OnStart != nil {
		opts.OnStart(len(sources))
	}
	if len(sources) == 0 {
		return stats, nil, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	if concurrency > len(sources) {
		concurrency = len(sources)
	}

	var mu sync.Mutex
	var results []SourceResult
	var wg sync.WaitGroup
	jobs := make(chan model.SourceDocument)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case source, ok := <-jobs:
					if !ok {
						return
					}
					sr := SourceResult{Source: source}
					res, runErr := RunSource(ctx, cfg, st, source, opts)
					if runErr != nil {
						sr.Error = runErr.Error()
						mu.Lock()
						stats.Errors++
						results = append(results, sr)
						mu.Unlock()
					} else {
						sr.Result = res
						mu.Lock()
						stats.Succeeded++
						if opts.Apply {
							stats.Applied++
						}
						results = append(results, sr)
						mu.Unlock()
					}
					if opts.OnSourceResult != nil {
						opts.OnSourceResult(sr)
					}
				}
			}
		}()
	}

dispatch:
	for _, source := range sources {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- source:
		}
	}
	close(jobs)
	wg.Wait()

	return stats, results, ctx.Err()
}

// ---- content bundle ------------------------------------------------------------

func buildContentBundle(item model.Item) string {
	var sb strings.Builder

	// Metadata header
	sb.WriteString("source_type: " + item.SourceType + "\n")
	if item.AuthorHandle != "" {
		handle := "@" + item.AuthorHandle
		if item.AuthorName != "" {
			handle += " (" + item.AuthorName + ")"
		}
		sb.WriteString("author: " + handle + "\n")
	}
	if item.PublishedAt != "" {
		sb.WriteString("published: " + item.PublishedAt + "\n")
	}
	if lang := strings.TrimSpace(coalesce(item.Language, item.XPostLang)); lang != "" {
		sb.WriteString("language: " + lang + "\n")
	}
	if item.Title != "" {
		sb.WriteString("title: " + item.Title + "\n")
	}
	sb.WriteString("\n")

	// Primary text
	if postText := strings.TrimSpace(item.XPostText); postText != "" {
		sb.WriteString("Post:\n" + postText + "\n\n")
	} else if text := strings.TrimSpace(item.Text); text != "" {
		sb.WriteString("Text:\n" + text + "\n\n")
	}

	// Summary
	if summary := strings.TrimSpace(item.SummaryText); summary != "" {
		sb.WriteString("Summary:\n" + summary + "\n\n")
	}

	// Transcript or article body
	if articleText := strings.TrimSpace(item.ArticleText); articleText != "" {
		if item.ArticleTitle == "X Media Transcript" {
			if t := extractTranscriptText(articleText); t != "" {
				if len(t) > maxTranscriptChars {
					t = t[:maxTranscriptChars] + "…"
				}
				sb.WriteString("Transcript:\n" + t + "\n\n")
			}
		} else {
			body := articleText
			if len(body) > maxArticleChars {
				body = body[:maxArticleChars] + "…"
			}
			label := "Article"
			if item.ArticleTitle != "" {
				label = item.ArticleTitle
			}
			sb.WriteString(label + ":\n" + body + "\n\n")
		}
	}

	// OCR text (already extracted from images)
	if ocrText := strings.TrimSpace(item.OCRText); ocrText != "" {
		sb.WriteString("Image text (OCR):\n" + ocrText + "\n\n")
	}

	return strings.TrimSpace(sb.String())
}

func buildSourceContentBundle(source model.SourceDocument) string {
	var sb strings.Builder

	sb.WriteString("record_kind: source\n")
	sb.WriteString("source_type: " + source.SourceType + "\n")
	if source.Domain != "" {
		sb.WriteString("domain: " + source.Domain + "\n")
	}
	if source.SiteName != "" {
		sb.WriteString("site_name: " + source.SiteName + "\n")
	}
	if source.CanonicalURL != "" {
		sb.WriteString("url: " + source.CanonicalURL + "\n")
	}
	if source.Title != "" {
		sb.WriteString("title: " + source.Title + "\n")
	}
	sb.WriteString("\n")

	if description := strings.TrimSpace(source.Description); description != "" {
		sb.WriteString("Description:\n" + description + "\n\n")
	}
	if summary := strings.TrimSpace(source.SummaryText); summary != "" {
		sb.WriteString("Summary:\n" + summary + "\n\n")
	}
	if extracted := strings.TrimSpace(source.ExtractedText); extracted != "" {
		if len(extracted) > maxArticleChars {
			extracted = extracted[:maxArticleChars] + "…"
		}
		sb.WriteString("Extracted text:\n" + extracted + "\n\n")
	}

	return strings.TrimSpace(sb.String())
}

func extractTranscriptText(raw string) string {
	parts := strings.Split(raw, "\nTranscript:\n")
	if len(parts) <= 1 {
		return strings.TrimSpace(raw)
	}
	segments := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		if t := strings.TrimSpace(p); t != "" {
			segments = append(segments, t)
		}
	}
	return strings.Join(segments, "\n\n")
}

// loadPhotoBytes collects raw bytes for each photo associated with an item.
// For each photo ref it tries, in order:
//  1. Local file on disk (if present and not pruned)
//  2. R2/S3 archive (if the item was pruned locally but is archived)
//
// Refs that are neither locally available nor archived are silently skipped.
func loadPhotoBytes(ctx context.Context, cfg config.Config, refs []model.ItemMediaRef, s3client *s3.Client, include bool) [][]byte {
	if !include {
		return nil
	}
	var out [][]byte
	for _, ref := range refs {
		if ref.MediaType != "photo" {
			continue
		}
		// 1. Try local file first.
		if strings.TrimSpace(ref.LocalPath) != "" && ref.LocalPrunedAt.IsZero() {
			absPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
			if data, err := os.ReadFile(absPath); err == nil {
				out = append(out, data)
				continue
			}
		}
		// 2. Fall back to R2/S3 if archived.
		if s3client != nil && ref.ArchiveStatus == "archived" &&
			strings.TrimSpace(ref.ArchiveBucket) != "" && strings.TrimSpace(ref.ArchiveKey) != "" {
			if data, err := fetchFromS3(ctx, s3client, ref.ArchiveBucket, ref.ArchiveKey); err == nil {
				out = append(out, data)
			}
		}
	}
	return out
}

func fetchFromS3(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Body.Close() }()
	return io.ReadAll(output.Body)
}

func buildS3Client(opts Options) *s3.Client {
	if opts.S3Endpoint == "" || opts.S3AccessKey == "" || opts.S3SecretKey == "" {
		return nil
	}
	client, err := mediaarchive.NewS3Client(mediaarchive.Options{
		Endpoint:    opts.S3Endpoint,
		Region:      firstNonEmpty(opts.S3Region, "auto"),
		AccessKeyID: opts.S3AccessKey,
		SecretKey:   opts.S3SecretKey,
		PathStyle:   true,
	})
	if err != nil {
		return nil
	}
	return client
}

// ---- LLM call -----------------------------------------------------------------

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

func effectiveSystemPrompt(opts Options) string {
	vocab := opts.Vocab.PromptSection()
	if vocab == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + vocab
}

func callLLM(ctx context.Context, bundle string, photoData [][]byte, opts Options) (Result, error) {
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		return callOllama(ctx, bundle, photoData, ollamaModel, opts)
	}
	if openrouterModel, ok := parseOpenRouterModel(opts.Model); ok {
		return callOpenRouter(ctx, bundle, photoData, openrouterModel, opts)
	}
	// Fall back: treat the model as a plain OpenRouter model name.
	return callOpenRouter(ctx, bundle, photoData, opts.Model, opts)
}

func callOllama(ctx context.Context, bundle string, photoData [][]byte, ollamaModel string, opts Options) (Result, error) {
	think := false
	sysMsg := ollamaMessage{Role: "system", Content: effectiveSystemPrompt(opts)}
	userMsg := ollamaMessage{Role: "user", Content: bundle}
	for _, data := range photoData {
		userMsg.Images = append(userMsg.Images, base64.StdEncoding.EncodeToString(data))
	}

	reqBody := ollamaRequest{
		Model:    ollamaModel,
		Messages: []ollamaMessage{sysMsg, userMsg},
		Stream:   false,
		Think:    &think,
	}

	endpoint := strings.TrimRight(opts.OllamaBase, "/") + "/api/chat"
	raw, err := doPost(ctx, endpoint, opts.OllamaKey, nil, reqBody, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("ollama categorize: %w", err)
	}

	var resp ollamaResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Result{}, fmt.Errorf("parse ollama response: %w", err)
	}

	return parseCategorizationJSON(resp.Message.Content, ollamaModel, opts.Vocab)
}

func callOpenRouter(ctx context.Context, bundle string, photoData [][]byte, openrouterModel string, opts Options) (Result, error) {
	if strings.TrimSpace(opts.OpenRouterKey) == "" {
		return Result{}, fmt.Errorf("OPENROUTER_API_KEY / DBRAIN_OPENROUTER_API_KEY not set")
	}

	sysMsg := chatMessage{Role: "system", Content: effectiveSystemPrompt(opts)}

	var userContent any
	if len(photoData) > 0 {
		parts := []contentPart{{Type: "text", Text: bundle}}
		for _, data := range photoData {
			dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
			parts = append(parts, contentPart{
				Type:     "image_url",
				ImageURL: &imageURL{URL: dataURL},
			})
		}
		userContent = parts
	} else {
		userContent = bundle
	}

	reqBody := chatRequest{
		Model:    openrouterModel,
		Messages: []chatMessage{sysMsg, {Role: "user", Content: userContent}},
		Stream:   false,
	}

	endpoint := strings.TrimRight(opts.OpenRouterBase, "/") + "/chat/completions"
	headers := map[string]string{}
	if opts.OpenRouterRef != "" {
		headers["HTTP-Referer"] = opts.OpenRouterRef
	}
	if opts.OpenRouterTitle != "" {
		headers["X-Title"] = opts.OpenRouterTitle
	}
	headers["User-Agent"] = version.UserAgent(opts.UserAgent)

	raw, err := doPost(ctx, endpoint, opts.OpenRouterKey, headers, reqBody, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("openrouter categorize: %w", err)
	}

	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Result{}, fmt.Errorf("parse openrouter response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("openrouter categorize: no choices returned")
	}

	return parseCategorizationJSON(resp.Choices[0].Message.Content, openrouterModel, opts.Vocab)
}

func doPost(ctx context.Context, endpoint, apiKey string, headers map[string]string, body any, timeout time.Duration) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := strings.TrimSpace(string(raw))
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, preview)
	}

	return raw, nil
}

// parseCategorizationJSON strips optional markdown fences, parses the JSON,
// and applies canonical vocab rules if provided.
func parseCategorizationJSON(content string, modelName string, vocab categoryvocab.Vocab) (Result, error) {
	text := strings.TrimSpace(content)

	// Strip markdown code fences if the model wrapped the output
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		end := strings.LastIndex(text, "```")
		if end > start {
			text = strings.TrimSpace(text[start:end])
		}
	}

	// Find the JSON object boundaries (in case there's leading/trailing prose)
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if j := strings.LastIndex(text, "}"); j >= 0 && j < len(text)-1 {
		text = text[:j+1]
	}

	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return Result{}, fmt.Errorf("parse categorization JSON: %w (content: %q)", err, truncate(content, 300))
	}

	// Normalise and apply vocab to tags, categories, and primary_category.
	// ApplyToTokens always normalises to lowercase-hyphenated form, then
	// applies alias resolution and drops.
	result.Tags = vocab.ApplyToTokens(result.Tags)
	result.Categories = vocab.ApplyToTokens(result.Categories)
	if pc := vocab.ApplyToTokens([]string{result.PrimaryCategory}); len(pc) > 0 {
		result.PrimaryCategory = pc[0]
	} else {
		result.PrimaryCategory = ""
	}

	result.Model = modelName
	result.RawResponse = content
	return result, nil
}

// ---- helpers ------------------------------------------------------------------

func resolveOpts(cfg config.Config, opts *Options) {
	if opts.Vocab.Empty() {
		vocab, _ := categoryvocab.Load(cfg.CategoriesPath)
		opts.Vocab = vocab
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_CATEGORIZE_MODEL"), defaultModel)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if strings.TrimSpace(opts.OpenRouterBase) == "" {
		opts.OpenRouterBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"), defaultOpenRouterBase)
	}
	if strings.TrimSpace(opts.OpenRouterKey) == "" {
		opts.OpenRouterKey = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
	}
	if strings.TrimSpace(opts.OpenRouterRef) == "" {
		opts.OpenRouterRef = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"), "https://local.dbrain")
	}
	if strings.TrimSpace(opts.OpenRouterTitle) == "" {
		opts.OpenRouterTitle = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"), "dbrain categorize")
	}
	if strings.TrimSpace(opts.UserAgent) == "" {
		opts.UserAgent = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_USER_AGENT")
	}
	if strings.TrimSpace(opts.OllamaBase) == "" {
		opts.OllamaBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"), defaultOllamaBase)
	}
	if strings.TrimSpace(opts.OllamaKey) == "" {
		opts.OllamaKey = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"), defaultOllamaKey)
	}
	if strings.TrimSpace(opts.S3Endpoint) == "" {
		opts.S3Endpoint = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT")
	}
	if strings.TrimSpace(opts.S3AccessKey) == "" {
		opts.S3AccessKey = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	}
	if strings.TrimSpace(opts.S3SecretKey) == "" {
		opts.S3SecretKey = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	}
	if strings.TrimSpace(opts.S3Region) == "" {
		opts.S3Region = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION"), "auto")
	}
}

// MergeUserTags appends categorizer output to existing comma-separated
// user_tags while preserving the existing order and removing duplicates.
func MergeUserTags(existing string, r Result) string {
	seen := map[string]struct{}{}
	var parts []string
	candidates := append(splitTags(existing), r.Tags...)
	candidates = append(candidates, r.Categories...)
	for _, t := range candidates {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		parts = append(parts, t)
	}
	return strings.Join(parts, ",")
}

func splitTags(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseOllamaModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "ollama/"):
		return strings.TrimSpace(value[7:]), true
	case strings.HasPrefix(lower, "ollama:"):
		return strings.TrimSpace(value[7:]), true
	}
	return "", false
}

func parseOpenRouterModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "openrouter/"):
		return strings.TrimSpace(value[11:]), true
	case strings.HasPrefix(lower, "openrouter:"):
		return strings.TrimSpace(value[11:]), true
	}
	return "", false
}

func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
