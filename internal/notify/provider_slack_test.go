package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/slackclient"
)

type slackClientFunc func(context.Context, string, slackclient.Message) (slackclient.Receipt, error)

func (f slackClientFunc) SendWebhook(ctx context.Context, url string, message slackclient.Message) (slackclient.Receipt, error) {
	return f(ctx, url, message)
}

func TestSlackProviderResolvesTypedReferenceOnlyDuringDeliveryAndUsesNotificationIDCorrelation(t *testing.T) {
	createdAt := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	notification := buzzTestNotification(t, createdAt)
	var resolverCalls int
	provider := newSlackProvider(SlackConfig{Enabled: true, WebhookURLRef: "env:DBRAIN_SLACK_WEBHOOK"}, func(context.Context, string) (string, error) {
		resolverCalls++
		return "https://hooks.slack.com/services/T000/B000/secret", nil
	}, slackClientFunc(func(_ context.Context, webhookURL string, message slackclient.Message) (slackclient.Receipt, error) {
		if webhookURL != "https://hooks.slack.com/services/T000/B000/secret" || message.Text != notification.Body {
			t.Fatalf("webhook=%q message=%#v", webhookURL, message)
		}
		return slackclient.Receipt{AcceptedAt: createdAt.Add(time.Second)}, nil
	}))
	if resolverCalls != 0 {
		t.Fatal("secret resolved during construction")
	}
	receipt, err := provider.Deliver(t.Context(), notification)
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 || receipt.Provider != "slack" || receipt.ExternalID != notification.ID || !receipt.AcceptedAt.Equal(createdAt.Add(time.Second)) {
		t.Fatalf("resolver=%d receipt=%#v", resolverCalls, receipt)
	}
}

func TestSlackProviderRejectsNonCanonicalNotificationBeforeDependencies(t *testing.T) {
	notification := buzzTestNotification(t, time.Now().UTC())
	notification.Body = "forged secret-marker"
	var resolverCalls, clientCalls int
	provider := newSlackProvider(SlackConfig{WebhookURLRef: "env:DBRAIN_SLACK_WEBHOOK"}, func(context.Context, string) (string, error) {
		resolverCalls++
		return "https://hooks.slack.com/services/T000/B000/secret", nil
	}, slackClientFunc(func(context.Context, string, slackclient.Message) (slackclient.Receipt, error) {
		clientCalls++
		return slackclient.Receipt{}, nil
	}))
	_, err := provider.Deliver(t.Context(), notification)
	assertNotifyDeliveryError(t, err, DeliveryErrorPermanent, "slack_notification_invalid")
	if resolverCalls != 0 || clientCalls != 0 || strings.Contains(fmt.Sprintf("%#v", err), "secret-marker") {
		t.Fatalf("resolver=%d client=%d error=%#v", resolverCalls, clientCalls, err)
	}
}

func TestSlackProviderRedactsWebhookAndMapsOnlyTypedClientErrors(t *testing.T) {
	const rawWebhook = "https://hooks.slack.com/services/T000/B000/secret"
	provider := newSlackProvider(SlackConfig{WebhookURLRef: "env:DBRAIN_SLACK_WEBHOOK"}, func(context.Context, string) (string, error) {
		return rawWebhook, nil
	}, slackClientFunc(func(context.Context, string, slackclient.Message) (slackclient.Receipt, error) {
		return slackclient.Receipt{}, slackclient.NewDeliveryError(slackclient.DeliveryAmbiguous, "slack_delivery_failed", errors.New(rawWebhook+" raw response"))
	}))
	_, err := provider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
	assertNotifyDeliveryError(t, err, DeliveryErrorAmbiguous, "slack_delivery_failed")
	if strings.Contains(fmt.Sprintf("%+v %#v", provider, err), rawWebhook) {
		t.Fatalf("webhook leaked: %#v", err)
	}

	provider = newSlackProvider(SlackConfig{WebhookURLRef: "env:DBRAIN_SLACK_WEBHOOK"}, func(context.Context, string) (string, error) { return rawWebhook, nil }, slackClientFunc(func(context.Context, string, slackclient.Message) (slackclient.Receipt, error) {
		return slackclient.Receipt{}, errors.New("raw response body")
	}))
	_, err = provider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
	assertNotifyDeliveryError(t, err, DeliveryErrorTemporary, "slack_delivery_failed")
	if strings.Contains(fmt.Sprintf("%+v %#v", provider, err), "raw response body") || strings.Contains(fmt.Sprintf("%+v %#v", provider, err), rawWebhook) {
		t.Fatalf("untyped error leaked: %#v", err)
	}

	provider = newSlackProvider(SlackConfig{WebhookURLRef: "env:DBRAIN_SLACK_WEBHOOK"}, func(context.Context, string) (string, error) {
		return "", errors.New(rawWebhook + " resolver response")
	}, slackClientFunc(func(context.Context, string, slackclient.Message) (slackclient.Receipt, error) {
		t.Fatal("client called after resolver failure")
		return slackclient.Receipt{}, nil
	}))
	_, err = provider.Deliver(t.Context(), buzzTestNotification(t, time.Now().UTC()))
	assertNotifyDeliveryError(t, err, DeliveryErrorTemporary, "slack_webhook_url_resolve_failed")
	if strings.Contains(fmt.Sprintf("%+v %#v", provider, err), rawWebhook) {
		t.Fatalf("resolver error leaked: %#v", err)
	}
}
