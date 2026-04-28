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

	"dbrain/internal/config"
	"dbrain/internal/mediaarchive"
	"dbrain/internal/model"
	"dbrain/internal/runtimeenv"
	"dbrain/internal/store"
)

const (
	defaultModel        = "openrouter/google/gemini-2.5-flash"
	defaultOllamaBase   = "http://127.0.0.1:11434"
	defaultOllamaKey    = "ollama"
	defaultOpenRouterBase = "https://openrouter.ai/api/v1"
	defaultTimeout      = 90 * time.Second
	maxArticleChars     = 3000
	maxTranscriptChars  = 3000
)

const systemPrompt = `You are categorizing items for a personal knowledge base.
Analyze the provided content and respond with ONLY valid JSON — no markdown, no code fences, no explanation.

Required format:
{
  "categories": ["Category Name"],
  "tags": ["tag-name"],
  "primary_category": "Most Relevant Category"
}

Rules:
- categories: 2-5 broad topical themes, Title Case, 1-3 words each (e.g. "Canadian Politics", "Economic Policy")
- tags: 5-10 specific concepts, people, places, or events; ALL lowercase, ALL spaces replaced with hyphens, no exceptions
  Good tags: "matt-gurney", "sovereign-wealth-fund", "canadian-economy"
  Bad tags:  "Matt Gurney", "sovereign wealth fund", "Canadian Economy"
- primary_category: single best match from the categories list
- Be specific and accurate based on the actual content, not generic`

// Options configures a categorize run.
type Options struct {
	Model           string
	Timeout         time.Duration
	Concurrency     int
	Limit           int
	Force           bool
	Apply           bool // save result back to user_tags
	IncludeImages   bool // embed archived photos as base64 for vision models
	OpenRouterBase  string
	OpenRouterKey   string
	OpenRouterRef   string
	OpenRouterTitle string
	OllamaBase      string
	OllamaKey       string
	// R2/S3 credentials for fetching archived photos not present locally.
	S3Endpoint  string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	// OnResult is called from worker goroutines as each item completes.
	// It must be safe to call concurrently. Leave nil to skip streaming output.
	OnResult func(ItemResult)
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
		tags := joinTags(result)
		if err := st.SaveItemUserTags(ctx, item.ID, tags); err != nil {
			return result, fmt.Errorf("save user_tags: %w", err)
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
	sysMsg := ollamaMessage{Role: "system", Content: systemPrompt}
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

	return parseCategorizationJSON(resp.Message.Content, ollamaModel)
}

func callOpenRouter(ctx context.Context, bundle string, photoData [][]byte, openrouterModel string, opts Options) (Result, error) {
	if strings.TrimSpace(opts.OpenRouterKey) == "" {
		return Result{}, fmt.Errorf("OPENROUTER_API_KEY / DBRAIN_OPENROUTER_API_KEY not set")
	}

	sysMsg := chatMessage{Role: "system", Content: systemPrompt}

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

	return parseCategorizationJSON(resp.Choices[0].Message.Content, openrouterModel)
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

// parseCategorizationJSON strips optional markdown fences and parses the JSON.
func parseCategorizationJSON(content string, modelName string) (Result, error) {
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

	// Normalize regardless of whether the model followed the prompt rules.
	for i, t := range result.Tags {
		result.Tags[i] = normalizeTag(t)
	}
	result.Tags = dedupeStrings(result.Tags)

	for i, c := range result.Categories {
		result.Categories[i] = strings.TrimSpace(c)
	}
	result.PrimaryCategory = strings.TrimSpace(result.PrimaryCategory)

	result.Model = modelName
	result.RawResponse = content
	return result, nil
}

// normalizeTag lowercases a tag and replaces every run of non-alphanumeric
// characters with a single hyphen, matching the "lowercase-hyphenated" rule.
func normalizeTag(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else {
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			prevHyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ---- helpers ------------------------------------------------------------------

func resolveOpts(cfg config.Config, opts *Options) {
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

func joinTags(r Result) string {
	seen := map[string]struct{}{}
	var parts []string
	for _, t := range append(r.Tags, r.Categories...) {
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
