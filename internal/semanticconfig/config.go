package semanticconfig

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/runtimeenv"
)

type Mode string
type Provider string
type IndexBackend string

const (
	ModeOff    Mode = "off"
	ModeShadow Mode = "shadow"
	ModeOn     Mode = "on"

	ProviderOllama    Provider     = "ollama"
	IndexBackendExact IndexBackend = "exact"
)

type Config struct {
	Mode                   Mode
	Provider               Provider
	Model                  string
	Dimensions             int
	IndexBackend           IndexBackend
	CandidateDepth         int
	ExactFallbackMaxChunks int
	OllamaBaseURL          string
}

func Resolve(rootDir string) (Config, error) {
	config := Config{
		Mode:                   ModeOff,
		Provider:               ProviderOllama,
		IndexBackend:           IndexBackendExact,
		CandidateDepth:         50,
		ExactFallbackMaxChunks: 25000,
	}

	if value := semanticValue(rootDir, "MODE"); value != "" {
		config.Mode = Mode(strings.ToLower(value))
	}
	if value := semanticValue(rootDir, "PROVIDER"); value != "" {
		config.Provider = Provider(strings.ToLower(value))
	}
	config.Model = semanticValue(rootDir, "MODEL")
	if err := resolvePositiveOrZeroInt(rootDir, "DIMENSIONS", &config.Dimensions); err != nil {
		return Config{}, err
	}
	if value := semanticValue(rootDir, "INDEX_BACKEND"); value != "" {
		config.IndexBackend = IndexBackend(strings.ToLower(value))
	}
	if err := resolveDefaultedInt(rootDir, "CANDIDATE_DEPTH", &config.CandidateDepth); err != nil {
		return Config{}, err
	}
	if err := resolveDefaultedInt(rootDir, "EXACT_FALLBACK_MAX_CHUNKS", &config.ExactFallbackMaxChunks); err != nil {
		return Config{}, err
	}

	baseURL, err := llmprovider.ResolveBaseURL(llmprovider.BaseURLResolveOptions{
		RootDir: rootDir, Provider: llmprovider.ProviderOllama,
	})
	if err != nil {
		return Config{}, fmt.Errorf("resolve Ollama base URL: %w", err)
	}
	validatedBaseURL, err := validateOllamaBaseURL(baseURL)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Ollama base URL: %w", err)
	}
	config.OllamaBaseURL = validatedBaseURL

	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	switch c.Mode {
	case ModeOff, ModeShadow, ModeOn:
	default:
		return fmt.Errorf("research.semantic.mode must be off, shadow, or on, got %q", c.Mode)
	}
	if c.Provider != ProviderOllama {
		return fmt.Errorf("research.semantic.provider %q is unsupported; only local Ollama embeddings are supported", c.Provider)
	}
	if c.IndexBackend != IndexBackendExact {
		return fmt.Errorf("research.semantic.index_backend must be exact, got %q", c.IndexBackend)
	}
	if c.Dimensions < 0 {
		return fmt.Errorf("research.semantic.dimensions must not be negative")
	}
	if c.CandidateDepth <= 0 {
		return fmt.Errorf("research.semantic.candidate_depth must be positive")
	}
	if c.ExactFallbackMaxChunks <= 0 {
		return fmt.Errorf("research.semantic.exact_fallback_max_chunks must be positive")
	}
	if c.Mode != ModeOff {
		if strings.TrimSpace(c.Model) == "" {
			return fmt.Errorf("research.semantic.model is required when mode is %s", c.Mode)
		}
		if c.Dimensions <= 0 {
			return fmt.Errorf("research.semantic.dimensions must be positive when mode is %s", c.Mode)
		}
	}
	if strings.TrimSpace(c.OllamaBaseURL) == "" {
		return fmt.Errorf("ollama base URL is required")
	}
	return nil
}

func semanticValue(rootDir, suffix string) string {
	return strings.TrimSpace(runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_RESEARCH_SEMANTIC_"+suffix))
}

func resolvePositiveOrZeroInt(rootDir, suffix string, destination *int) error {
	value := semanticValue(rootDir, suffix)
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("research.semantic.%s must be an integer: %w", strings.ToLower(suffix), err)
	}
	*destination = parsed
	return nil
}

func resolveDefaultedInt(rootDir, suffix string, destination *int) error {
	return resolvePositiveOrZeroInt(rootDir, suffix, destination)
}

func validateOllamaBaseURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Ollama HTTP endpoint %q", value)
	}
	return parsed.String(), nil
}
