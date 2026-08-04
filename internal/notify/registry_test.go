package notify

import (
	"context"
	"strings"
	"testing"
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

type registryTestProvider struct{ name string }

func (p registryTestProvider) Name() string { return p.name }

func (p registryTestProvider) Deliver(context.Context, Notification) (Receipt, error) {
	return Receipt{}, nil
}
