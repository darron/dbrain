package notify

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/darron/dbrain/internal/buzzclient"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/keyer"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type buzzSecretResolver func(context.Context, string) (string, error)

type buzzProvider struct {
	config  BuzzConfig
	resolve buzzSecretResolver
	client  buzzclient.Client
}

func newBuzzProvider(config BuzzConfig, resolve buzzSecretResolver, client buzzclient.Client) *buzzProvider {
	if resolve == nil {
		resolve = runtimeenv.ResolveSecretRef
	}
	return &buzzProvider{config: config, resolve: resolve, client: client}
}

func newBuiltinBuzzProvider(_ context.Context, config Config) (Provider, bool, error) {
	if !config.Buzz.Enabled {
		return nil, false, nil
	}
	client, err := buzzclient.New(buzzclient.Options{
		RelayURL:           config.Buzz.RelayURL,
		AllowPrivateOrigin: config.Buzz.AllowPrivateOrigin,
	})
	if err != nil {
		return nil, false, errors.New("buzz_provider_invalid")
	}
	return newBuzzProvider(config.Buzz, runtimeenv.ResolveSecretRef, client), true, nil
}

func (p *buzzProvider) Name() string { return "buzz" }

func (p *buzzProvider) Deliver(ctx context.Context, notification Notification) (Receipt, error) {
	if p == nil || p.resolve == nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_provider_unavailable", errors.New("buzz_provider_unavailable"))
	}
	resolved, err := p.resolve(ctx, p.config.PrivateKeyRef)
	if err != nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_private_key_resolve_failed", errors.New("buzz_private_key_resolve_failed"))
	}
	signer, err := decodeBuzzSigner(resolved)
	if err != nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorPermanent, "buzz_private_key_invalid", errors.New("buzz_private_key_invalid"))
	}
	if p.client == nil {
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_provider_unavailable", errors.New("buzz_provider_unavailable"))
	}
	buzzReceipt, err := p.client.SendChannelMessage(ctx, signer, buzzclient.ChannelMessage{
		ChannelID: p.config.ChannelID,
		Content:   notification.Body,
		CreatedAt: notification.CreatedAt,
	})
	if err != nil {
		return Receipt{}, mapBuzzDeliveryError(err)
	}
	return Receipt{Provider: "buzz", ExternalID: buzzReceipt.EventID, AcceptedAt: buzzReceipt.AcceptedAt.UTC()}, nil
}

func decodeBuzzSigner(raw string) (nostr.Signer, error) {
	secret := strings.TrimSpace(raw)
	if len(secret) == 64 {
		decoded, err := hex.DecodeString(secret)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("invalid private key")
		}
		for index := range decoded {
			decoded[index] = 0
		}
		return plainBuzzSigner(secret)
	}
	prefix, value, err := nip19.Decode(secret)
	if err != nil || prefix != "nsec" {
		return nil, errors.New("invalid private key")
	}
	hexSecret, ok := value.(string)
	if !ok || len(hexSecret) != 64 {
		return nil, errors.New("invalid private key")
	}
	return plainBuzzSigner(hexSecret)
}

func plainBuzzSigner(secret string) (nostr.Signer, error) {
	signer, err := keyer.NewPlainKeySigner(secret)
	if err != nil {
		return nil, errors.New("invalid private key")
	}
	return signer, nil
}

func mapBuzzDeliveryError(err error) error {
	var buzzErr *buzzclient.DeliveryError
	if !errors.As(err, &buzzErr) || !validSafeCode(buzzErr.Code, 64) {
		return NewDeliveryError(DeliveryErrorTemporary, "buzz_delivery_failed", errors.New("buzz_delivery_failed"))
	}
	kind := DeliveryErrorTemporary
	switch buzzErr.Kind {
	case buzzclient.DeliveryPermanent:
		kind = DeliveryErrorPermanent
	case buzzclient.DeliveryAmbiguous:
		kind = DeliveryErrorAmbiguous
	case buzzclient.DeliveryTemporary:
	default:
		return NewDeliveryError(DeliveryErrorTemporary, "buzz_delivery_failed", errors.New("buzz_delivery_failed"))
	}
	return NewDeliveryError(kind, buzzErr.Code, errors.New(buzzErr.Code))
}

var _ Provider = (*buzzProvider)(nil)
