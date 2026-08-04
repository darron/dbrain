package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRegistryRejectsEmptyAndDuplicateFactoryNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("", func(context.Context, Config) (Provider, bool, error) { return nil, false, nil }); err == nil {
		t.Fatal("empty factory name accepted")
	}
	if err := registry.Register("buzz", func(context.Context, Config) (Provider, bool, error) { return nil, false, nil }); err != nil {
		t.Fatalf("Register buzz: %v", err)
	}
	if err := registry.Register("buzz", func(context.Context, Config) (Provider, bool, error) { return nil, false, nil }); err == nil {
		t.Fatal("duplicate factory name accepted")
	}
}

func TestRegistryRejectsUnknownConfiguredProvider(t *testing.T) {
	_, err := NewRegistry().Build(t.Context(), Config{Enabled: true, Buzz: BuzzConfig{Enabled: true}})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Build error = %v, want unknown provider", err)
	}
}

func TestRegistryRejectsEnabledConfigurationWithoutProvider(t *testing.T) {
	_, err := NewRegistry().Build(t.Context(), Config{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("Build error = %v, want missing provider", err)
	}
}

func TestRegistryRejectsFactoryNameMismatch(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("buzz", func(context.Context, Config) (Provider, bool, error) {
		return registryTestProvider{name: "other"}, true, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Build(t.Context(), Config{Enabled: true, Buzz: BuzzConfig{Enabled: true}})
	if err == nil || !strings.Contains(err.Error(), "different name") {
		t.Fatalf("Build error = %v, want name mismatch", err)
	}
}

func TestRegistryBuildsConfiguredProviders(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("buzz", func(context.Context, Config) (Provider, bool, error) {
		return registryTestProvider{name: "buzz"}, true, nil
	}); err != nil {
		t.Fatal(err)
	}
	providers, err := registry.Build(t.Context(), Config{Enabled: true, Buzz: BuzzConfig{Enabled: true}})
	if err != nil || len(providers) != 1 || providers[0].Name() != "buzz" {
		t.Fatalf("Build = %#v, %v", providers, err)
	}
}

func TestRegistryBuildsBuzzAndSlackInSortedOrder(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"buzz", "slack"} {
		name := name
		if err := registry.Register(name, func(context.Context, Config) (Provider, bool, error) {
			return registryTestProvider{name: name}, true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	providers, err := registry.Build(t.Context(), Config{Enabled: true, Buzz: BuzzConfig{Enabled: true}, Slack: SlackConfig{Enabled: true}})
	if err != nil || len(providers) != 2 || providers[0].Name() != "buzz" || providers[1].Name() != "slack" {
		t.Fatalf("Build = %#v, %v", providers, err)
	}
}

func TestBuiltinRegistryBindsActualSlackFactory(t *testing.T) {
	providers, err := BuiltinRegistry().Build(t.Context(), Config{Enabled: true, Slack: SlackConfig{Enabled: true, WebhookURLRef: "env:DBRAIN_SLACK_WEBHOOK"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].Name() != "slack" {
		t.Fatalf("providers = %#v", providers)
	}
	if _, ok := providers[0].(*slackProvider); !ok {
		t.Fatalf("builtin Slack provider = %T", providers[0])
	}
}

func TestBuildStatusReportsAcceptedBuzzAndSlackAfterTerminalEnvelopePruning(t *testing.T) {
	state := EmptyState()
	acceptedAt := stateAt(time.Hour)
	for _, name := range []string{"buzz", "slack"} {
		state.LastDeliveries[name] = DeliverySummary{
			NotificationID: "ntf_012345678901234567890123",
			Provider:       name,
			Kind:           EventFailure,
			Status:         DeliveryAccepted,
			At:             acceptedAt,
		}
	}
	got, err := BuildStatus(Config{Enabled: true, RepeatAfter: 6 * time.Hour, Buzz: BuzzConfig{Enabled: true}, Slack: SlackConfig{Enabled: true}}, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 2 || got.Providers[0].Name != "buzz" || got.Providers[1].Name != "slack" || !got.Providers[0].LastAcceptedAt.Equal(acceptedAt) || !got.Providers[1].LastAcceptedAt.Equal(acceptedAt) {
		t.Fatalf("providers = %#v", got.Providers)
	}
}

type registryTestProvider struct{ name string }

func (p registryTestProvider) Name() string { return p.name }

func (p registryTestProvider) Deliver(context.Context, Notification) (Receipt, error) {
	return Receipt{}, nil
}
