package xphotoocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/version"
)

func ocrWithOllama(ctx context.Context, absolutePath string, opts Options, ollamaModel string) (string, string, error) {
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return "", "", fmt.Errorf("read image: %w", err)
	}
	think := false
	payload, err := json.Marshal(ollamaOCRRequest{
		Model:  ollamaModel,
		Stream: false,
		Think:  &think,
		Messages: []ollamaOCRMessage{{
			Role:    "user",
			Content: ocrPrompt,
			Images:  []string{base64.StdEncoding.EncodeToString(data)},
		}},
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal ollama ocr request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(opts.OllamaBase, "/")+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("build ollama ocr request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(opts.OllamaKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(opts.OllamaKey))
	}

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ollama ocr request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", fmt.Errorf("read ollama ocr response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("ollama ocr %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out ollamaOCRResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("decode ollama ocr response: %w", err)
	}
	if strings.TrimSpace(out.Message.Content) == "" {
		return "", "", fmt.Errorf("ollama ocr returned no text")
	}
	return strings.TrimSpace(out.Message.Content), firstNonEmpty(strings.TrimSpace(opts.Model), "ollama/"+ollamaModel), nil
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
				{Type: "text", Text: ocrPrompt},
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
	req.Header.Set("User-Agent", version.UserAgent(opts.UserAgent))

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
