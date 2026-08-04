package slackclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
)

const testWebhookURL = "https://hooks.slack.com/services/T00000000/B00000000/secret-token"

func TestSendWebhookPostsEscapedTextAndReturnsAcceptanceTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var gotMethod, gotContentType, gotURL string
	var gotBody []byte
	client, err := New(Options{
		Now: func() time.Time { return now },
		HTTPClientFactory: func(policy safehttp.Policy) HTTPDoer {
			if !policy.DisableRedirects || !policy.DisableCompression || policy.AllowPrivateNetwork || len(policy.AllowedPrivateOrigins) != 0 || len(policy.AllowedOrigins) != 1 || policy.AllowedOrigins[0] != "https://hooks.slack.com:443" {
				t.Fatalf("unsafe HTTP policy: %#v", policy)
			}
			if policy.ConnectTimeout != 5*time.Second || policy.TLSHandshakeTimeout != 5*time.Second || policy.ResponseHeaderTimeout != 5*time.Second {
				t.Fatalf("transport timeout policy = %#v", policy)
			}
			return doerFunc(func(request *http.Request) (*http.Response, error) {
				gotMethod, gotContentType, gotURL = request.Method, request.Header.Get("Content-Type"), request.URL.String()
				gotBody, _ = io.ReadAll(request.Body)
				return response(http.StatusOK, "ok"), nil
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "A&B<C>"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotContentType != "application/json" || gotURL != testWebhookURL {
		t.Fatalf("request = method:%q content-type:%q url:%q", gotMethod, gotContentType, gotURL)
	}
	if got, want := string(gotBody), `{"text":"A\u0026B\u003cC\u003e"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if receipt != (Receipt{AcceptedAt: now}) {
		t.Fatalf("receipt = %#v, want accepted time %s", receipt, now)
	}
}

func TestSendWebhookRejectsInvalidOfficialServiceURLsWithoutDispatch(t *testing.T) {
	t.Parallel()
	for _, webhookURL := range []string{
		"http://hooks.slack.com/services/a/b/c",
		"https://example.com/services/a/b/c",
		"https://hooks.slack.com:444/services/a/b/c",
		"https://user:secret@hooks.slack.com/services/a/b/c",
		"https://hooks.slack.com/services/a/b/c?secret=value",
		"https://hooks.slack.com/services/a/b/c#secret",
		"https://hooks.slack.com/services/a/b",
		"https://hooks.slack.com/services/a/b/c/d",
		"https://hooks.slack.com/services/a//c",
	} {
		t.Run(webhookURL, func(t *testing.T) {
			dispatched := false
			client, err := New(Options{HTTPClientFactory: func(safehttp.Policy) HTTPDoer {
				dispatched = true
				return doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("must not dispatch") })
			}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.SendWebhook(t.Context(), webhookURL, Message{Text: "body"})
			assertDeliveryError(t, err, DeliveryPermanent, "slack_invalid_payload")
			if dispatched {
				t.Fatal("HTTP client factory called for invalid webhook URL")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), webhookURL) {
				t.Fatalf("error leaked webhook: %v", err)
			}
		})
	}
}

func TestSendWebhookClassifiesSlackResponsesWithoutLeakingBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		kind   DeliveryErrorKind
		code   string
	}{
		{name: "invalid payload", status: http.StatusBadRequest, body: "invalid_payload", kind: DeliveryPermanent, code: "slack_invalid_payload"},
		{name: "disabled hook", status: http.StatusGone, body: "no_active_hooks", kind: DeliveryPermanent, code: "slack_webhook_disabled"},
		{name: "prohibited", status: http.StatusForbidden, body: "action_prohibited", kind: DeliveryPermanent, code: "slack_action_prohibited"},
		{name: "archived", status: http.StatusNotFound, body: "channel_is_archived", kind: DeliveryPermanent, code: "slack_channel_archived"},
		{name: "unknown client response", status: http.StatusBadRequest, body: "provider detail secret", kind: DeliveryPermanent, code: "slack_delivery_failed"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: "rate_limited", kind: DeliveryTemporary, code: "slack_rate_limited"},
		{name: "service unavailable", status: http.StatusServiceUnavailable, body: "provider detail secret", kind: DeliveryTemporary, code: "slack_service_unavailable"},
		{name: "unexpected success", status: http.StatusOK, body: "not-ok-secret", kind: DeliveryPermanent, code: "slack_delivery_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(*http.Request) (*http.Response, error) { return response(test.status, test.body), nil })
			_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
			assertDeliveryError(t, err, test.kind, test.code)
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), testWebhookURL) {
				t.Fatalf("error leaked provider response or webhook: %v", err)
			}
		})
	}
}

func TestSendWebhookBoundsResponseRead(t *testing.T) {
	t.Parallel()
	tooLarge := strings.Repeat("x", 4097)
	client := newTestClient(t, func(*http.Request) (*http.Response, error) { return response(http.StatusBadRequest, tooLarge), nil })
	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryPermanent, "slack_delivery_failed")
}

func TestSendWebhookMarksPostDispatchTimeoutAmbiguous(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryAmbiguous, "slack_delivery_failed")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestSendWebhookRejectsCanceledContextBeforeDispatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := newTestClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("must not dispatch") })
	_, err := client.SendWebhook(ctx, testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryTemporary, "slack_delivery_failed")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (fn doerFunc) Do(request *http.Request) (*http.Response, error) { return fn(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}
}

func newTestClient(t *testing.T, do doerFunc) Client {
	t.Helper()
	client, err := New(Options{HTTPClientFactory: func(safehttp.Policy) HTTPDoer { return do }})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertDeliveryError(t *testing.T, err error, kind DeliveryErrorKind, code string) {
	t.Helper()
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("error = %T %v, want DeliveryError", err, err)
	}
	if deliveryErr.Kind != kind || deliveryErr.Code != code || deliveryErr.Error() != code {
		t.Fatalf("delivery error = %#v, want kind=%q code=%q", deliveryErr, kind, code)
	}
}
