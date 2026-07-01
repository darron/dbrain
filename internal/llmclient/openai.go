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

type openAIChatRequest struct {
	Model         string          `json:"model"`
	Messages      []openAIMessage `json:"messages"`
	Stream        bool            `json:"stream"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	RepeatPenalty *float64        `json:"repeat_penalty,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAIChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func chatOpenAI(ctx context.Context, client *http.Client, req Request, target llmprovider.Target, accounting llmprovider.ParamAccounting) (Response, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	body := openAIChatRequest{
		Model:    target.Ref.APIModel,
		Messages: openAIMessages(req.Messages),
		Stream:   false,
	}
	applyOpenAIParams(&body, accounting.Sent)
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal %s chat request: %w", target.Spec.DisplayName, err)
	}
	httpReq, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, strings.TrimRight(target.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create %s chat request: %w", target.Spec.DisplayName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if auth := target.AuthorizationHeader(); auth != "" {
		httpReq.Header.Set("Authorization", auth)
	}
	for key, value := range target.Headers {
		if strings.TrimSpace(value) != "" {
			httpReq.Header.Set(key, value)
		}
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
	var out openAIChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("parse %s chat response: %w", target.Spec.DisplayName, err)
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("run %s chat: response contained no choices", target.Spec.DisplayName)
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return Response{}, fmt.Errorf("run %s chat: response contained no content", target.Spec.DisplayName)
	}
	return responseFromTarget(target, text, raw, accounting), nil
}

func openAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, message := range messages {
		parts := make([]openAIContentPart, 0, len(message.Parts))
		var textOnly strings.Builder
		hasImage := false
		for _, part := range message.Parts {
			switch part.Type {
			case ContentImage:
				hasImage = true
				mime := strings.TrimSpace(part.MIMEType)
				if mime == "" {
					mime = "image/jpeg"
				}
				parts = append(parts, openAIContentPart{
					Type:     "image_url",
					ImageURL: &openAIImageURL{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(part.ImageData)},
				})
			default:
				if hasImage {
					parts = append(parts, openAIContentPart{Type: "text", Text: part.Text})
				} else {
					if textOnly.Len() > 0 {
						textOnly.WriteString("\n")
					}
					textOnly.WriteString(part.Text)
				}
			}
		}
		if hasImage {
			if textOnly.Len() > 0 {
				parts = append([]openAIContentPart{{Type: "text", Text: textOnly.String()}}, parts...)
			}
			out = append(out, openAIMessage{Role: message.Role, Content: parts})
		} else {
			out = append(out, openAIMessage{Role: message.Role, Content: textOnly.String()})
		}
	}
	return out
}

func applyOpenAIParams(req *openAIChatRequest, params map[string]any) {
	if value, ok := floatParam(params, "temperature"); ok {
		req.Temperature = &value
	}
	if value, ok := floatParam(params, "top_p"); ok {
		req.TopP = &value
	}
	if value, ok := intParam(params, "top_k"); ok {
		req.TopK = &value
	}
	if value, ok := floatParam(params, "repeat_penalty"); ok {
		req.RepeatPenalty = &value
	}
}

func previewBody(raw []byte, max int) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > max {
		return text[:max]
	}
	return text
}
