// Package buzzclient implements the small Nostr protocol subset needed to post
// channel messages through Buzz. It deliberately owns no configuration or
// secret-resolution policy; callers provide a signer and a bounded message.
package buzzclient

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
)

const (
	defaultTimeout     = 10 * time.Second
	transportTimeout   = 3 * time.Second
	maxContentBytes    = 4096
	maxRelayFrameBytes = 64 << 10
)

type ChannelMessage struct {
	ChannelID string
	Content   string
	CreatedAt time.Time
}

type Receipt struct {
	EventID    string
	AcceptedAt time.Time
}

type Client interface {
	SendChannelMessage(context.Context, nostr.Signer, ChannelMessage) (Receipt, error)
}

type Options struct {
	RelayURL           string
	AllowPrivateOrigin bool
	Timeout            time.Duration
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
	return "buzz_delivery_failed"
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type client struct {
	relayURL           string
	handshakeOrigin    string
	allowPrivateOrigin bool
	timeout            time.Duration
	now                func() time.Time
	lookupNetIP        func(context.Context, string, string) ([]netip.Addr, error)
	dialContext        func(context.Context, string, string) (net.Conn, error)
	tlsClientConfig    *tls.Config
}

type clientDependencies struct {
	now             func() time.Time
	lookupNetIP     func(context.Context, string, string) ([]netip.Addr, error)
	dialContext     func(context.Context, string, string) (net.Conn, error)
	tlsClientConfig *tls.Config
}

func New(options Options) (Client, error) {
	return newClient(options, clientDependencies{})
}

func newClient(options Options, dependencies clientDependencies) (Client, error) {
	relayURL, handshakeOrigin, err := validateRelayURL(options.RelayURL)
	if err != nil {
		return nil, permanent("buzz_relay_url_invalid", err)
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	now := dependencies.now
	if now == nil {
		now = time.Now
	}
	return &client{
		relayURL: relayURL, handshakeOrigin: handshakeOrigin,
		allowPrivateOrigin: options.AllowPrivateOrigin, timeout: timeout, now: now,
		lookupNetIP: dependencies.lookupNetIP, dialContext: dependencies.dialContext,
		tlsClientConfig: dependencies.tlsClientConfig,
	}, nil
}

func (c *client) SendChannelMessage(ctx context.Context, signer nostr.Signer, message ChannelMessage) (Receipt, error) {
	if err := validateMessage(message); err != nil {
		return Receipt{}, err
	}
	if signer == nil {
		return Receipt{}, permanent("buzz_signer_invalid", nil)
	}
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	if err := callCtx.Err(); err != nil {
		return Receipt{}, contextDeliveryError(false, "connect", err)
	}

	allowedPrivate := []string(nil)
	if c.allowPrivateOrigin {
		allowedPrivate = []string{c.handshakeOrigin}
	}
	httpClient := safehttp.NewClient(safehttp.Policy{
		AllowedOrigins:        []string{c.handshakeOrigin},
		AllowedPrivateOrigins: allowedPrivate,
		DisableRedirects:      true,
		LookupNetIP:           c.lookupNetIP,
		DialContext:           c.dialContext,
		TLSClientConfig:       c.tlsClientConfig,
		ConnectTimeout:        boundedTransportTimeout(c.timeout),
		TLSHandshakeTimeout:   boundedTransportTimeout(c.timeout),
		ResponseHeaderTimeout: boundedTransportTimeout(c.timeout),
	})
	conn, _, err := websocket.Dial(callCtx, c.relayURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		if callCtx.Err() != nil {
			return Receipt{}, contextDeliveryError(false, "connect", callCtx.Err())
		}
		return Receipt{}, temporary("buzz_connect_failed", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(maxRelayFrameBytes)

	challenge, err := c.waitForAuth(callCtx, conn)
	if err != nil {
		return Receipt{}, err
	}
	authEvent := nostr.Event{
		CreatedAt: nostr.Timestamp(c.now().UTC().Unix()),
		Kind:      nostr.KindClientAuthentication,
		Tags: nostr.Tags{
			nostr.Tag{"relay", c.relayURL},
			nostr.Tag{"challenge", challenge},
		},
		Content: "",
	}
	pubKey, err := signChecked(callCtx, signer, &authEvent, "buzz_auth_sign_failed")
	if err != nil {
		return Receipt{}, err
	}
	if err := writeEnvelope(callCtx, conn, []any{"AUTH", authEvent}); err != nil {
		if callCtx.Err() != nil {
			return Receipt{}, contextDeliveryError(false, "auth", callCtx.Err())
		}
		return Receipt{}, temporary("buzz_auth_write_failed", err)
	}
	if err := c.waitForOK(callCtx, conn, authEvent.ID, false); err != nil {
		return Receipt{}, err
	}

	channelID := uuid.MustParse(strings.TrimSpace(message.ChannelID)).String()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(message.CreatedAt.UTC().Unix()),
		Kind:      9,
		Tags:      nostr.Tags{nostr.Tag{"h", channelID}},
		Content:   message.Content,
	}
	messagePubKey, err := signChecked(callCtx, signer, &event, "buzz_event_sign_failed")
	if err != nil {
		return Receipt{}, err
	}
	if messagePubKey != pubKey {
		return Receipt{}, permanent("buzz_signer_changed", nil)
	}
	if err := writeEnvelope(callCtx, conn, []any{"EVENT", event}); err != nil {
		if callCtx.Err() != nil {
			return Receipt{}, contextDeliveryError(true, "publish", callCtx.Err())
		}
		return Receipt{}, ambiguous("buzz_publish_failed", err)
	}
	if err := c.waitForOK(callCtx, conn, event.ID, true); err != nil {
		return Receipt{}, err
	}
	return Receipt{EventID: event.ID, AcceptedAt: c.now().UTC()}, nil
}

func (c *client) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= c.timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *client) waitForAuth(ctx context.Context, conn *websocket.Conn) (string, error) {
	for {
		envelope, err := readEnvelope(ctx, conn)
		if err != nil {
			if ctx.Err() != nil {
				return "", contextDeliveryError(false, "auth", ctx.Err())
			}
			return "", readError(false, err)
		}
		switch envelope.kind {
		case "AUTH":
			if len(envelope.parts) != 2 {
				return "", temporary("buzz_protocol_invalid", nil)
			}
			var challenge string
			if err := json.Unmarshal(envelope.parts[1], &challenge); err != nil || strings.TrimSpace(challenge) == "" || len(challenge) > 1024 {
				return "", temporary("buzz_protocol_invalid", nil)
			}
			return challenge, nil
		case "NOTICE":
			return "", temporary("buzz_notice", nil)
		case "CLOSED":
			return "", temporary("buzz_closed", nil)
		default:
			return "", temporary("buzz_protocol_invalid", nil)
		}
	}
}

func (c *client) waitForOK(ctx context.Context, conn *websocket.Conn, eventID string, published bool) error {
	for {
		envelope, err := readEnvelope(ctx, conn)
		if err != nil {
			if ctx.Err() != nil {
				stage := "auth"
				if published {
					stage = "publish"
				}
				return contextDeliveryError(published, stage, ctx.Err())
			}
			return readError(published, err)
		}
		switch envelope.kind {
		case "OK":
			if len(envelope.parts) != 4 {
				return stageError(published, "buzz_protocol_invalid")
			}
			var gotID string
			var accepted bool
			var reason string
			if json.Unmarshal(envelope.parts[1], &gotID) != nil || json.Unmarshal(envelope.parts[2], &accepted) != nil || json.Unmarshal(envelope.parts[3], &reason) != nil || gotID != eventID {
				return stageError(published, "buzz_protocol_invalid")
			}
			if accepted {
				return nil
			}
			if !published {
				return permanent("buzz_auth_rejected", nil)
			}
			return publishRejection(reason)
		case "NOTICE":
			return stageError(published, "buzz_notice")
		case "CLOSED":
			return stageError(published, "buzz_closed")
		default:
			return stageError(published, "buzz_protocol_invalid")
		}
	}
}

type relayEnvelope struct {
	kind  string
	parts []json.RawMessage
}

func readEnvelope(ctx context.Context, conn *websocket.Conn) (relayEnvelope, error) {
	typ, payload, err := conn.Read(ctx)
	if err != nil {
		if websocket.CloseStatus(err) == websocket.StatusMessageTooBig || strings.Contains(err.Error(), "read limited") {
			return relayEnvelope{}, errors.New("frame_too_large")
		}
		return relayEnvelope{}, err
	}
	if typ != websocket.MessageText || len(payload) > maxRelayFrameBytes {
		return relayEnvelope{}, errors.New("frame_too_large")
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(payload, &parts); err != nil || len(parts) == 0 {
		return relayEnvelope{}, errors.New("protocol_invalid")
	}
	var kind string
	if err := json.Unmarshal(parts[0], &kind); err != nil {
		return relayEnvelope{}, errors.New("protocol_invalid")
	}
	return relayEnvelope{kind: kind, parts: parts}, nil
}

func writeEnvelope(ctx context.Context, conn *websocket.Conn, envelope any) error {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

func validateRelayURL(raw string) (string, string, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Opaque != "" || target.User != nil || strings.ToLower(target.Scheme) != "wss" || target.Hostname() == "" {
		return "", "", errors.New("invalid relay URL")
	}
	if target.Path != "" || target.RawPath != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return "", "", errors.New("relay URL must be an origin")
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	hostPort := host
	if target.Port() != "" || strings.Contains(host, ":") {
		hostPort = net.JoinHostPort(host, target.Port())
		if target.Port() == "" {
			hostPort = "[" + host + "]"
		}
	}
	relayURL := "wss://" + hostPort
	origin, err := safehttp.CanonicalOriginEndpoint("https://" + hostPort)
	if err != nil {
		return "", "", err
	}
	return relayURL, origin, nil
}

func validateMessage(message ChannelMessage) error {
	if _, err := uuid.Parse(strings.TrimSpace(message.ChannelID)); err != nil {
		return permanent("buzz_channel_invalid", err)
	}
	if message.Content == "" || !utf8.ValidString(message.Content) {
		return permanent("buzz_content_invalid", nil)
	}
	if len(message.Content) > maxContentBytes {
		return permanent("buzz_content_too_large", nil)
	}
	if message.CreatedAt.IsZero() {
		return permanent("buzz_created_at_invalid", nil)
	}
	return nil
}

func signChecked(ctx context.Context, signer nostr.Signer, event *nostr.Event, code string) (string, error) {
	pubKey, err := signer.GetPublicKey(ctx)
	if err != nil {
		return "", signerOperationError(ctx, code, err)
	}
	if !validPublicKey(pubKey) {
		return "", permanent("buzz_signer_invalid", nil)
	}
	unsigned := snapshotUnsignedEvent(event)
	if err := signer.SignEvent(ctx, event); err != nil {
		return "", signerOperationError(ctx, code, err)
	}
	if !sameUnsignedEvent(event, unsigned) {
		return "", permanent("buzz_signer_mutated", nil)
	}
	after, err := signer.GetPublicKey(ctx)
	if err != nil {
		return "", signerOperationError(ctx, code, err)
	}
	if after != pubKey || event.PubKey != pubKey {
		return "", permanent("buzz_signer_changed", nil)
	}
	valid, err := event.CheckSignature()
	if err != nil || !valid {
		return "", permanent("buzz_signature_invalid", err)
	}
	return pubKey, nil
}

func snapshotUnsignedEvent(event *nostr.Event) nostr.Event {
	snapshot := nostr.Event{
		CreatedAt: event.CreatedAt,
		Kind:      event.Kind,
		Content:   event.Content,
	}
	if event.Tags != nil {
		snapshot.Tags = make(nostr.Tags, len(event.Tags))
		for index, tag := range event.Tags {
			if tag != nil {
				snapshot.Tags[index] = append(nostr.Tag(nil), tag...)
			}
		}
	}
	return snapshot
}

func sameUnsignedEvent(event *nostr.Event, snapshot nostr.Event) bool {
	if event.CreatedAt != snapshot.CreatedAt || event.Kind != snapshot.Kind || event.Content != snapshot.Content ||
		(event.Tags == nil) != (snapshot.Tags == nil) || len(event.Tags) != len(snapshot.Tags) {
		return false
	}
	for index, tag := range event.Tags {
		if (tag == nil) != (snapshot.Tags[index] == nil) || len(tag) != len(snapshot.Tags[index]) {
			return false
		}
		for valueIndex, value := range tag {
			if value != snapshot.Tags[index][valueIndex] {
				return false
			}
		}
	}
	return true
}

func signerOperationError(ctx context.Context, code string, cause error) error {
	if ctx != nil && ctx.Err() != nil {
		cause = ctx.Err()
	}
	switch {
	case errors.Is(cause, context.Canceled):
		return temporary("buzz_canceled", context.Canceled)
	case errors.Is(cause, context.DeadlineExceeded):
		return temporary("buzz_signer_timeout", context.DeadlineExceeded)
	default:
		return temporary(code, cause)
	}
}

func validPublicKey(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && len(value) == 64
}

func publishRejection(reason string) error {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.HasPrefix(reason, "restricted:"), strings.HasPrefix(reason, "blocked:"), strings.HasPrefix(reason, "auth-required:"):
		return permanent("buzz_not_authorized", nil)
	case strings.HasPrefix(reason, "invalid:"):
		return permanent("buzz_event_invalid", nil)
	default:
		return permanent("buzz_rejected", nil)
	}
}

func readError(published bool, err error) error {
	if err != nil && err.Error() == "frame_too_large" {
		return stageError(published, "buzz_frame_too_large")
	}
	return stageError(published, "buzz_protocol_invalid")
}

func stageError(published bool, code string) error {
	if published {
		return ambiguous(code, nil)
	}
	return temporary(code, nil)
}

func contextDeliveryError(published bool, stage string, cause error) error {
	if errors.Is(cause, context.Canceled) {
		if published {
			return ambiguous("buzz_publish_canceled", context.Canceled)
		}
		return temporary("buzz_canceled", context.Canceled)
	}
	if published {
		return ambiguous("buzz_publish_timeout", context.DeadlineExceeded)
	}
	if stage == "auth" {
		return temporary("buzz_auth_timeout", context.DeadlineExceeded)
	}
	return temporary("buzz_connect_timeout", context.DeadlineExceeded)
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
	return "buzz_delivery_failed"
}

func boundedTransportTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 && timeout < transportTimeout {
		return timeout
	}
	return transportTimeout
}

var _ Client = (*client)(nil)
