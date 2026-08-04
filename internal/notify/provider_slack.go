package notify

import (
	"context"
	"errors"

	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/slackclient"
)

type slackSecretResolver func(context.Context, string) (string, error)

type slackProvider struct {
	config  SlackConfig
	resolve slackSecretResolver
	client  slackclient.Client
}

func newSlackProvider(config SlackConfig, resolve slackSecretResolver, client slackclient.Client) *slackProvider {
	if resolve == nil {
		resolve = runtimeenv.ResolveSecretRef
	}
	return &slackProvider{config: config, resolve: resolve, client: client}
}

func newBuiltinSlackProvider(_ context.Context, config Config) (Provider, bool, error) {
	if !config.Slack.Enabled {
		return nil, false, nil
	}
	client, err := slackclient.New(slackclient.Options{})
	if err != nil {
		return nil, false, errors.New("slack_provider_invalid")
	}
	return newSlackProvider(config.Slack, runtimeenv.ResolveSecretRef, client), true, nil
}

func (p *slackProvider) Name() string { return "slack" }

func (p *slackProvider) Deliver(ctx context.Context, notification Notification) (Receipt, error) {
	if err := ValidateNotification(notification); err != nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorPermanent, "slack_notification_invalid", errors.New("slack_notification_invalid"))
	}
	if p == nil || p.resolve == nil || p.client == nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "slack_provider_unavailable", errors.New("slack_provider_unavailable"))
	}
	if err := p.config.validate(); err != nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorPermanent, "slack_webhook_url_ref_invalid", errors.New("slack_webhook_url_ref_invalid"))
	}
	webhookURL, err := p.resolve(ctx, p.config.WebhookURLRef)
	if err != nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "slack_webhook_url_resolve_failed", errors.New("slack_webhook_url_resolve_failed"))
	}
	slackReceipt, err := p.client.SendWebhook(ctx, webhookURL, slackclient.Message{Text: notification.Body})
	if err != nil {
		return Receipt{}, mapSlackDeliveryError(err)
	}
	// Incoming webhooks return no remote message ID; notification.ID is the
	// local correlation key retained in the delivery receipt.
	return Receipt{Provider: "slack", ExternalID: notification.ID, AcceptedAt: slackReceipt.AcceptedAt.UTC()}, nil
}

func mapSlackDeliveryError(err error) error {
	var slackErr *slackclient.DeliveryError
	if !errors.As(err, &slackErr) || !validSafeCode(slackErr.Code, 64) {
		return NewDeliveryError(DeliveryErrorTemporary, "slack_delivery_failed", errors.New("slack_delivery_failed"))
	}
	kind := DeliveryErrorTemporary
	switch slackErr.Kind {
	case slackclient.DeliveryPermanent:
		kind = DeliveryErrorPermanent
	case slackclient.DeliveryAmbiguous:
		kind = DeliveryErrorAmbiguous
	case slackclient.DeliveryTemporary:
	default:
		return NewDeliveryError(DeliveryErrorTemporary, "slack_delivery_failed", errors.New("slack_delivery_failed"))
	}
	return NewDeliveryError(kind, slackErr.Code, errors.New(slackErr.Code))
}

var _ Provider = (*slackProvider)(nil)
