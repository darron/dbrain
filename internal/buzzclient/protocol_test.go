package buzzclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/keyer"
)

func TestBuzzClientAUTHThenPublishesSignedChannelEvent(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 8, 3, 18, 10, 11, 0, time.UTC)
	var acceptedAt = time.Date(2026, 8, 3, 18, 10, 12, 0, time.UTC)
	var acceptedEventID string
	fixture := newRelayFixture(t, func(ctx context.Context, conn *websocket.Conn, relayURL string) {
		relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge-42"})
		auth := readEventEnvelope(t, ctx, conn, "AUTH")
		assertSignedEvent(t, auth)
		if auth.Kind != nostr.KindClientAuthentication || auth.Content != "" || countTag(auth.Tags, "relay", relayURL) != 1 || countTag(auth.Tags, "challenge", "challenge-42") != 1 {
			t.Errorf("auth event = %#v", auth)
		}
		relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, true, ""})

		event := readEventEnvelope(t, ctx, conn, "EVENT")
		assertSignedEvent(t, event)
		if event.Kind != 9 || event.PubKey != auth.PubKey || event.Content != "scheduled sync failed" || event.CreatedAt != nostr.Timestamp(createdAt.Unix()) || countTag(event.Tags, "h", testChannelID) != 1 || len(event.Tags) != 1 {
			t.Errorf("message event = %#v", event)
		}
		acceptedEventID = event.ID
		relayWriteEnvelope(t, ctx, conn, []any{"OK", event.ID, true, ""})
	})
	client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true, Timeout: time.Second}, func() time.Time { return acceptedAt })
	signer, _ := keyer.NewPlainKeySigner(testSecretKey)
	receipt, err := client.SendChannelMessage(t.Context(), signer, validMessage(createdAt))
	if err != nil {
		t.Fatal(err)
	}
	fixture.wait()
	if receipt.EventID == "" || receipt.EventID != acceptedEventID || receipt.AcceptedAt != acceptedAt {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestBuzzClientRejectsSignerMutationOfUnsignedProtocolFields(t *testing.T) {
	t.Parallel()
	mutations := []struct {
		name   string
		mutate func(*nostr.Event)
	}{
		{name: "kind", mutate: func(event *nostr.Event) { event.Kind++ }},
		{name: "created at", mutate: func(event *nostr.Event) { event.CreatedAt++ }},
		{name: "tags", mutate: func(event *nostr.Event) { event.Tags[0][1] = "mutated-secret-tag" }},
		{name: "content", mutate: func(event *nostr.Event) { event.Content = "mutated-secret-content" }},
	}
	stages := []struct {
		name       string
		mutateCall int
		handler    relayHandler
	}{
		{
			name:       "AUTH",
			mutateCall: 1,
			handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
				relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
				_, _, _ = conn.Read(ctx)
			},
		},
		{
			name:       "kind 9 message",
			mutateCall: 2,
			handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
				relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
				auth := readEventEnvelope(t, ctx, conn, "AUTH")
				relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, true, ""})
				_, _, _ = conn.Read(ctx)
			},
		},
	}
	for _, stage := range stages {
		for _, mutation := range mutations {
			t.Run(stage.name+" "+mutation.name, func(t *testing.T) {
				fixture := newRelayFixture(t, stage.handler)
				client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true, Timeout: time.Second}, time.Now)
				plain, err := keyer.NewPlainKeySigner(testSecretKey)
				if err != nil {
					t.Fatal(err)
				}
				signer := &mutatingSigner{signer: plain, mutateCall: stage.mutateCall, mutate: mutation.mutate}
				_, err = client.SendChannelMessage(t.Context(), signer, validMessage(time.Now().UTC()))
				fixture.wait()
				assertDeliveryError(t, err, DeliveryPermanent, "buzz_signer_mutated")
				if formatted := fmt.Sprintf("%v\n%+v\n%#v", err, err, err); strings.Contains(formatted, "mutated-secret") {
					t.Fatalf("mutation error leaked signer-controlled data: %s", formatted)
				}
			})
		}
	}
}

func TestBuzzClientDeterministicMessageEventID(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 8, 3, 18, 10, 11, 987654321, time.UTC)
	ids := make([]string, 0, 2)
	for range 2 {
		fixture := newRelayFixture(t, acceptProtocol(func(event nostr.Event) { ids = append(ids, event.ID) }))
		client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true}, time.Now)
		signer, _ := keyer.NewPlainKeySigner(testSecretKey)
		if _, err := client.SendChannelMessage(t.Context(), signer, validMessage(createdAt)); err != nil {
			t.Fatal(err)
		}
		fixture.wait()
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("event IDs = %v, want identical", ids)
	}
}

func TestBuzzClientRelayFailuresAreTypedAndSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler relayHandler
		kind    DeliveryErrorKind
		code    string
	}{
		{name: "authentication rejection", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
			auth := readEventEnvelope(t, ctx, conn, "AUTH")
			relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, false, "auth-required: channel and secret leak"})
		}, kind: DeliveryPermanent, code: "buzz_auth_rejected"},
		{name: "membership rejection", handler: rejectionHandler(t, "restricted: channel and secret leak"), kind: DeliveryPermanent, code: "buzz_not_authorized"},
		{name: "invalid event rejection", handler: rejectionHandler(t, "invalid: channel and secret leak"), kind: DeliveryPermanent, code: "buzz_event_invalid"},
		{name: "unknown rejection", handler: rejectionHandler(t, "some raw channel and secret leak"), kind: DeliveryPermanent, code: "buzz_rejected"},
		{name: "notice before publish", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			relayWriteEnvelope(t, ctx, conn, []any{"NOTICE", "raw channel and secret leak"})
		}, kind: DeliveryTemporary, code: "buzz_notice"},
		{name: "closed before publish", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			relayWriteEnvelope(t, ctx, conn, []any{"CLOSED", "sub", "raw channel and secret leak"})
		}, kind: DeliveryTemporary, code: "buzz_closed"},
		{name: "malformed frame", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"not":"an envelope"}`))
		}, kind: DeliveryTemporary, code: "buzz_protocol_invalid"},
		{name: "oversized frame", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			_ = conn.Write(ctx, websocket.MessageText, []byte(strings.Repeat("x", maxRelayFrameBytes+1)))
		}, kind: DeliveryTemporary, code: "buzz_frame_too_large"},
		{name: "notice after publish", handler: postPublishEnvelope(t, []any{"NOTICE", "raw channel and secret leak"}), kind: DeliveryAmbiguous, code: "buzz_notice"},
		{name: "closed after publish", handler: postPublishEnvelope(t, []any{"CLOSED", "sub", "raw channel and secret leak"}), kind: DeliveryAmbiguous, code: "buzz_closed"},
		{name: "malformed frame after publish", handler: postPublishRaw(t, []byte(`{"not":"an envelope"}`)), kind: DeliveryAmbiguous, code: "buzz_protocol_invalid"},
		{name: "oversized frame after publish", handler: postPublishRaw(t, []byte(strings.Repeat("x", maxRelayFrameBytes+1))), kind: DeliveryAmbiguous, code: "buzz_frame_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelayFixture(t, test.handler)
			client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true, Timeout: time.Second}, time.Now)
			signer, _ := keyer.NewPlainKeySigner(testSecretKey)
			_, err := client.SendChannelMessage(t.Context(), signer, ChannelMessage{ChannelID: testChannelID, Content: "secret leak", CreatedAt: time.Now().UTC()})
			fixture.wait()
			assertDeliveryError(t, err, test.kind, test.code)
			if strings.Contains(fmt.Sprintf("%v", err), "secret leak") || strings.Contains(fmt.Sprintf("%v", err), testChannelID) {
				t.Fatalf("error leaked relay or message data: %v", err)
			}
		})
	}
}

func TestBuzzClientCancellationAfterPublishIsAmbiguous(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	fixture := newRelayFixture(t, func(serverCtx context.Context, conn *websocket.Conn, _ string) {
		relayWriteEnvelope(t, serverCtx, conn, []any{"AUTH", "challenge"})
		auth := readEventEnvelope(t, serverCtx, conn, "AUTH")
		relayWriteEnvelope(t, serverCtx, conn, []any{"OK", auth.ID, true, ""})
		_ = readEventEnvelope(t, serverCtx, conn, "EVENT")
		cancel()
		_, _, _ = conn.Read(serverCtx)
	})
	client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true, Timeout: time.Second}, time.Now)
	signer, _ := keyer.NewPlainKeySigner(testSecretKey)
	_, err := client.SendChannelMessage(ctx, signer, validMessage(time.Now().UTC()))
	fixture.wait()
	assertDeliveryError(t, err, DeliveryAmbiguous, "buzz_publish_canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v does not unwrap cancellation", err)
	}
}

func TestBuzzClientTimeoutStages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler relayHandler
		kind    DeliveryErrorKind
		code    string
	}{
		{name: "missing auth", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			_, _, _ = conn.Read(ctx)
		}, kind: DeliveryTemporary, code: "buzz_auth_timeout"},
		{name: "publish response", handler: func(ctx context.Context, conn *websocket.Conn, _ string) {
			relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
			auth := readEventEnvelope(t, ctx, conn, "AUTH")
			relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, true, ""})
			_ = readEventEnvelope(t, ctx, conn, "EVENT")
			_, _, _ = conn.Read(ctx)
		}, kind: DeliveryAmbiguous, code: "buzz_publish_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelayFixture(t, test.handler)
			client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true, Timeout: 80 * time.Millisecond}, time.Now)
			signer, _ := keyer.NewPlainKeySigner(testSecretKey)
			_, err := client.SendChannelMessage(t.Context(), signer, validMessage(time.Now().UTC()))
			fixture.wait()
			assertDeliveryError(t, err, test.kind, test.code)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error %v does not unwrap deadline", err)
			}
		})
	}
}

func TestBuzzClientPrivateOriginRequiresExactOptIn(t *testing.T) {
	t.Parallel()
	fixture := newRelayFixture(t, acceptProtocol(nil))
	client := fixture.client(Options{RelayURL: fixture.relayURL, Timeout: time.Second}, time.Now)
	signer, _ := keyer.NewPlainKeySigner(testSecretKey)
	_, err := client.SendChannelMessage(t.Context(), signer, validMessage(time.Now().UTC()))
	assertDeliveryError(t, err, DeliveryTemporary, "buzz_connect_failed")
}

func TestBuzzClientRejectsRedirect(t *testing.T) {
	t.Parallel()
	target := newRelayFixture(t, acceptProtocol(nil))
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.relayURL, "wss://", "https://", 1), http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	relayURL := strings.Replace(redirect.URL, "https://", "wss://", 1)
	deps := localTLSDependencies(redirect)
	client, err := newClient(Options{RelayURL: relayURL, AllowPrivateOrigin: true, Timeout: time.Second}, deps)
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := keyer.NewPlainKeySigner(testSecretKey)
	_, err = client.SendChannelMessage(t.Context(), signer, validMessage(time.Now().UTC()))
	assertDeliveryError(t, err, DeliveryTemporary, "buzz_connect_failed")
}

type relayHandler func(context.Context, *websocket.Conn, string)

type relayFixture struct {
	t        *testing.T
	server   *httptest.Server
	relayURL string
	done     chan struct{}
	once     sync.Once
}

func newRelayFixture(t *testing.T, handler relayHandler) *relayFixture {
	t.Helper()
	fixture := &relayFixture{t: t, done: make(chan struct{})}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer fixture.finish()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		handler(ctx, conn, fixture.relayURL)
	}))
	fixture.relayURL = strings.Replace(fixture.server.URL, "https://", "wss://", 1)
	t.Cleanup(func() { fixture.server.Close(); fixture.finish() })
	return fixture
}

func (f *relayFixture) finish() { f.once.Do(func() { close(f.done) }) }

func (f *relayFixture) wait() {
	f.t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		f.t.Fatal("relay fixture did not finish")
	}
}

func (f *relayFixture) client(opts Options, now func() time.Time) Client {
	f.t.Helper()
	deps := localTLSDependencies(f.server)
	deps.now = now
	client, err := newClient(opts, deps)
	if err != nil {
		f.t.Fatal(err)
	}
	return client
}

func localTLSDependencies(server *httptest.Server) clientDependencies {
	transport := server.Client().Transport.(*http.Transport)
	return clientDependencies{
		lookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		dialContext:     (&net.Dialer{Timeout: time.Second}).DialContext,
		tlsClientConfig: transport.TLSClientConfig.Clone(),
	}
}

func acceptProtocol(observe func(nostr.Event)) relayHandler {
	return func(ctx context.Context, conn *websocket.Conn, relayURL string) {
		_ = writeJSON(ctx, conn, []any{"AUTH", "challenge"})
		auth, err := readEvent(ctx, conn, "AUTH")
		if err != nil {
			return
		}
		_ = writeJSON(ctx, conn, []any{"OK", auth.ID, true, ""})
		event, err := readEvent(ctx, conn, "EVENT")
		if err != nil {
			return
		}
		if observe != nil {
			observe(event)
		}
		_ = writeJSON(ctx, conn, []any{"OK", event.ID, true, ""})
	}
}

func rejectionHandler(t *testing.T, reason string) relayHandler {
	return func(ctx context.Context, conn *websocket.Conn, _ string) {
		relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
		auth := readEventEnvelope(t, ctx, conn, "AUTH")
		relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, true, ""})
		event := readEventEnvelope(t, ctx, conn, "EVENT")
		relayWriteEnvelope(t, ctx, conn, []any{"OK", event.ID, false, reason})
	}
}

func postPublishEnvelope(t *testing.T, envelope any) relayHandler {
	return func(ctx context.Context, conn *websocket.Conn, _ string) {
		relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
		auth := readEventEnvelope(t, ctx, conn, "AUTH")
		relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, true, ""})
		_ = readEventEnvelope(t, ctx, conn, "EVENT")
		relayWriteEnvelope(t, ctx, conn, envelope)
	}
}

func postPublishRaw(t *testing.T, payload []byte) relayHandler {
	return func(ctx context.Context, conn *websocket.Conn, _ string) {
		relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
		auth := readEventEnvelope(t, ctx, conn, "AUTH")
		relayWriteEnvelope(t, ctx, conn, []any{"OK", auth.ID, true, ""})
		_ = readEventEnvelope(t, ctx, conn, "EVENT")
		_ = conn.Write(ctx, websocket.MessageText, payload)
	}
}

func relayWriteEnvelope(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	if err := writeJSON(ctx, conn, value); err != nil && ctx.Err() == nil {
		t.Errorf("write: %v", err)
	}
}

func writeJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

func readEventEnvelope(t *testing.T, ctx context.Context, conn *websocket.Conn, wantType string) nostr.Event {
	t.Helper()
	event, err := readEvent(ctx, conn, wantType)
	if err != nil {
		t.Errorf("read %s: %v", wantType, err)
		return nostr.Event{}
	}
	return event
}

func readEvent(ctx context.Context, conn *websocket.Conn, wantType string) (nostr.Event, error) {
	_, payload, err := conn.Read(ctx)
	if err != nil {
		return nostr.Event{}, err
	}
	var envelope []json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope) != 2 {
		return nostr.Event{}, fmt.Errorf("invalid envelope")
	}
	var typ string
	if err := json.Unmarshal(envelope[0], &typ); err != nil || typ != wantType {
		return nostr.Event{}, fmt.Errorf("type = %q", typ)
	}
	var event nostr.Event
	if err := json.Unmarshal(envelope[1], &event); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func assertSignedEvent(t *testing.T, event nostr.Event) {
	t.Helper()
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		t.Errorf("invalid signature: ok=%v err=%v", ok, err)
	}
}

func countTag(tags nostr.Tags, key, value string) int {
	count := 0
	for _, tag := range tags {
		if len(tag) == 2 && tag[0] == key && tag[1] == value {
			count++
		}
	}
	return count
}
