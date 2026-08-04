package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Factory func(context.Context, Config) (Provider, bool, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() Registry {
	return Registry{factories: make(map[string]Factory)}
}

func BuiltinRegistry() Registry {
	registry := NewRegistry()
	if err := registry.Register("buzz", newBuiltinBuzzProvider); err != nil {
		panic(err)
	}
	if err := registry.Register("slack", newBuiltinSlackProvider); err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Register(name string, factory Factory) error {
	name = strings.TrimSpace(name)
	if name == "" || factory == nil {
		return fmt.Errorf("notification provider factory is invalid")
	}
	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("duplicate notification provider factory %q", name)
	}
	r.factories[name] = factory
	return nil
}

func (r Registry) Build(ctx context.Context, config Config) ([]Provider, error) {
	if !config.Enabled {
		return nil, nil
	}
	names := configuredProviderNames(config)
	if len(names) == 0 {
		return nil, fmt.Errorf("notification configuration has no enabled provider")
	}
	providers := make([]Provider, 0, len(names))
	for _, name := range names {
		factory, ok := r.factories[name]
		if !ok {
			return nil, fmt.Errorf("unknown configured notification provider %q", name)
		}
		provider, configured, err := factory(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("build notification provider %q: %w", name, err)
		}
		if !configured {
			return nil, fmt.Errorf("configured notification provider %q was not built", name)
		}
		if provider == nil || provider.Name() != name {
			return nil, fmt.Errorf("notification provider factory %q returned a provider with a different name", name)
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func configuredProviderNames(config Config) []string {
	names := make([]string, 0, 2)
	if config.Buzz.Enabled {
		names = append(names, "buzz")
	}
	if config.Slack.Enabled {
		names = append(names, "slack")
	}
	sort.Strings(names)
	return names
}
