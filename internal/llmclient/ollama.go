package llmclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darron/dbrain/internal/llmprovider"
)

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Think    *bool           `json:"think,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

func chatOllama(ctx context.Context, client *http.Client, req Request, target llmprovider.Target, accounting llmprovider.ParamAccounting) (Response, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	think := false
	body := ollamaChatRequest{
		Model:    target.Ref.APIModel,
		Messages: ollamaMessages(req.Messages),
		Stream:   false,
		Think:    &think,
	}
	if len(accounting.Sent) > 0 {
		body.Options = llmprovider.CloneAnyMap(accounting.Sent)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal %s chat request: %w", target.Spec.DisplayName, err)
	}
	httpReq, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, strings.TrimRight(target.BaseURL, "/")+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create %s chat request: %w", target.Spec.DisplayName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if auth := target.AuthorizationHeader(); auth != "" {
		httpReq.Header.Set("Authorization", auth)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("run %s chat: %w", target.Spec.DisplayName, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read %s chat response: %w", target.Spec.DisplayName, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("run %s chat: http %d: %s", target.Spec.DisplayName, resp.StatusCode, previewBody(raw, 300))
	}
	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("parse %s chat response: %w", target.Spec.DisplayName, err)
	}
	text := strings.TrimSpace(out.Message.Content)
	if text == "" {
		return Response{}, fmt.Errorf("run %s chat: response contained no content", target.Spec.DisplayName)
	}
	return responseFromTarget(target, text, raw, accounting), nil
}

func ollamaMessages(messages []Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(messages))
	for _, message := range messages {
		var content strings.Builder
		var images []string
		for _, part := range message.Parts {
			switch part.Type {
			case ContentImage:
				images = append(images, base64.StdEncoding.EncodeToString(part.ImageData))
			default:
				if content.Len() > 0 {
					content.WriteString("\n")
				}
				content.WriteString(part.Text)
			}
		}
		out = append(out, ollamaMessage{
			Role:    message.Role,
			Content: content.String(),
			Images:  images,
		})
	}
	return out
}

func floatParam(params map[string]any, key string) (float64, bool) {
	switch value := params[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func intParam(params map[string]any, key string) (int, bool) {
	switch value := params[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}
