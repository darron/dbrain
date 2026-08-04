package slackclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
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

	receipt, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "A&B<C> <!channel> <@USER> <https://example.test/path|label>"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotContentType != "application/json" || gotURL != testWebhookURL {
		t.Fatalf("request = method:%q content-type:%q url:%q", gotMethod, gotContentType, gotURL)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("decode outbound JSON: %v", err)
	}
	if got, want := payload.Text, "A&amp;B&lt;C&gt; &lt;!channel&gt; &lt;@USER&gt; &lt;https://example.test/path|label&gt;"; got != want {
		t.Fatalf("Slack-visible text = %q, want %q", got, want)
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
		"https://hooks.slack.com/services/./b/c",
		"https://hooks.slack.com/services/a/../c",
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

func TestSendWebhookAllowsGovSlackOrigin(t *testing.T) {
	t.Parallel()
	const webhookURL = "https://hooks.slack-gov.com/services/T00000000/B00000000/secret-token"
	client, err := New(Options{HTTPClientFactory: func(policy safehttp.Policy) HTTPDoer {
		if got, want := policy.AllowedOrigins, []string{"https://hooks.slack-gov.com:443"}; !equalStrings(got, want) {
			t.Fatalf("allowed origins = %#v, want %#v", got, want)
		}
		return doerFunc(func(*http.Request) (*http.Response, error) { return response(http.StatusOK, "ok"), nil })
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendWebhook(t.Context(), webhookURL, Message{Text: "body"}); err != nil {
		t.Fatal(err)
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

func TestSendWebhookMarksResponseReadFailureAmbiguous(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &failingReadCloser{data: []byte("ok"), err: errors.New("provider response secret")},
			Header:     make(http.Header),
		}, nil
	})

	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryAmbiguous, "slack_delivery_failed")
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked response detail: %v", err)
	}
}

func TestSendWebhookBoundsPayloadBeforeDispatch(t *testing.T) {
	t.Parallel()
	const maxPayloadBytes = 40 << 10
	const jsonEnvelopeBytes = len(`{"text":""}`)
	tests := []struct {
		name         string
		text         string
		wantDispatch bool
	}{
		{name: "boundary", text: strings.Repeat("x", maxPayloadBytes-jsonEnvelopeBytes), wantDispatch: true},
		{name: "over limit", text: strings.Repeat("x", maxPayloadBytes-jsonEnvelopeBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatches := 0
			client := newTestClient(t, func(*http.Request) (*http.Response, error) {
				dispatches++
				return response(http.StatusOK, "ok"), nil
			})

			_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: test.text})
			if test.wantDispatch {
				if err != nil {
					t.Fatal(err)
				}
				if dispatches != 1 {
					t.Fatalf("dispatches = %d, want 1", dispatches)
				}
				return
			}
			assertDeliveryError(t, err, DeliveryPermanent, "slack_invalid_payload")
			if dispatches != 0 {
				t.Fatalf("dispatches = %d, want 0", dispatches)
			}
		})
	}
}

func TestSendWebhookMarksPreDispatchFailureTemporary(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryTemporary, "slack_delivery_failed")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestSendWebhookMarksPostDispatchFailureAmbiguous(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		if trace == nil || trace.WroteRequest == nil {
			t.Fatal("request has no WroteRequest trace")
		}
		trace.WroteRequest(httptrace.WroteRequestInfo{})
		return nil, context.DeadlineExceeded
	})
	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryAmbiguous, "slack_delivery_failed")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestSendWebhookMarksWroteRequestErrorTemporary(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		if trace == nil || trace.WroteRequest == nil {
			t.Fatal("request has no WroteRequest trace")
		}
		trace.WroteRequest(httptrace.WroteRequestInfo{Err: context.Canceled})
		return nil, context.Canceled
	})
	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryTemporary, "slack_delivery_failed")
}

func TestSendWebhookKeepsSuccessfulWriteAmbiguousAfterLaterWriteError(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		if trace == nil || trace.WroteRequest == nil {
			t.Fatal("request has no WroteRequest trace")
		}
		trace.WroteRequest(httptrace.WroteRequestInfo{})
		trace.WroteRequest(httptrace.WroteRequestInfo{Err: context.Canceled})
		return nil, context.DeadlineExceeded
	})
	_, err := client.SendWebhook(t.Context(), testWebhookURL, Message{Text: "body"})
	assertDeliveryError(t, err, DeliveryAmbiguous, "slack_delivery_failed")
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

type failingReadCloser struct {
	data []byte
	err  error
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, r.err
}

func (*failingReadCloser) Close() error { return nil }

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

func equalStrings(left, right []string) bool {
	return len(left) == len(right) && (len(left) == 0 || left[0] == right[0])
}
