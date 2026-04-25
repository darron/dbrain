package xphotoocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultOCRModel          = "openrouter/google/gemini-3.1-flash-lite-preview"
	openRouterVisionTool     = "openrouter-vision"
	openRouterVisionVersion  = "openrouter-vision-v1"
	tesseractTool            = "tesseract"
	tesseractVersion         = "tesseract-v1"
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

type ocrBlock struct {
	Heading     string `json:"heading"`
	LocalPath   string `json:"local_path"`
	RemoteURL   string `json:"remote_url"`
	ExpandedURL string `json:"expanded_url"`
	Tool        string `json:"tool"`
	Model       string `json:"model"`
	Text        string `json:"text"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = defaultOCRModel
	}
	if strings.TrimSpace(opts.TesseractBinary) == "" {
		opts.TesseractBinary = "tesseract"
	}
	if strings.TrimSpace(opts.OpenRouterBase) == "" {
		opts.OpenRouterBase = firstNonEmpty(strings.TrimSpace(os.Getenv("DBRAIN_OPENROUTER_BASE_URL")), defaultOpenRouterBaseURL)
	}
	if strings.TrimSpace(opts.OpenRouterKey) == "" {
		opts.OpenRouterKey = firstNonEmpty(strings.TrimSpace(os.Getenv("DBRAIN_OPENROUTER_API_KEY")), strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")))
	}
	if strings.TrimSpace(opts.OpenRouterTitle) == "" {
		opts.OpenRouterTitle = firstNonEmpty(strings.TrimSpace(os.Getenv("DBRAIN_OPENROUTER_TITLE")), "dbrain X photo OCR")
	}
	if strings.TrimSpace(opts.OpenRouterRef) == "" {
		opts.OpenRouterRef = firstNonEmpty(strings.TrimSpace(os.Getenv("DBRAIN_OPENROUTER_REFERER")), "https://local.dbrain")
	}

	items, err := st.ListItemsForXPhotoOCR(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		ItemsQueued:    len(items),
		ItemsProcessed: len(items),
	}
	if len(items) == 0 {
		return stats, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}
	debugLog(opts.Logger, "x photo ocr candidates loaded", "items", len(items), "limit", opts.Limit, "force", opts.Force, "concurrency", concurrency)

	var mu sync.Mutex
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
					outcome := processOCRItem(ctx, cfg, st, opts, item)
					mu.Lock()
					stats.ItemsUpdated += outcome.itemsUpdated
					stats.ItemsUnchanged += outcome.itemsUnchanged
					stats.ItemsSkipped += outcome.itemsSkipped
					stats.PhotoCandidates += outcome.photoCandidates
					stats.PhotosOCRed += outcome.photosOCRed
					stats.HostedAttempts += outcome.hostedAttempts
					stats.HostedFallbacks += outcome.hostedFallbacks
					stats.Errors += outcome.errors
					mu.Unlock()
				}
			}
		}()
	}
dispatchLoop:
	for _, item := range items {
		select {
		case <-ctx.Done():
			break dispatchLoop
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return stats, err
	}
	return stats, nil
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

func processOCRItem(ctx context.Context, cfg config.Config, st *store.Store, opts Options, item model.Item) ocrItemOutcome {
	outcome := ocrItemOutcome{}
	if err := ctx.Err(); err != nil {
		return outcome
	}

	refs, err := st.ListItemMediaRefs(ctx, item.ID)
	if err != nil {
		if isContextCanceled(err) || ctx.Err() != nil {
			return outcome
		}
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr refs failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	photos := downloadedPhotoRefs(refs)
	outcome.photoCandidates = len(photos)
	if len(photos) == 0 {
		outcome.itemsSkipped++
		return outcome
	}

	blocks := make([]ocrBlock, 0, len(photos))
	lastErr := ""
	toolsUsed := map[string]struct{}{}
	modelsUsed := map[string]struct{}{}
	for _, ref := range photos {
		if err := ctx.Err(); err != nil {
			return outcome
		}
		absolutePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
		block, hostedAttempted, hostedFallback, err := ocrPhoto(ctx, absolutePath, ref, opts)
		if hostedAttempted {
			outcome.hostedAttempts++
		}
		if hostedFallback {
			outcome.hostedFallbacks++
		}
		if err != nil {
			if isContextCanceled(err) || ctx.Err() != nil {
				return outcome
			}
			lastErr = err.Error()
			debugLog(opts.Logger, "x photo ocr failed", "source_key", item.SourceKey, "item_id", item.ID, "local_path", ref.LocalPath, "error", err.Error())
			continue
		}
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		blocks = append(blocks, block)
		toolsUsed[block.Tool] = struct{}{}
		if strings.TrimSpace(block.Model) != "" {
			modelsUsed[block.Model] = struct{}{}
		}
		outcome.photosOCRed++
	}

	inputHash := hashPhotoInputs(photos)
	if err := ctx.Err(); err != nil {
		return outcome
	}
	if len(blocks) == 0 {
		_, saveErr := st.SaveItemOCR(ctx, item.ID, model.OCRResult{
			Status:      "error",
			Error:       firstNonEmpty(lastErr, "no OCR text extracted"),
			FetchedAt:   time.Now().UTC(),
			Tool:        collapseSet(toolsUsed),
			ToolVersion: collapseToolVersion(toolsUsed),
			Model:       collapseSet(modelsUsed),
		}, inputHash)
		if saveErr != nil {
			if isContextCanceled(saveErr) || ctx.Err() != nil {
				return outcome
			}
			outcome.errors++
			debugLog(opts.Logger, "x photo ocr state save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", saveErr.Error())
		} else {
			outcome.errors++
		}
		outcome.itemsSkipped++
		return outcome
	}

	rawJSON, _ := json.Marshal(blocks)
	changed, err := st.SaveItemOCR(ctx, item.ID, model.OCRResult{
		Text:        renderOCRBlocks(blocks),
		RawJSON:     string(rawJSON),
		Status:      "ok",
		Model:       collapseSet(modelsUsed),
		FetchedAt:   time.Now().UTC(),
		Tool:        collapseSet(toolsUsed),
		ToolVersion: collapseToolVersion(toolsUsed),
	}, inputHash)
	if err != nil {
		if isContextCanceled(err) || ctx.Err() != nil {
			return outcome
		}
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	if !changed {
		outcome.itemsUnchanged++
		debugLog(opts.Logger, "x photo ocr unchanged", "source_key", item.SourceKey, "item_id", item.ID)
		return outcome
	}

	refreshed, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		if isContextCanceled(err) || ctx.Err() != nil {
			return outcome
		}
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr refresh failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	if err := vault.WriteItem(cfg, refreshed); err != nil {
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr note write failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	outcome.itemsUpdated++
	debugLog(opts.Logger, "x photo ocr saved", "source_key", item.SourceKey, "item_id", item.ID, "ocr_chars", len(refreshed.OCRText), "tool", refreshed.OCRTool, "model", refreshed.OCRModel)
	return outcome
}

func downloadedPhotoRefs(refs []model.ItemMediaRef) []model.ItemMediaRef {
	photos := make([]model.ItemMediaRef, 0, len(refs))
	for _, ref := range refs {
		if ref.MediaType != "photo" || ref.DownloadStatus != "downloaded" || strings.TrimSpace(ref.LocalPath) == "" || !ref.LocalPrunedAt.IsZero() {
			continue
		}
		photos = append(photos, ref)
	}
	return photos
}

func ocrPhoto(ctx context.Context, absolutePath string, ref model.ItemMediaRef, opts Options) (ocrBlock, bool, bool, error) {
	heading := fmt.Sprintf("Photo %d", ref.Ordinal+1)
	hostedAttempted := false
	hostedFallback := false
	if strings.TrimSpace(opts.OpenRouterKey) != "" {
		hostedAttempted = true
		text, modelName, err := ocrWithOpenRouter(ctx, absolutePath, opts)
		if err == nil && strings.TrimSpace(text) != "" {
			return ocrBlock{
				Heading:     heading,
				LocalPath:   ref.LocalPath,
				RemoteURL:   ref.RemoteURL,
				ExpandedURL: ref.ExpandedURL,
				Tool:        openRouterVisionTool,
				Model:       modelName,
				Text:        text,
			}, hostedAttempted, hostedFallback, nil
		}
		hostedFallback = true
	}

	text, err := ocrWithTesseract(ctx, absolutePath, opts.TesseractBinary, opts.Timeout)
	if err != nil {
		return ocrBlock{}, hostedAttempted, hostedFallback, err
	}
	return ocrBlock{
		Heading:     heading,
		LocalPath:   ref.LocalPath,
		RemoteURL:   ref.RemoteURL,
		ExpandedURL: ref.ExpandedURL,
		Tool:        tesseractTool,
		Model:       "",
		Text:        text,
	}, hostedAttempted, hostedFallback, nil
}

func ocrWithOpenRouter(ctx context.Context, absolutePath string, opts Options) (string, string, error) {
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return "", "", fmt.Errorf("read image: %w", err)
	}
	mimeType := http.DetectContentType(data)
	modelName := stripOpenRouterPrefix(opts.Model)
	payload, err := json.Marshal(ocrRequest{
		Model: modelName,
		Messages: []ocrMessage{{
			Role: "user",
			Content: []ocrMsgPart{
				{Type: "text", Text: "Extract all readable text from this image. Return plain text only. Preserve obvious line breaks. If the image contains very little readable text, return a concise factual description instead. Do not add markdown fences or commentary."},
				{Type: "image_url", ImageURL: &ocrImageURL{URL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}},
			},
		}},
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal openrouter ocr request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(opts.OpenRouterBase, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("build openrouter ocr request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(opts.OpenRouterKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", opts.OpenRouterRef)
	req.Header.Set("X-Title", opts.OpenRouterTitle)

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("openrouter ocr request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("read openrouter ocr response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("openrouter ocr %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out ocrResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("decode openrouter ocr response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", "", fmt.Errorf("openrouter ocr returned no text")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), firstNonEmpty(strings.TrimSpace(opts.Model), strings.TrimSpace(out.Model)), nil
}

func ocrWithTesseract(ctx context.Context, absolutePath, binary string, timeout time.Duration) (string, error) {
	ocrCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ocrCtx, binary, absolutePath, "stdout")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, context.Canceled) {
			return "", context.Canceled
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("tesseract: %s", errMsg)
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", fmt.Errorf("tesseract returned no text")
	}
	return text, nil
}

func renderOCRBlocks(blocks []ocrBlock) string {
	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block.Heading)
		b.WriteString("\n")
		b.WriteString(block.Text)
	}
	return b.String()
}

func stripOpenRouterPrefix(model string) string {
	model = strings.TrimSpace(model)
	return strings.TrimPrefix(model, "openrouter/")
}

func hashPhotoInputs(refs []model.ItemMediaRef) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, strings.TrimSpace(ref.LocalPath)+"|"+strings.TrimSpace(ref.RemoteURL))
	}
	slices.Sort(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func collapseSet(values map[string]struct{}) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, value)
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	if len(keys) == 1 {
		return keys[0]
	}
	return strings.Join(keys, ",")
}

func collapseToolVersion(values map[string]struct{}) string {
	versions := make([]string, 0, len(values))
	for value := range values {
		switch value {
		case openRouterVisionTool:
			versions = append(versions, openRouterVisionVersion)
		case tesseractTool:
			versions = append(versions, tesseractVersion)
		}
	}
	if len(versions) == 0 {
		return ""
	}
	slices.Sort(versions)
	return strings.Join(versions, ",")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
