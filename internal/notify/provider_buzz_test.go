package notify

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/buzzclient"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const buzzTestSecretHex = "0000000000000000000000000000000000000000000000000000000000000001"

type buzzClientFunc func(context.Context, nostr.Signer, buzzclient.ChannelMessage) (buzzclient.Receipt, error)

func (f buzzClientFunc) SendChannelMessage(ctx context.Context, signer nostr.Signer, message buzzclient.ChannelMessage) (buzzclient.Receipt, error) {
	return f(ctx, signer, message)
}

func TestBuzzProviderResolvesHexAndNsecOnlyDuringDelivery(t *testing.T) {
	t.Parallel()
	nsec, err := nip19.EncodePrivateKey(buzzTestSecretHex)
	if err != nil {
		t.Fatal(err)
	}
	wantPubKey, err := nostr.GetPublicKey(buzzTestSecretHex)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 3, 18, 30, 0, 0, time.UTC)
	for _, resolved := range []string{buzzTestSecretHex, nsec, "  " + nsec + "\n"} {
		resolved := resolved
		t.Run(strings.TrimSpace(resolved)[:4], func(t *testing.T) {
			var resolverCalls int
			client := buzzClientFunc(func(ctx context.Context, signer nostr.Signer, message buzzclient.ChannelMessage) (buzzclient.Receipt, error) {
				pubKey, err := signer.GetPublicKey(ctx)
				if err != nil || pubKey != wantPubKey {
					t.Fatalf("signer public key = %q, %v", pubKey, err)
				}
				if message.ChannelID != "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2" || message.Content != "dbrain notification delivery test." || message.CreatedAt != createdAt {
					t.Fatalf("message = %#v", message)
				}
				event := nostr.Event{CreatedAt: nostr.Timestamp(createdAt.Unix()), Kind: 9, Content: message.Content}
				if err := signer.SignEvent(ctx, &event); err != nil {
					t.Fatal(err)
				}
				return buzzclient.Receipt{EventID: event.ID, AcceptedAt: createdAt.Add(time.Second)}, nil
			})
			provider := newBuzzProvider(BuzzConfig{Enabled: true, ChannelID: "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2", PrivateKeyRef: "env:BUZZ_KEY"}, func(context.Context, string) (string, error) {
				resolverCalls++
				return resolved, nil
			}, client)
			if resolverCalls != 0 {
				t.Fatal("secret resolved during provider construction")
			}
			receipt, err := provider.Deliver(t.Context(), buzzTestNotification(t, createdAt))
			if err != nil {
				t.Fatal(err)
			}
			if resolverCalls != 1 || receipt.Provider != "buzz" || receipt.ExternalID == "" || receipt.AcceptedAt != createdAt.Add(time.Second) {
				t.Fatalf("resolver calls=%d receipt=%#v", resolverCalls, receipt)
			}
			for _, formatted := range []string{fmt.Sprintf("%v", provider), fmt.Sprintf("%#v", provider), fmt.Sprintf("%v", receipt), fmt.Sprintf("%#v", receipt)} {
				if strings.Contains(formatted, strings.TrimSpace(resolved)) || strings.Contains(formatted, buzzTestSecretHex) {
					t.Fatalf("provider or receipt retained secret: %s", formatted)
				}
			}
		})
	}
}

func TestBuzzProviderRejectsPublicAndMalformedKeysWithSafeError(t *testing.T) {
	t.Parallel()
	pubKey, _ := nostr.GetPublicKey(buzzTestSecretHex)
	npub, _ := nip19.EncodePublicKey(pubKey)
	for _, resolved := range []string{npub, "not-a-key", strings.Repeat("0", 63), strings.Repeat("z", 64), ""} {
		provider := newBuzzProvider(BuzzConfig{ChannelID: "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2", PrivateKeyRef: "env:BUZZ_KEY"}, func(context.Context, string) (string, error) {
			return resolved, nil
		}, buzzClientFunc(func(context.Context, nostr.Signer, buzzclient.ChannelMessage) (buzzclient.Receipt, error) {
			t.Fatal("client called for invalid private key")
			return buzzclient.Receipt{}, nil
		}))
		_, err := provider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
		assertNotifyDeliveryError(t, err, DeliveryErrorPermanent, "buzz_private_key_invalid")
		if strings.Contains(fmt.Sprintf("%#v", err), resolved) && resolved != "" {
			t.Fatalf("error retained invalid key %q: %#v", resolved, err)
		}
	}
}

func TestBuzzProviderRejectsNonCanonicalNotificationBeforeDependencies(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 8, 4, 13, 10, 0, 0, time.UTC)
	forged := buzzTestNotification(t, createdAt)
	forged.Body = "forged raw notification secret-marker"
	tests := []struct {
		name         string
		notification Notification
	}{
		{name: "forged canonical body", notification: forged},
		{name: "raw body only", notification: Notification{Body: "raw arbitrary text secret-marker"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var resolverCalls int
			var clientCalls int
			provider := newBuzzProvider(BuzzConfig{ChannelID: "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2", PrivateKeyRef: "env:BUZZ_KEY"}, func(context.Context, string) (string, error) {
				resolverCalls++
				return buzzTestSecretHex, nil
			}, buzzClientFunc(func(context.Context, nostr.Signer, buzzclient.ChannelMessage) (buzzclient.Receipt, error) {
				clientCalls++
				return buzzclient.Receipt{}, nil
			}))

			_, err := provider.Deliver(t.Context(), test.notification)
			assertNotifyDeliveryError(t, err, DeliveryErrorPermanent, "buzz_notification_invalid")
			if resolverCalls != 0 || clientCalls != 0 {
				t.Fatalf("invalid notification reached dependencies: resolver=%d client=%d", resolverCalls, clientCalls)
			}
			if formatted := fmt.Sprintf("%v\n%+v\n%#v", err, err, err); strings.Contains(formatted, "secret-marker") || strings.Contains(formatted, test.notification.Body) {
				t.Fatalf("invalid-notification error leaked raw body: %s", formatted)
			}
		})
	}
}

func TestBuzzProviderSanitizesSecretResolutionAndClientErrors(t *testing.T) {
	t.Parallel()
	raw, _ := hex.DecodeString(buzzTestSecretHex)
	nsec, _ := nip19.EncodePrivateKey(buzzTestSecretHex)
	secretForms := []string{nsec, buzzTestSecretHex, string(raw), base64.StdEncoding.EncodeToString(raw)}
	leak := strings.Join(secretForms, "|")

	resolutionProvider := newBuzzProvider(BuzzConfig{PrivateKeyRef: "env:BUZZ_KEY"}, func(context.Context, string) (string, error) {
		return "", errors.New(leak)
	}, nil)
	_, resolutionErr := resolutionProvider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
	assertNotifyDeliveryError(t, resolutionErr, DeliveryErrorTemporary, "buzz_private_key_resolve_failed")

	clientProvider := newBuzzProvider(BuzzConfig{ChannelID: "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2", PrivateKeyRef: "env:BUZZ_KEY"}, func(context.Context, string) (string, error) {
		return nsec, nil
	}, buzzClientFunc(func(context.Context, nostr.Signer, buzzclient.ChannelMessage) (buzzclient.Receipt, error) {
		return buzzclient.Receipt{}, buzzclient.NewDeliveryError(buzzclient.DeliveryAmbiguous, "buzz_publish_timeout", errors.New(leak))
	}))
	_, clientErr := clientProvider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
	assertNotifyDeliveryError(t, clientErr, DeliveryErrorAmbiguous, "buzz_publish_timeout")

	for _, value := range []any{resolutionProvider, clientProvider, resolutionErr, clientErr} {
		formatted := fmt.Sprintf("%v\n%+v\n%#v", value, value, value)
		for _, secret := range secretForms {
			if strings.Contains(formatted, secret) {
				t.Fatalf("formatted value retained secret form: %q", formatted)
			}
		}
	}
	var logs []string
	var metricEvents []metrics.Event
	store := newManagerStore(t)
	now := stateAt(0)
	manager := NewManager(Options{RepeatAfter: 6 * time.Hour}, store, []Provider{clientProvider},
		WithClock(func() time.Time { return now }),
		WithManagerLogger(func(message string) { logs = append(logs, message) }),
		WithMetricsEmitter(func(event metrics.Event) error { metricEvents = append(metricEvents, event); return nil }),
	)
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil || err.Error() != "notification_delivery_failed" {
		t.Fatalf("Observe error = %v", err)
	}
	status, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	visibleSurfaces := []string{
		string(stateJSON), fmt.Sprintf("%+v", status), fmt.Sprintf("%v", logs), fmt.Sprintf("%v", metricEvents),
	}
	for _, surface := range visibleSurfaces {
		for _, secret := range secretForms {
			if strings.Contains(surface, secret) {
				t.Fatalf("state, status, log, or metric surface retained secret")
			}
		}
	}
}

func TestBuzzProviderMapsReusableClientDeliveryKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		clientKind buzzclient.DeliveryErrorKind
		notifyKind DeliveryErrorKind
	}{
		{buzzclient.DeliveryTemporary, DeliveryErrorTemporary},
		{buzzclient.DeliveryPermanent, DeliveryErrorPermanent},
		{buzzclient.DeliveryAmbiguous, DeliveryErrorAmbiguous},
	}
	for _, test := range tests {
		provider := newBuzzProvider(BuzzConfig{ChannelID: "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2", PrivateKeyRef: "env:BUZZ_KEY"}, func(context.Context, string) (string, error) {
			return buzzTestSecretHex, nil
		}, buzzClientFunc(func(context.Context, nostr.Signer, buzzclient.ChannelMessage) (buzzclient.Receipt, error) {
			return buzzclient.Receipt{}, buzzclient.NewDeliveryError(test.clientKind, "buzz_safe_code", errors.New("raw relay rejection"))
		}))
		_, err := provider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
		assertNotifyDeliveryError(t, err, test.notifyKind, "buzz_safe_code")
	}
}

func TestBuiltinRegistryBindsActualBuzzFactory(t *testing.T) {
	t.Parallel()
	providers, err := BuiltinRegistry().Build(t.Context(), Config{Enabled: true, Buzz: BuzzConfig{
		Enabled:       true,
		RelayURL:      "wss://relay.getbuzz.app",
		ChannelID:     "9ecb8b70-2e5d-4f17-8a29-7736c2866dc2",
		PrivateKeyRef: "env:DBRAIN_NOTIFICATIONS_BUZZ_PRIVATE_KEY",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].Name() != "buzz" {
		t.Fatalf("providers = %#v", providers)
	}
	if _, ok := providers[0].(*buzzProvider); !ok {
		t.Fatalf("builtin buzz provider = %T, want concrete adapter", providers[0])
	}
}

func buzzTestNotification(t *testing.T, createdAt time.Time) Notification {
	t.Helper()
	notification, err := DeriveNotification(EventFacts{
		Kind:      EventTest,
		Operation: OperationScheduledSyncAll,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return notification
}

func assertNotifyDeliveryError(t *testing.T, err error, kind DeliveryErrorKind, code string) {
	t.Helper()
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("error = %T %v, want notify DeliveryError", err, err)
	}
	if deliveryErr.Kind != kind || deliveryErr.Code != code || deliveryErr.Error() != code {
		t.Fatalf("delivery error = %#v, want kind=%q code=%q", deliveryErr, kind, code)
	}
}
