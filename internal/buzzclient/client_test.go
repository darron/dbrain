package buzzclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/keyer"
)

const (
	testSecretKey = "0000000000000000000000000000000000000000000000000000000000000001"
	testChannelID = "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2"
)

func TestBuzzClientRejectsInvalidConfigurationAndMessage(t *testing.T) {
	t.Parallel()
	signer, err := keyer.NewPlainKeySigner(testSecretKey)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		opts    Options
		message ChannelMessage
		code    string
	}{
		{name: "https relay", opts: Options{RelayURL: "https://relay.example"}, message: validMessage(createdAt), code: "buzz_relay_url_invalid"},
		{name: "relay path", opts: Options{RelayURL: "wss://relay.example/path"}, message: validMessage(createdAt), code: "buzz_relay_url_invalid"},
		{name: "relay credentials", opts: Options{RelayURL: "wss://user:secret@relay.example"}, message: validMessage(createdAt), code: "buzz_relay_url_invalid"},
		{name: "invalid channel", opts: Options{RelayURL: "wss://relay.example"}, message: ChannelMessage{ChannelID: "secret-channel", Content: "body", CreatedAt: createdAt}, code: "buzz_channel_invalid"},
		{name: "empty content", opts: Options{RelayURL: "wss://relay.example"}, message: ChannelMessage{ChannelID: testChannelID, CreatedAt: createdAt}, code: "buzz_content_invalid"},
		{name: "oversized content", opts: Options{RelayURL: "wss://relay.example"}, message: ChannelMessage{ChannelID: testChannelID, Content: strings.Repeat("x", 4097), CreatedAt: createdAt}, code: "buzz_content_too_large"},
		{name: "zero timestamp", opts: Options{RelayURL: "wss://relay.example"}, message: ChannelMessage{ChannelID: testChannelID, Content: "body"}, code: "buzz_created_at_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(test.opts)
			if err == nil {
				_, err = client.SendChannelMessage(t.Context(), signer, test.message)
			}
			assertDeliveryError(t, err, DeliveryPermanent, test.code)
			if (test.message.ChannelID != "" && strings.Contains(fmt.Sprint(err), test.message.ChannelID)) ||
				(test.message.Content != "" && strings.Contains(fmt.Sprint(err), test.message.Content)) {
				t.Fatalf("error leaked message data: %v", err)
			}
		})
	}
}

func TestBuzzClientPublicOriginRejectsPrivateAndMixedDNSAnswers(t *testing.T) {
	t.Parallel()
	for _, addrs := range [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.2")},
	} {
		var dialCalls atomic.Int32
		client, err := newClient(Options{RelayURL: "wss://relay.example", Timeout: 200 * time.Millisecond}, clientDependencies{
			lookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) { return addrs, nil },
			dialContext: func(context.Context, string, string) (net.Conn, error) {
				dialCalls.Add(1)
				return nil, errors.New("dial must not be reached")
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		signer, _ := keyer.NewPlainKeySigner(testSecretKey)
		_, err = client.SendChannelMessage(t.Context(), signer, validMessage(time.Now().UTC()))
		assertDeliveryError(t, err, DeliveryTemporary, "buzz_connect_failed")
		if got := dialCalls.Load(); got != 0 {
			t.Fatalf("validated dial called %d times for non-public DNS answer", got)
		}
	}
}

func TestBuzzClientContextCancellationIsSafe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client, err := New(Options{RelayURL: "wss://relay.example"})
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := keyer.NewPlainKeySigner(testSecretKey)
	_, err = client.SendChannelMessage(ctx, signer, validMessage(time.Now().UTC()))
	assertDeliveryError(t, err, DeliveryTemporary, "buzz_canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v does not unwrap context cancellation", err)
	}
}

func TestBuzzClientConnectionTimeoutUsesShorterCallerDeadline(t *testing.T) {
	t.Parallel()
	client, err := newClient(Options{RelayURL: "wss://relay.example", Timeout: time.Second}, clientDependencies{
		lookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		dialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	signer, _ := keyer.NewPlainKeySigner(testSecretKey)
	_, err = client.SendChannelMessage(ctx, signer, validMessage(time.Now().UTC()))
	assertDeliveryError(t, err, DeliveryTemporary, "buzz_connect_timeout")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v does not unwrap deadline", err)
	}
}

func TestBuzzClientSignerOperationFailuresRemainRetryable(t *testing.T) {
	pubKey, err := nostr.GetPublicKey(testSecretKey)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		signer    nostr.Signer
		code      string
		unwrapErr error
	}{
		{name: "public key cancellation", signer: failingSigner{getErr: context.Canceled}, code: "buzz_canceled", unwrapErr: context.Canceled},
		{name: "public key transient failure", signer: failingSigner{getErr: errors.New("remote signer offline")}, code: "buzz_auth_sign_failed"},
		{name: "sign deadline", signer: failingSigner{pubKey: pubKey, signErr: context.DeadlineExceeded}, code: "buzz_signer_timeout", unwrapErr: context.DeadlineExceeded},
		{name: "sign transient failure", signer: failingSigner{pubKey: pubKey, signErr: errors.New("hardware signer unavailable")}, code: "buzz_auth_sign_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelayFixture(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
				relayWriteEnvelope(t, ctx, conn, []any{"AUTH", "challenge"})
				_, _, _ = conn.Read(ctx)
			})
			client := fixture.client(Options{RelayURL: fixture.relayURL, AllowPrivateOrigin: true, Timeout: time.Second}, time.Now)
			_, err := client.SendChannelMessage(t.Context(), test.signer, validMessage(time.Now().UTC()))
			fixture.wait()
			assertDeliveryError(t, err, DeliveryTemporary, test.code)
			if test.unwrapErr != nil && !errors.Is(err, test.unwrapErr) {
				t.Fatalf("error %v does not unwrap %v", err, test.unwrapErr)
			}
		})
	}
}

type failingSigner struct {
	pubKey  string
	getErr  error
	signErr error
}

func (s failingSigner) GetPublicKey(context.Context) (string, error) { return s.pubKey, s.getErr }

func (s failingSigner) SignEvent(context.Context, *nostr.Event) error { return s.signErr }

type mutatingSigner struct {
	signer     nostr.Signer
	mutateCall int
	calls      int
	mutate     func(*nostr.Event)
}

func (s *mutatingSigner) GetPublicKey(ctx context.Context) (string, error) {
	return s.signer.GetPublicKey(ctx)
}

func (s *mutatingSigner) SignEvent(ctx context.Context, event *nostr.Event) error {
	s.calls++
	if s.calls == s.mutateCall {
		s.mutate(event)
	}
	return s.signer.SignEvent(ctx, event)
}

func validMessage(createdAt time.Time) ChannelMessage {
	return ChannelMessage{ChannelID: testChannelID, Content: "scheduled sync failed", CreatedAt: createdAt}
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
