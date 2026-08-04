// Package slackclient posts bounded text messages to Slack incoming webhooks.
// It deliberately owns neither webhook configuration nor secret resolution.
package slackclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
)

const (
	transportTimeout       = 5 * time.Second
	maxResponseBytes       = 4096
	maxWebhookPayloadBytes = 40 << 10
)

type Message struct {
	Text string
}

// Receipt records Slack's acceptance time. Incoming webhooks do not return a
// Slack message identifier.
type Receipt struct {
	AcceptedAt time.Time
}

type Client interface {
	SendWebhook(context.Context, string, Message) (Receipt, error)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClientFactory is injectable for deterministic transport tests. The
// default creates a safehttp client using the policy supplied for each hook.
type HTTPClientFactory func(safehttp.Policy) HTTPDoer

type Options struct {
	Now               func() time.Time
	HTTPClientFactory HTTPClientFactory
}

type DeliveryErrorKind string

const (
	DeliveryTemporary DeliveryErrorKind = "temporary"
	DeliveryPermanent DeliveryErrorKind = "permanent"
	DeliveryAmbiguous DeliveryErrorKind = "ambiguous"
)

type DeliveryError struct {
	Kind DeliveryErrorKind
	Code string
	err  error
}

func NewDeliveryError(kind DeliveryErrorKind, code string, cause error) error {
	return &DeliveryError{Kind: kind, Code: code, err: sanitizedCause(code, cause)}
}

func (e *DeliveryError) Error() string {
	if e != nil && validCode(e.Code) {
		return e.Code
	}
	return "slack_delivery_failed"
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type client struct {
	now               func() time.Time
	httpClientFactory HTTPClientFactory
}

func New(options Options) (Client, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	factory := options.HTTPClientFactory
	if factory == nil {
		factory = func(policy safehttp.Policy) HTTPDoer { return safehttp.NewClient(policy) }
	}
	return &client{now: now, httpClientFactory: factory}, nil
}

func (c *client) SendWebhook(ctx context.Context, webhookURL string, message Message) (Receipt, error) {
	origin, err := validateWebhookURL(webhookURL)
	if err != nil {
		return Receipt{}, permanent("slack_invalid_payload", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, temporary("slack_delivery_failed", err)
	}
	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: escapeSlackText(message.Text)})
	if err != nil {
		return Receipt{}, permanent("slack_invalid_payload", err)
	}
	if len(body) > maxWebhookPayloadBytes {
		return Receipt{}, permanent("slack_invalid_payload", nil)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, permanent("slack_invalid_payload", err)
	}
	request.Header.Set("Content-Type", "application/json")
	wroteRequest := false
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			// A write error means the transport did not successfully dispatch the
			// request, so it remains safe for the manager to retry.
			wroteRequest = info.Err == nil
		},
	}))

	httpClient := c.httpClientFactory(safehttp.Policy{
		Timeout:               transportTimeout,
		AllowedOrigins:        []string{origin},
		DisableRedirects:      true,
		DisableCompression:    true,
		ConnectTimeout:        transportTimeout,
		TLSHandshakeTimeout:   transportTimeout,
		ResponseHeaderTimeout: transportTimeout,
	})
	if httpClient == nil {
		return Receipt{}, temporary("slack_delivery_failed", nil)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		if safehttp.IsPolicyError(err) {
			return Receipt{}, permanent("slack_delivery_failed", err)
		}
		if !wroteRequest {
			return Receipt{}, temporary("slack_delivery_failed", err)
		}
		return Receipt{}, ambiguous("slack_delivery_failed", err)
	}
	if response == nil || response.Body == nil {
		return Receipt{}, ambiguous("slack_delivery_failed", nil)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil {
		return Receipt{}, ambiguous("slack_delivery_failed", readErr)
	}
	if len(responseBody) > maxResponseBytes {
		return Receipt{}, responseError(response.StatusCode, "")
	}
	if response.StatusCode == http.StatusOK && string(responseBody) == "ok" {
		return Receipt{AcceptedAt: c.now().UTC()}, nil
	}
	return Receipt{}, responseError(response.StatusCode, string(responseBody))
}

func validateWebhookURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("invalid Slack webhook URL")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if parsed.Scheme != "https" || (host != "hooks.slack.com" && host != "hooks.slack-gov.com") || (parsed.Port() != "" && parsed.Port() != "443") {
		return "", errors.New("invalid Slack webhook URL")
	}
	if parsed.RawPath != "" {
		return "", errors.New("invalid Slack webhook URL")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "services" || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[1] == "." || parts[1] == ".." || parts[2] == "." || parts[2] == ".." || parts[3] == "." || parts[3] == ".." {
		return "", errors.New("invalid Slack webhook URL")
	}
	return "https://" + host + ":443", nil
}

func escapeSlackText(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

func responseError(status int, responseBody string) error {
	if status == http.StatusTooManyRequests {
		return temporary("slack_rate_limited", nil)
	}
	if status >= 500 && status <= 599 {
		return temporary("slack_service_unavailable", nil)
	}
	switch strings.TrimSpace(responseBody) {
	case "invalid_payload":
		return permanent("slack_invalid_payload", nil)
	case "no_active_hooks":
		return permanent("slack_webhook_disabled", nil)
	case "action_prohibited":
		return permanent("slack_action_prohibited", nil)
	case "channel_is_archived":
		return permanent("slack_channel_archived", nil)
	}
	if status >= 400 && status <= 499 {
		return permanent("slack_delivery_failed", nil)
	}
	if status >= 200 && status <= 299 {
		return permanent("slack_delivery_failed", nil)
	}
	return temporary("slack_delivery_failed", nil)
}

func temporary(code string, cause error) error {
	return NewDeliveryError(DeliveryTemporary, code, cause)
}
func permanent(code string, cause error) error {
	return NewDeliveryError(DeliveryPermanent, code, cause)
}
func ambiguous(code string, cause error) error {
	return NewDeliveryError(DeliveryAmbiguous, code, cause)
}

func sanitizedCause(code string, cause error) error {
	switch {
	case errors.Is(cause, context.Canceled):
		return context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return errors.New(safeCode(code))
	}
}

func validCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func safeCode(code string) string {
	if validCode(code) {
		return code
	}
	return "slack_delivery_failed"
}
