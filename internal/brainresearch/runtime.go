package brainresearch

import (
	"fmt"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

func NewRuntimeBuilder(cfg config.Config, st *store.Store, configuredOverride semanticconfig.Mode, forceOn, forceOff bool) (*Builder, error) {
	if _, err := semanticconfig.EffectiveMode(semanticconfig.ModeOff, forceOn, forceOff); err != nil {
		return nil, err
	}
	configured, err := semanticconfig.ResolveDiagnostic(cfg.RootDir)
	if err != nil {
		return nil, err
	}
	configuredMode := configured.Mode
	if configuredOverride != "" {
		configuredMode = configuredOverride
	}
	mode, err := semanticconfig.EffectiveMode(configuredMode, forceOn, forceOff)
	if err != nil {
		return nil, err
	}
	b := New(cfg, st).WithSemanticMode(mode)
	if mode == semanticconfig.ModeOff {
		return b, nil
	}
	ready := configured
	ready.Mode = mode
	if err := ready.Validate(); err != nil {
		return nil, err
	}
	provider, err := embedding.NewOllama(embedding.OllamaOptions{
		BaseURL: ready.OllamaBaseURL, Model: ready.Model, Dimensions: ready.Dimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("construct semantic embedding provider: %w", err)
	}
	profile := semanticbuild.Profile(provider.Info())
	retriever := researchsemantic.New(provider, semanticindex.NewExact(st), st)
	return b.WithSemanticRetriever(retriever, researchsemantic.Options{
		Profile: profile, Limit: ready.CandidateDepth, MaxChunks: ready.ExactFallbackMaxChunks,
	}), nil
}
