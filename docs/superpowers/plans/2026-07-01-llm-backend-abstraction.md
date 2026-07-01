# LLM Backend Abstraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generalize the current Ollama/OpenRouter/LM Studio model paths into a first-class backend registry and shared chat client so `omlx/<model>` and configured OpenAI-compatible aliases work without breaking existing hosted OpenRouter/Gemini OCR or external `summarize` CLI behavior.

**Architecture:** Keep task packages in charge of prompts, parsing, persistence, and product semantics. Move provider parsing, provider metadata, runtime target resolution, auth/header policy, transport serialization, response extraction, and provenance into `internal/llmprovider` plus a small `internal/llmclient` package. Research and MCP inherit backend support through `summarizecli`; OCR remains a compatibility-protected separate path in this pass.

**Tech Stack:** Go, `net/http`, `httptest`, existing `runtimeenv` YAML/env/secret resolution, Ollama native `/api/chat`, OpenAI-compatible `/chat/completions`, current source summary/categorization/research/bakeoff packages, `task` gates.

---

## Source Spec

Implement against:

- `docs/superpowers/specs/2026-07-01-llm-backend-abstraction-design.md`
- Existing LM Studio branch state in `docs/superpowers/plans/2026-06-30-lmstudio-provider.md`

Compatibility is a hard requirement:

- OpenRouter and other hosted OpenAI-compatible endpoints remain first-class.
- OpenRouter/Gemini OCR must work without Ollama, LM Studio, oMLX, or any local server.
- Existing unqualified summary models continue through the external `summarize` CLI path.
- Existing unqualified categorization models continue to mean "plain OpenRouter model id".
- Tesseract remains an OCR fallback, not an LLM backend.
- Configured aliases are root/config-aware. Any path that needs to recognize
  `llm_backends.<alias>` must use `RegistryForRoot(rootDir)`, not the
  package-level built-in-only `ParseModelRef` helper.

## File Structure

- Modify `internal/runtimeenv/config.go`: export a narrow config-section reader for `llm_backends`.
- Test `internal/runtimeenv/runtimeenv_test.go`: prove nested `llm_backends` maps can be read without exposing secret values unnecessarily.
- Modify `internal/llmprovider/provider.go`: replace hard-coded provider iteration with registry-aware parsing, provider specs, transports, capabilities, task/header policy, and built-in provider specs.
- Modify `internal/llmprovider/params.go`: rename request accounting away from bakeoff-only `ParityParams`, keep compatibility aliases while moving policy behind provider specs.
- Create `internal/llmprovider/registry.go`: registry construction, built-in registration, configured OpenAI-compatible aliases, and provider lookup.
- Create `internal/llmprovider/resolve.go`: runtime target resolution from root/env/overrides, URL normalization, secret loading, headers, auth behavior, and selected-provider-only secret access.
- Test `internal/llmprovider/provider_test.go`: registry parser, built-ins, aliases, empty prefixes, and duplicate alias rejection.
- Test `internal/llmprovider/params_test.go`: parameter accounting for Ollama, LM Studio, oMLX, OpenRouter, and aliases.
- Test `internal/llmprovider/resolve_test.go`: target resolution, OpenRouter headers, optional auth omission, configured alias secrets, and unrelated-secret laziness.
- Create `internal/llmclient/types.go`: task-neutral chat request/response/message/content types.
- Create `internal/llmclient/client.go`: public chat call entry point, HTTP client injection, timeout behavior, capability enforcement, provenance response.
- Create `internal/llmclient/openai.go`: OpenAI-compatible chat completions serialization/response parsing.
- Create `internal/llmclient/ollama.go`: Ollama native chat serialization/response parsing.
- Create `internal/llmclient/params.go`: apply/send/omit sampler params based on provider policy.
- Test `internal/llmclient/client_test.go`: mocked Ollama, OpenRouter, LM Studio, oMLX, configured alias, auth headers, unsupported image handling, and malformed responses.
- Modify `internal/summarizecli/types.go`: remove direct transport request structs once `llmclient` owns them; keep public `Options`.
- Modify `internal/summarizecli/provider.go`: delegate direct-provider parsing/display to `llmprovider`.
- Modify `internal/summarizecli/env.go`: keep external CLI env rewrite, but stop duplicating direct-provider resolution.
- Modify `internal/summarizecli/direct_target.go`: either delete or reduce to a compatibility shim around `llmprovider.ResolveTarget`.
- Modify `internal/summarizecli/direct.go`: call `llmclient` for provider-qualified direct summaries.
- Modify `internal/summarizecli/direct_response.go`: delete if unused after `llmclient` migration.
- Test `internal/summarizecli/client_test.go`: preserve existing direct summary tests, add oMLX and alias summary tests, and add plain-model external CLI regression tests.
- Modify `internal/itemcategorize/types.go`: remove provider request/response wire structs once `llmclient` owns them; keep public options for compatibility; add `RootDir` so direct helper calls and batch runs can resolve configured aliases.
- Modify `internal/itemcategorize/options.go`: translate existing option fields into resolver overrides.
- Modify `internal/itemcategorize/llm.go`: build task messages/content parts and call `llmclient`.
- Modify `internal/itemcategorize/llm_http.go`: delete if no longer used.
- Test `internal/itemcategorize/run_test.go`: preserve OpenRouter/Ollama/LM Studio behavior, add oMLX/alias text tests, image capability tests, and plain OpenRouter fallback tests.
- Modify `internal/modelbakeoff/types.go`: add `Transport` and local/hosted metadata to `ModelRun`; keep schema `model_bakeoff.v2` because `main` still has `model_bakeoff.v1`.
- Modify `internal/modelbakeoff/run.go`: derive provider metadata from registry, not provider switches.
- Modify `internal/modelbakeoff/report.go`: render transport and local/hosted metadata.
- Test `internal/modelbakeoff/run_test.go` and `internal/modelbakeoff/report_test.go`: verify oMLX and alias metadata.
- Test `internal/brainresearch` or `internal/summarizecli`: prove research-owned planner/synthesis calls can use `omlx/<model>` through `summarizecli`.
- Test `internal/mcpserver`: prove `planner_model: "omlx/<model>"` passes through the research pack without MCP owning provider logic.
- Test `internal/xphotoocr/run_test.go`: preserve OpenRouter/Gemini hosted OCR with no local backend configured.
- Modify `internal/app/env_docs.go`: document oMLX and configured backend aliases.
- Modify `config.yaml.sample`: add `omlx` and `llm_backends` examples.
- Modify `README.md`, `COMMANDS.md`, `skills/dbrain-model-bakeoff/SKILL.md`: backend usage and bakeoff examples.
- Modify `CHANGELOG.md`: user-visible backend abstraction, oMLX, configured aliases, and compatibility note.

## Task 1: Registry, Provider Specs, And Configured Aliases

**Files:**
- Modify: `internal/runtimeenv/config.go`
- Modify: `internal/runtimeenv/runtimeenv_test.go`
- Modify: `internal/llmprovider/provider.go`
- Modify: `internal/llmprovider/params.go`
- Create: `internal/llmprovider/registry.go`
- Create: `internal/llmprovider/resolve.go`
- Test: `internal/llmprovider/provider_test.go`
- Test: `internal/llmprovider/params_test.go`
- Test: `internal/llmprovider/resolve_test.go`

- [ ] **Step 1: Add runtimeenv config-section tests**

Append tests to `internal/runtimeenv/runtimeenv_test.go`:

```go
func TestConfigMapReadsLLMBackends(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: env:LOCALAI_API_KEY
    local: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok := ConfigMap(root, "llm_backends")
	if !ok {
		t.Fatal("expected llm_backends config map")
	}
	localai, ok := got["localai"].(map[string]any)
	if !ok {
		t.Fatalf("localai map missing: %#v", got["localai"])
	}
	if localai["transport"] != "openai_chat_completions" {
		t.Fatalf("transport = %#v", localai["transport"])
	}
	if localai["api_key"] != "env:LOCALAI_API_KEY" {
		t.Fatalf("api_key ref = %#v", localai["api_key"])
	}
}

func TestConfigMapMissingPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`summary: {model: ""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := ConfigMap(root, "llm_backends"); ok || got != nil {
		t.Fatalf("ConfigMap missing path = %#v, %v", got, ok)
	}
}
```

- [ ] **Step 2: Run runtimeenv tests and verify the new API is missing**

Run:

```sh
go test ./internal/runtimeenv
```

Expected: FAIL with `undefined: ConfigMap`.

- [ ] **Step 3: Export a narrow config map reader**

Add to `internal/runtimeenv/config.go`:

```go
// ConfigMap returns a shallow copy of a nested YAML map from the runtime config.
// It is intentionally narrow: callers can inspect explicit config sections
// without gaining access to unparsed environment files or secret resolution.
func ConfigMap(rootDir string, path ...string) (map[string]any, bool) {
	if strings.TrimSpace(rootDir) == "" || len(path) == 0 {
		return nil, false
	}
	cfg, ok := loadConfigForRoot(rootDir)
	if !ok {
		return nil, false
	}
	var current any = cfg
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := lookupMapValue(m, part)
		if !ok {
			return nil, false
		}
		current = next
	}
	m, ok := current.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out, true
}
```

- [ ] **Step 4: Replace provider constants with registry-aware types and built-ins**

Refactor `internal/llmprovider/provider.go` so it defines these stable types and constants:

```go
type Provider string

const (
	ProviderPlain      Provider = ""
	ProviderOllama     Provider = "ollama"
	ProviderOpenRouter Provider = "openrouter"
	ProviderLMStudio   Provider = "lmstudio"
	ProviderOMLX       Provider = "omlx"
)

type Transport string

const (
	TransportOllamaChat         Transport = "ollama_chat"
	TransportOpenAIChat         Transport = "openai_chat_completions"
	TransportAnthropicMessages  Transport = "anthropic_messages"
)

type CapabilityStatus string

const (
	CapabilitySupported                 CapabilityStatus = "supported"
	CapabilityUnsupported               CapabilityStatus = "unsupported"
	CapabilityModelDependentOrUnverified CapabilityStatus = "model_dependent_or_unverified"
)

type Task string

const (
	TaskSummary    Task = "summary"
	TaskCategorize Task = "categorize"
	TaskOCR        Task = "ocr"
	TaskBakeoff    Task = "bakeoff"
)

type Capabilities struct {
	Text         bool
	Images       CapabilityStatus
	JSONMode     CapabilityStatus
	ToolCalling  CapabilityStatus
	ReasoningCtl CapabilityStatus
}

type HeaderPolicy struct {
	RefererEnvKeys []string
	TitleEnvKeys   []string
	UserAgentEnvKeys []string
	DefaultRefererByTask map[Task]string
	DefaultTitleByTask   map[Task]string
}

type ParamPolicy struct {
	LocalModelfileParity bool
	OpenAICompatibleLocal bool
}

type PromptPolicy struct {
	ParityStatusWithPreset string
}

type ReasoningPolicy struct {
	StatusWithDirectCall string
}

type ProviderSpec struct {
	ID             Provider
	DisplayName    string
	Transport      Transport
	Local          bool
	DefaultBaseURL string
	DefaultAPIKey  string
	BaseURLEnvKeys []string
	APIKeyEnvKeys  []string
	RequiresAPIKey bool
	HeaderPolicy   HeaderPolicy
	ToolName       string
	ToolVersion    string
	ParamPolicy    ParamPolicy
	PromptPolicy   PromptPolicy
	ReasoningPolicy ReasoningPolicy
	Capabilities   Capabilities
	ConfigPath      []string
}

type ModelRef struct {
	Original          string
	Provider          Provider
	APIModel          string
	ProviderQualified bool
	Spec              *ProviderSpec
}
```

Use exact direct tool identities:

```go
const (
	ToolOllamaDirect     = "ollama-direct"
	ToolOpenRouterDirect = "openrouter-direct"
	ToolLMStudioDirect   = "lmstudio-direct"
	ToolOMLXDirect       = "omlx-direct"

	ToolVersionOllamaDirect     = "ollama-direct-v1"
	ToolVersionOpenRouterDirect = "openrouter-direct-v1"
	ToolVersionLMStudioDirect   = "lmstudio-direct-v1"
	ToolVersionOMLXDirect       = "omlx-direct-v1"
)
```

- [ ] **Step 5: Add registry tests before implementation**

Replace or extend `internal/llmprovider/provider_test.go` with tests covering built-ins and aliases:

```go
func TestDefaultRegistryParsesBuiltInProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model     string
		provider  Provider
		apiModel  string
		transport Transport
		local     bool
	}{
		{"ollama/qwen3.6:35b", ProviderOllama, "qwen3.6:35b", TransportOllamaChat, true},
		{"openrouter/google/gemini-2.5-flash", ProviderOpenRouter, "google/gemini-2.5-flash", TransportOpenAIChat, false},
		{"lmstudio/qwen/qwen3.6-35b-a3b", ProviderLMStudio, "qwen/qwen3.6-35b-a3b", TransportOpenAIChat, true},
		{"omlx/qwen3.5-coder", ProviderOMLX, "qwen3.5-coder", TransportOpenAIChat, true},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			ref := ParseModelRef(tt.model)
			if !ref.ProviderQualified || ref.Provider != tt.provider || ref.APIModel != tt.apiModel {
				t.Fatalf("ParseModelRef(%q) = %+v", tt.model, ref)
			}
			if ref.Spec == nil {
				t.Fatalf("missing provider spec for %q", tt.model)
			}
			if ref.Spec.Transport != tt.transport || ref.Spec.Local != tt.local {
				t.Fatalf("spec = %+v", ref.Spec)
			}
		})
	}
}

func TestRegistryParsesConfiguredOpenAICompatibleAlias(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.Register(ProviderSpec{
		ID:             Provider("localai"),
		DisplayName:    "localai",
		Transport:      TransportOpenAIChat,
		Local:          true,
		DefaultBaseURL: "http://127.0.0.1:8080/v1",
		Capabilities:  TextOnlyCapabilities(),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ref := reg.ParseModelRef("localai/meta-llama")
	if !ref.ProviderQualified || ref.Provider != Provider("localai") || ref.APIModel != "meta-llama" {
		t.Fatalf("alias ref = %+v", ref)
	}
	if ref.Spec == nil || ref.Spec.Transport != TransportOpenAIChat || !ref.Spec.Local {
		t.Fatalf("alias spec = %+v", ref.Spec)
	}
}

func TestRegistryRejectsConfiguredAliasOverBuiltIn(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	err := reg.Register(ProviderSpec{ID: ProviderOllama, Transport: TransportOpenAIChat})
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate provider error, got %v", err)
	}
}

func TestEmptyProviderRefIncludesOMLXAndAliases(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.Register(ProviderSpec{ID: Provider("localai"), Transport: TransportOpenAIChat}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"omlx/", "omlx:", "localai/", "localai:"} {
		provider, ok := reg.EmptyProviderRef(model)
		if !ok {
			t.Fatalf("expected empty provider for %q", model)
		}
		if provider != Provider(strings.TrimRight(model, "/:")) {
			t.Fatalf("provider for %q = %q", model, provider)
		}
	}
}
```

- [ ] **Step 6: Implement registry construction and parser delegation**

Create `internal/llmprovider/registry.go`:

```go
type Registry struct {
	specs map[Provider]ProviderSpec
	order []Provider
}

func NewRegistry() Registry {
	reg := Registry{specs: map[Provider]ProviderSpec{}}
	for _, spec := range BuiltInProviderSpecs() {
		if err := reg.Register(spec); err != nil {
			panic(err)
		}
	}
	return reg
}

func (r *Registry) Register(spec ProviderSpec) error {
	spec.ID = Provider(strings.ToLower(strings.TrimSpace(string(spec.ID))))
	if spec.ID == "" {
		return fmt.Errorf("provider id is required")
	}
	if _, exists := r.specs[spec.ID]; exists {
		return fmt.Errorf("provider %q already registered", spec.ID)
	}
	if spec.DisplayName == "" {
		spec.DisplayName = string(spec.ID)
	}
	if spec.Capabilities == (Capabilities{}) {
		spec.Capabilities = TextOnlyCapabilities()
	}
	r.specs[spec.ID] = spec
	r.order = append(r.order, spec.ID)
	return nil
}

func (r Registry) Spec(provider Provider) (ProviderSpec, bool) {
	spec, ok := r.specs[provider]
	return spec, ok
}

func ParseModelRef(model string) ModelRef {
	return defaultRegistry().ParseModelRef(model)
}

func EmptyProviderRef(model string) (Provider, bool) {
	return defaultRegistry().EmptyProviderRef(model)
}

var defaultRegistryOnce sync.Once
var defaultRegistryValue Registry

func defaultRegistry() Registry {
	defaultRegistryOnce.Do(func() {
		defaultRegistryValue = NewRegistry()
	})
	return defaultRegistryValue
}
```

Keep parsing behavior in `Registry.ParseModelRef` and `Registry.EmptyProviderRef` equivalent to the current `ParseModelRef`/`EmptyProviderRef`, but iterate `r.order` instead of a hard-coded provider slice.
Retain package-level `ParseModelRef(model string)` and
`EmptyProviderRef(model string)` free functions as thin wrappers over the
cached default registry. Current callers in `internal/summarizecli/provider.go`,
`internal/itemcategorize/llm.go`, and `internal/modelbakeoff/run.go` must keep
compiling while root-aware call sites move to `RegistryForRoot(rootDir)`.

- [ ] **Step 7: Add built-in specs**

In `internal/llmprovider/provider.go` or `registry.go`, add:

```go
func BuiltInProviderSpecs() []ProviderSpec {
	return []ProviderSpec{
		{
			ID: ProviderOllama, DisplayName: "Ollama", Transport: TransportOllamaChat, Local: true,
			DefaultBaseURL: "http://127.0.0.1:11434",
			DefaultAPIKey: "ollama",
			BaseURLEnvKeys: []string{"DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"},
			APIKeyEnvKeys: []string{"DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"},
			ToolName: ToolOllamaDirect, ToolVersion: ToolVersionOllamaDirect,
			ParamPolicy: ParamPolicy{LocalModelfileParity: true},
			PromptPolicy: PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{StatusWithDirectCall: "think-disabled"},
			Capabilities: Capabilities{Text: true, Images: CapabilityModelDependentOrUnverified, JSONMode: CapabilityModelDependentOrUnverified, ToolCalling: CapabilityModelDependentOrUnverified, ReasoningCtl: CapabilitySupported},
			ConfigPath: []string{"ollama"},
		},
		{
			ID: ProviderOpenRouter, DisplayName: "OpenRouter", Transport: TransportOpenAIChat, Local: false,
			DefaultBaseURL: "https://openrouter.ai/api/v1",
			BaseURLEnvKeys: []string{"DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"},
			APIKeyEnvKeys: []string{"DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY"},
			RequiresAPIKey: true,
			HeaderPolicy: HeaderPolicy{
				RefererEnvKeys: []string{"DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"},
				TitleEnvKeys: []string{"DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"},
				UserAgentEnvKeys: []string{"DBRAIN_USER_AGENT"},
				DefaultRefererByTask: map[Task]string{TaskCategorize: "https://local.dbrain", TaskOCR: "https://local.dbrain"},
				DefaultTitleByTask: map[Task]string{TaskCategorize: "dbrain categorize", TaskOCR: "dbrain X photo OCR"},
			},
			ToolName: ToolOpenRouterDirect, ToolVersion: ToolVersionOpenRouterDirect,
			Capabilities: Capabilities{Text: true, Images: CapabilityModelDependentOrUnverified, JSONMode: CapabilityModelDependentOrUnverified, ToolCalling: CapabilityModelDependentOrUnverified, ReasoningCtl: CapabilityModelDependentOrUnverified},
			ConfigPath: []string{"openrouter"},
		},
		{
			ID: ProviderLMStudio, DisplayName: "LM Studio", Transport: TransportOpenAIChat, Local: true,
			DefaultBaseURL: "http://127.0.0.1:1234/v1",
			DefaultAPIKey: "lm-studio",
			BaseURLEnvKeys: []string{"DBRAIN_LMSTUDIO_BASE_URL"},
			APIKeyEnvKeys: []string{"DBRAIN_LMSTUDIO_API_KEY"},
			ToolName: ToolLMStudioDirect, ToolVersion: ToolVersionLMStudioDirect,
			ParamPolicy: ParamPolicy{OpenAICompatibleLocal: true},
			PromptPolicy: PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{StatusWithDirectCall: "unknown"},
			Capabilities: TextOnlyCapabilities(),
			ConfigPath: []string{"lmstudio"},
		},
		{
			ID: ProviderOMLX, DisplayName: "oMLX", Transport: TransportOpenAIChat, Local: true,
			DefaultBaseURL: "http://127.0.0.1:8000/v1",
			DefaultAPIKey: "",
			BaseURLEnvKeys: []string{"DBRAIN_OMLX_BASE_URL"},
			APIKeyEnvKeys: []string{"DBRAIN_OMLX_API_KEY"},
			ToolName: ToolOMLXDirect, ToolVersion: ToolVersionOMLXDirect,
			ParamPolicy: ParamPolicy{OpenAICompatibleLocal: true},
			PromptPolicy: PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{StatusWithDirectCall: "unknown"},
			Capabilities: TextOnlyCapabilities(),
			ConfigPath: []string{"omlx"},
		},
	}
}

func TextOnlyCapabilities() Capabilities {
	return Capabilities{
		Text: true,
		Images: CapabilityUnsupported,
		JSONMode: CapabilityModelDependentOrUnverified,
		ToolCalling: CapabilityModelDependentOrUnverified,
		ReasoningCtl: CapabilityModelDependentOrUnverified,
	}
}
```

- [ ] **Step 8: Add configured alias loading**

Add `RegistryForRoot(rootDir string) (Registry, error)` to `internal/llmprovider/registry.go`. Use `runtimeenv.ConfigMap(rootDir, "llm_backends")`. For each alias:

```go
func RegistryForRoot(rootDir string) (Registry, error) {
	reg := NewRegistry()
	raw, ok := runtimeenv.ConfigMap(rootDir, "llm_backends")
	if !ok {
		return reg, nil
	}
	for alias, value := range raw {
		id := Provider(strings.ToLower(strings.TrimSpace(alias)))
		if id == "" {
			return Registry{}, fmt.Errorf("llm_backends contains an empty alias id")
		}
		entry, ok := value.(map[string]any)
		if !ok {
			return Registry{}, fmt.Errorf("llm_backends.%s must be a map", alias)
		}
		baseURL := strings.TrimSpace(stringValue(entry, "base_url"))
		if baseURL == "" {
			return Registry{}, fmt.Errorf("llm_backends.%s base_url is required", alias)
		}
		transport := Transport(stringValue(entry, "transport"))
		if transport == "" {
			transport = TransportOpenAIChat
		}
		if transport != TransportOpenAIChat {
			return Registry{}, fmt.Errorf("llm_backends.%s transport %q is not supported in this release", alias, transport)
		}
		local, _ := boolValue(entry, "local")
		spec := ProviderSpec{
			ID: id,
			DisplayName: firstString(stringValue(entry, "display_name"), string(id)),
			Transport: transport,
			Local: local,
			DefaultBaseURL: baseURL,
			DefaultAPIKey: "",
			ToolName: string(id) + "-direct",
			ToolVersion: string(id) + "-direct-v1",
			ParamPolicy: ParamPolicy{OpenAICompatibleLocal: local},
			PromptPolicy: PromptPolicy{ParityStatusWithPreset: "requires-live-verification"},
			ReasoningPolicy: ReasoningPolicy{StatusWithDirectCall: "unknown"},
			Capabilities: TextOnlyCapabilities(),
			ConfigPath: []string{"llm_backends", string(id)},
		}
		if err := reg.Register(spec); err != nil {
			return Registry{}, err
		}
	}
	return reg, nil
}
```

Implement `stringValue`, `boolValue`, and `firstString` in the same package. Do not resolve `api_key` secret refs while building the registry; resolution belongs in `ResolveTarget` for the selected provider only.
Do not copy configured alias `api_key` into `ProviderSpec.DefaultAPIKey`; it is
raw config input, not a default credential. `ResolveTarget` is the only place
that may read `llm_backends.<alias>.api_key`, resolve secret refs, and decide
whether to set or omit `Authorization`.
Aliases are intentionally not visible through the package-level
`ParseModelRef`; that helper is built-in-only for compatibility. Summary,
categorization, bakeoff, and any future task wrapper that accepts configured
aliases must parse through a registry from `RegistryForRoot(rootDir)`.
When using a root-aware registry, empty configured alias prefixes such as
`localai/` and `localai:` must fail closed the same way built-in empty prefixes
do.

- [ ] **Step 9: Rename parameter accounting while preserving branch compatibility**

In `internal/llmprovider/params.go`, add the new canonical type:

```go
type ParamAccounting struct {
	Requested  map[string]any
	Sent       map[string]any
	Omitted    map[string]string
	Strictness string
}

type ParityParams = ParamAccounting
```

Keep existing `DbrainModelfilePreset`, `EmptyParityParams`, and `DbrainParityForProvider` function names so current bakeoff/source/categorization code still compiles during migration. Change `DbrainParityForProvider` to use provider specs:

```go
func DbrainParityForSpec(spec ProviderSpec) ParamAccounting {
	requested := DbrainModelfilePreset()
	switch {
	case spec.ParamPolicy.LocalModelfileParity:
		return ParamAccounting{Requested: CloneAnyMap(requested), Sent: CloneAnyMap(requested), Omitted: map[string]string{}, Strictness: StrictnessStrict}
	case spec.ParamPolicy.OpenAICompatibleLocal:
		return ParamAccounting{
			Requested: CloneAnyMap(requested),
			Sent: map[string]any{
				"temperature": requested["temperature"],
				"top_p": requested["top_p"],
				"top_k": requested["top_k"],
				"repeat_penalty": requested["repeat_penalty"],
			},
			Omitted: map[string]string{"min_p": "not verified for OpenAI-compatible local chat completions; requires live verification before sending"},
			Strictness: StrictnessNonStrict,
		}
	default:
		return EmptyParityParams()
	}
}
```

Then make `DbrainParityForProvider(provider Provider)` look up `NewRegistry().Spec(provider)` and call `DbrainParityForSpec`.
Also add a generic `AccountParamsForSpec(spec ProviderSpec, requested map[string]any)`
helper in `internal/llmprovider` and have both bakeoff parity code and
`internal/llmclient` use it. Do not duplicate omit reasons in `llmclient`.
For arbitrary requested sampler params, send `temperature`, `top_p`, `top_k`,
and `repeat_penalty` when present. Send `min_p` only for
`TransportOllamaChat`; omit `min_p` with a stable reason for
`TransportOpenAIChat`. Preserve unknown requested keys in `Requested`, omit
them with a stable "not mapped for this transport" reason, and mark strictness
non-strict when any requested key is omitted.

- [ ] **Step 10: Add target resolver tests**

Create `internal/llmprovider/resolve_test.go` with these exact behavior tests:

```go
func TestResolveTargetOMLXOmitsAuthorizationWhenKeyEmpty(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "omlx/qwen3.5-coder",
		Task: TaskSummary,
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.BaseURL != "http://127.0.0.1:8000/v1" {
		t.Fatalf("BaseURL = %q", target.BaseURL)
	}
	if target.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", target.APIKey)
	}
	if target.AuthorizationHeader() != "" {
		t.Fatalf("AuthorizationHeader = %q", target.AuthorizationHeader())
	}
}

func TestResolveTargetOpenRouterRequiresKeyAndSummaryHeadersAreConfiguredOnly(t *testing.T) {
	t.Parallel()

	_, err := ResolveTarget(context.Background(), ResolveOptions{Model: "openrouter/google/gemini-test", Task: TaskSummary})
	if err == nil || !strings.Contains(err.Error(), "OpenRouter") || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing OpenRouter API key error, got %v", err)
	}

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "openrouter/google/gemini-test",
		Task: TaskSummary,
		Env: map[string]string{"DBRAIN_OPENROUTER_API_KEY": "router-key"},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Headers["HTTP-Referer"] != "" || target.Headers["X-Title"] != "" {
		t.Fatalf("summary should not default OpenRouter referer/title, got %#v", target.Headers)
	}
	if got := target.Headers["User-Agent"]; !strings.HasPrefix(got, "dbrain/") {
		t.Fatalf("expected default dbrain User-Agent for OpenRouter summary, got %q", got)
	}
}

func TestResolveTargetOpenRouterCategorizeKeepsHeaderDefaults(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		Model: "openrouter/google/gemini-test",
		Task: TaskCategorize,
		Env: map[string]string{"DBRAIN_OPENROUTER_API_KEY": "router-key"},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Headers["HTTP-Referer"] != "https://local.dbrain" || target.Headers["X-Title"] != "dbrain categorize" {
		t.Fatalf("categorize OpenRouter headers = %#v", target.Headers)
	}
}

func TestResolveTargetConfiguredAliasResolvesOnlySelectedSecret(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
openrouter:
  api_key: env:MISSING_OPENROUTER_KEY
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: env:LOCALAI_KEY
    local: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAI_KEY", "alias-secret")

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		RootDir: root,
		Model: "localai/test-model",
		Task: TaskSummary,
	})
	if err != nil {
		t.Fatalf("ResolveTarget should not read unrelated OpenRouter secret: %v", err)
	}
	if target.APIKey != "alias-secret" {
		t.Fatalf("APIKey = %q", target.APIKey)
	}
}

func TestResolveTargetEmptyOverrideFallsThroughToSelectedSecret(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
openrouter:
  api_key: env:OPENROUTER_TEST_KEY
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENROUTER_TEST_KEY", "resolved-openrouter-key")

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		RootDir: root,
		Model: "openrouter/google/gemini-test",
		Task: TaskCategorize,
		Overrides: map[Provider]ProviderOverrides{
			ProviderOpenRouter: {BaseURL: "", APIKey: ""},
		},
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.APIKey != "resolved-openrouter-key" {
		t.Fatalf("APIKey = %q", target.APIKey)
	}
}

func TestResolveTargetAliasUnsetSecretRefDoesNotBecomeBearerToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: env:MISSING_LOCALAI_KEY
    local: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	target, err := ResolveTarget(context.Background(), ResolveOptions{
		RootDir: root,
		Model: "localai/test-model",
		Task: TaskSummary,
	})
	if err == nil {
		t.Fatalf("expected unresolved alias secret ref error, got target %+v", target)
	}
	if strings.Contains(err.Error(), "Bearer env:MISSING_LOCALAI_KEY") {
		t.Fatalf("secret ref leaked as bearer token: %v", err)
	}
}
```

- [ ] **Step 11: Implement target resolution**

Create `internal/llmprovider/resolve.go`:

```go
type ProviderOverrides struct {
	BaseURL string
	APIKey string
	Referer string
	Title string
	UserAgent string
}

type ResolveOptions struct {
	RootDir string
	Env map[string]string
	Model string
	Task Task
	Overrides map[Provider]ProviderOverrides
}

type Target struct {
	Ref ModelRef
	Spec ProviderSpec
	BaseURL string
	APIKey string
	Headers map[string]string
	DisplayName string
}

func (t Target) AuthorizationHeader() string {
	if strings.TrimSpace(t.APIKey) == "" {
		return ""
	}
	return "Bearer " + strings.TrimSpace(t.APIKey)
}
```

Implementation rules:

- Build `reg, err := RegistryForRoot(opts.RootDir)`.
- Parse with `reg.ParseModelRef(opts.Model)`.
- Return `direct model requested without a supported direct model` if the model is not provider-qualified.
- Resolve base URL in this order: non-empty selected provider override,
  explicit env map, `runtimeenv.FirstNonEmpty(rootDir, spec.BaseURLEnvKeys...)`,
  configured alias `llm_backends.<id>.base_url`, provider default.
- Resolve API key only for the selected provider in this order: selected
  provider override when non-empty, explicit env map value for the selected provider,
  `runtimeenv.FirstNonEmptySecret(ctx, rootDir, spec.APIKeyEnvKeys...)`,
  configured alias `llm_backends.<id>.api_key` via
  `runtimeenv.ResolveSecretRef`, provider default.
- Empty override fields mean "not set" and must fall through to env/config. This
  preserves plain-model categorization, where legacy `Options.OpenRouterKey`
  may be empty until the resolver loads `openrouter.api_key`.
- Use `runtimeenv.ResolveSecretRef` for config `api_key` values that start with `env:`, `op://`, or `keychain://`.
- If `spec.RequiresAPIKey` and the resolved key is empty, return a provider-named error.
- Normalize `TransportOpenAIChat` base URLs to include `/v1`, except OpenRouter keeps `/api/v1`.
- Base URL normalization must be idempotent: `http://host:8000/v1` stays
  `http://host:8000/v1`, not `http://host:8000/v1/v1`.
- Normalize `TransportOllamaChat` base URLs to no `/v1` suffix.
- Add OpenRouter headers from selected overrides/env/config/defaults by task.
- Preserve current OpenRouter direct-summary behavior by always emitting a
  versioned `User-Agent` header for OpenRouter, even when referer/title are not
  configured. Categorization and OCR keep their existing OpenRouter
  referer/title defaults.
- Do not read or resolve secrets for any provider other than `ref.Provider`.
- Keep the existing non-empty default API keys for Ollama (`ollama`) and LM
  Studio (`lm-studio`) so their current `Authorization` headers remain
  unchanged. Empty-key omission applies to oMLX and configured aliases that
  resolve no API key.

- [ ] **Step 12: Run registry and resolver tests**

Run:

```sh
go test ./internal/runtimeenv ./internal/llmprovider
```

Expected: PASS.

- [ ] **Step 13: Commit Task 1 in an isolated execution worktree**

```sh
git add internal/runtimeenv/config.go internal/runtimeenv/runtimeenv_test.go internal/llmprovider/provider.go internal/llmprovider/provider_test.go internal/llmprovider/params.go internal/llmprovider/params_test.go internal/llmprovider/registry.go internal/llmprovider/resolve.go internal/llmprovider/resolve_test.go
git commit -m "refactor: add llm backend registry"
```

## Task 2: Shared Chat Client

**Files:**
- Create: `internal/llmclient/types.go`
- Create: `internal/llmclient/client.go`
- Create: `internal/llmclient/openai.go`
- Create: `internal/llmclient/ollama.go`
- Create: `internal/llmclient/params.go`
- Test: `internal/llmclient/client_test.go`

- [ ] **Step 1: Write client transport tests**

Create `internal/llmclient/client_test.go` with table-driven tests for these cases:

```go
func TestChatOpenAICompatibleLocalOMLXOmitsAuthorization(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	var capturedPath string
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3.5-coder","choices":[{"message":{"content":"local response"}}]}`))
	}))
	defer server.Close()

	resp, err := Chat(context.Background(), Request{
		Model: "omlx/qwen3.5-coder",
		Messages: []Message{
			SystemMessage("system prompt"),
			UserTextMessage("body text"),
		},
		Timeout: 2 * time.Second,
		Task: llmprovider.TaskSummary,
		Resolve: llmprovider.ResolveOptions{
			Env: map[string]string{"DBRAIN_OMLX_BASE_URL": server.URL + "/v1"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedAuth != "" {
		t.Fatalf("Authorization = %q, want empty", capturedAuth)
	}
	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", capturedPath)
	}
	if captured.Model != "qwen3.5-coder" {
		t.Fatalf("model = %q", captured.Model)
	}
	if resp.Text != "local response" || resp.Model != "omlx/qwen3.5-coder" || resp.APIModel != "qwen3.5-coder" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Tool != llmprovider.ToolOMLXDirect || resp.ToolVersion != llmprovider.ToolVersionOMLXDirect {
		t.Fatalf("tool = %s/%s", resp.Tool, resp.ToolVersion)
	}
}

func TestChatOllamaSendsNativeOptionsAndDisablesThinking(t *testing.T) {
	t.Parallel()

	var captured ollamaChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"model":"dbrain:2026042701","message":{"content":"ollama response"}}`))
	}))
	defer server.Close()

	resp, err := Chat(context.Background(), Request{
		Model: "ollama/dbrain:2026042701",
		Messages: []Message{UserTextMessage("body text")},
		SamplerParams: map[string]any{"temperature": 0.6, "top_p": 0.95},
		Timeout: 2 * time.Second,
		Task: llmprovider.TaskSummary,
		Resolve: llmprovider.ResolveOptions{Env: map[string]string{"DBRAIN_OLLAMA_BASE_URL": server.URL}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if captured.Think == nil || *captured.Think {
		t.Fatalf("expected think=false, got %#v", captured.Think)
	}
	if captured.Options["temperature"] != float64(0.6) || captured.Options["top_p"] != float64(0.95) {
		t.Fatalf("options = %#v", captured.Options)
	}
	if resp.Transport != llmprovider.TransportOllamaChat {
		t.Fatalf("transport = %q", resp.Transport)
	}
}

func TestChatRejectsImagesForTextOnlyProvider(t *testing.T) {
	t.Parallel()

	_, err := Chat(context.Background(), Request{
		Model: "omlx/qwen3.5-coder",
		Messages: []Message{{
			Role: "user",
			Parts: []ContentPart{
				{Type: ContentText, Text: "caption this"},
				{Type: ContentImage, ImageData: []byte{1, 2, 3}, MIMEType: "image/jpeg"},
			},
		}},
		Timeout: time.Second,
		Task: llmprovider.TaskCategorize,
	})
	if err == nil || !strings.Contains(err.Error(), "oMLX") || !strings.Contains(err.Error(), "images") {
		t.Fatalf("expected image capability error, got %v", err)
	}
}
```

Also add tests named:

- `TestChatOpenRouterSendsRequiredAuthAndCategorizeHeaders`
- `TestChatConfiguredAliasUsesConfiguredEndpoint`
- `TestChatOpenAICompatibleNoChoicesError`
- `TestChatOpenAICompatibleEmptyContentError`
- `TestChatTimeoutUsesRequestContext`

- [ ] **Step 2: Run client tests and verify package is missing**

Run:

```sh
go test ./internal/llmclient
```

Expected: FAIL because `internal/llmclient` does not exist.

- [ ] **Step 3: Define client types**

Create `internal/llmclient/types.go`:

```go
package llmclient

import (
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
)

type ContentType string

const (
	ContentText  ContentType = "text"
	ContentImage ContentType = "image"
)

type ContentPart struct {
	Type      ContentType
	Text      string
	ImageData []byte
	MIMEType  string
}

type Message struct {
	Role  string
	Parts []ContentPart
}

type ResponseContract string

const (
	ResponseContractText ResponseContract = "text"
	ResponseContractJSON ResponseContract = "json_prompt_only"
)

type Request struct {
	Model            string
	Messages         []Message
	SamplerParams    map[string]any
	ResponseContract ResponseContract
	Timeout          time.Duration
	Task             llmprovider.Task
	RootDir          string
	Env              map[string]string
	ProviderOverrides map[llmprovider.Provider]llmprovider.ProviderOverrides
	Resolve          llmprovider.ResolveOptions
	HTTPClient       *http.Client
}

type Response struct {
	Text            string
	RawJSON         string
	Model           string
	Provider        llmprovider.Provider
	APIModel        string
	Transport       llmprovider.Transport
	Tool            string
	ToolVersion     string
	ParamAccounting llmprovider.ParamAccounting
}

func SystemMessage(text string) Message {
	return Message{Role: "system", Parts: []ContentPart{{Type: ContentText, Text: text}}}
}

func UserTextMessage(text string) Message {
	return Message{Role: "user", Parts: []ContentPart{{Type: ContentText, Text: text}}}
}
```

- [ ] **Step 4: Implement Chat dispatch and capability checks**

Create `internal/llmclient/client.go`:

```go
func Chat(ctx context.Context, req Request) (Response, error) {
	if req.Timeout <= 0 {
		req.Timeout = 2 * time.Minute
	}
	if req.Task == "" {
		req.Task = llmprovider.TaskSummary
	}
	resolveOpts := req.Resolve
	resolveOpts.Model = req.Model
	resolveOpts.Task = req.Task
	if resolveOpts.RootDir == "" {
		resolveOpts.RootDir = req.RootDir
	}
	if resolveOpts.Env == nil {
		resolveOpts.Env = req.Env
	}
	if resolveOpts.Overrides == nil {
		resolveOpts.Overrides = req.ProviderOverrides
	}
	target, err := llmprovider.ResolveTarget(ctx, resolveOpts)
	if err != nil {
		return Response{}, err
	}
	if err := validateCapabilities(target, req.Messages); err != nil {
		return Response{}, err
	}
	client := req.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	accounting := accountParams(target.Spec, req.SamplerParams)
	switch target.Spec.Transport {
	case llmprovider.TransportOllamaChat:
		return chatOllama(ctx, client, req, target, accounting)
	case llmprovider.TransportOpenAIChat:
		return chatOpenAI(ctx, client, req, target, accounting)
	default:
		return Response{}, fmt.Errorf("provider %s transport %s is not implemented", target.Spec.DisplayName, target.Spec.Transport)
	}
}
```

`Chat` must use `req.HTTPClient` when set and fall back to `http.DefaultClient`
when nil. Existing tests in this repo frequently swap `http.DefaultClient` with
a custom transport; direct summary tests rely on that interception pattern.

`validateCapabilities` must reject `ContentImage` parts only when
`target.Spec.Capabilities.Images == llmprovider.CapabilityUnsupported`.
`CapabilityModelDependentOrUnverified` is allowed through the transport for
existing image-capable task paths, which preserves current OpenRouter image
categorization and Ollama image transport behavior. LM Studio, oMLX, and
configured aliases remain text-only because their first-pass specs use
`CapabilityUnsupported`.

- [ ] **Step 5: Implement OpenAI-compatible transport**

Create `internal/llmclient/openai.go` with request structs local to this package:

```go
type openAIChatRequest struct {
	Model string `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream bool `json:"stream"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP *float64 `json:"top_p,omitempty"`
	TopK *int `json:"top_k,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
}

type openAIMessage struct {
	Role string `json:"role"`
	Content any `json:"content"`
}
```

`chatOpenAI` must:

- POST to `strings.TrimRight(target.BaseURL, "/") + "/chat/completions"`.
- Set `Content-Type: application/json`.
- Set `Authorization` only when `target.AuthorizationHeader()` is non-empty.
- Copy `target.Headers` except empty values.
- Convert text-only messages to `content: string`.
- Convert mixed text/image messages to OpenAI content parts with data URLs.
- Parse `choices[0].message.content`.
- Return provider-qualified `target.Ref.Original` as `Response.Model`.
- Return raw response JSON in `Response.RawJSON`.

- [ ] **Step 6: Implement Ollama transport**

Create `internal/llmclient/ollama.go` with request structs local to this package:

```go
type ollamaChatRequest struct {
	Model string `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream bool `json:"stream"`
	Think *bool `json:"think,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role string `json:"role"`
	Content string `json:"content"`
	Images []string `json:"images,omitempty"`
}
```

`chatOllama` must:

- POST to `strings.TrimRight(target.BaseURL, "/") + "/api/chat"`.
- Set `think=false`.
- Put sampler params under `options`.
- Attach base64 images to the user message that carried image parts.
- Omit `Authorization` when the API key is empty.
- Parse `message.content`.

- [ ] **Step 7: Implement sampler accounting**

Create `internal/llmclient/params.go` as a thin wrapper over provider policy:

```go
func accountParams(spec llmprovider.ProviderSpec, requested map[string]any) llmprovider.ParamAccounting {
	return llmprovider.AccountParamsForSpec(spec, requested)
}
```

- [ ] **Step 8: Run client tests**

Run:

```sh
go test ./internal/llmclient
```

Expected: PASS.

- [ ] **Step 9: Commit Task 2 in an isolated execution worktree**

```sh
git add internal/llmclient
git commit -m "feat: add shared llm chat client"
```

## Task 3: Migrate Direct Summaries Through llmclient

**Files:**
- Modify: `internal/summarizecli/types.go`
- Modify: `internal/summarizecli/provider.go`
- Modify: `internal/summarizecli/env.go`
- Modify: `internal/summarizecli/direct_target.go`
- Modify: `internal/summarizecli/direct.go`
- Modify or delete: `internal/summarizecli/direct_response.go`
- Test: `internal/summarizecli/client_test.go`

- [ ] **Step 1: Add summary compatibility tests**

Append tests to `internal/summarizecli/client_test.go`:

```go
func TestRunDirectOMLXSummaryForLocalFileInput(t *testing.T) {
	var captured struct {
		Model string `json:"model"`
		Messages []struct {
			Role string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"model":"qwen3.5-coder","choices":[{"message":{"content":"omlx summary"}}]}`)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	inputPath := filepath.Join(t.TempDir(), "summary-input.md")
	if err := os.WriteFile(inputPath, []byte("Title: Example\n\nBody content"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(context.Background(), Options{
		Input: inputPath,
		Summarize: true,
		Model: "omlx/qwen3.5-coder",
		Prompt: "System prompt",
		Timeout: 2 * time.Second,
		Env: map[string]string{"DBRAIN_OMLX_BASE_URL": "http://omlx.test/v1"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Text != "omlx summary" || result.Summary.Model != "omlx/qwen3.5-coder" {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if result.Summary.Tool != llmprovider.ToolOMLXDirect || result.Summary.ToolVersion != llmprovider.ToolVersionOMLXDirect {
		t.Fatalf("tool = %s/%s", result.Summary.Tool, result.Summary.ToolVersion)
	}
	if captured.Model != "qwen3.5-coder" {
		t.Fatalf("captured model = %q", captured.Model)
	}
}

func TestRunPlainSummaryModelStillUsesExternalSummarizeCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-plain-cli"
  exit 0
fi
prev=""
model=""
for arg in "$@"; do
  if [ "$prev" = "--model" ]; then
    model="$arg"
  fi
  prev="$arg"
done
if [ "$model" != "google/gemini-plain" ]; then
  echo "unexpected model: $model" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"google/gemini-plain"},"extracted":{"url":"README.md","title":"Readme","description":"","siteName":"","content":"body"},"summary":"external cli summary"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		Binary: binary,
		Input: "README.md",
		Summarize: true,
		Model: "google/gemini-plain",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary.Tool != ToolName || result.Summary.ToolVersion != "test-plain-cli" {
		t.Fatalf("expected external summarize CLI tool, got %+v", result.Summary)
	}
}
```

Add `TestRunDirectConfiguredAliasSummary` with a temp root config:

```yaml
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://localai.test/v1
    api_key: ""
    local: true
```

Run `Run` with `Options{RootDir: root, Model: "localai/test-model"}`. Assert
`Model == "localai/test-model"`, API request model is `test-model`, and no auth
header is sent.

- [ ] **Step 2: Run summarizecli tests and verify oMLX fails**

Run:

```sh
go test ./internal/summarizecli
```

Expected: FAIL for missing oMLX direct support.

- [ ] **Step 3: Delegate direct-provider detection to the root-aware registry**

In `internal/summarizecli/direct.go`, replace `UsesDirectSummary`,
`SummaryToolName`, and `SummaryToolVersion` provider switches with root-aware
registry lookups while keeping the old signatures as built-in-only
compatibility wrappers:

```go
func UsesDirectSummary(model string) bool {
	return UsesDirectSummaryForRoot("", model)
}

func UsesDirectSummaryForRoot(rootDir string, model string) bool {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return false
	}
	ref := reg.ParseModelRef(model)
	return ref.ProviderQualified && ref.Spec != nil
}

func SummaryToolName(model string) string {
	return SummaryToolNameForRoot("", model)
}

func SummaryToolNameForRoot(rootDir string, model string) string {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return ToolName
	}
	ref := reg.ParseModelRef(model)
	if ref.ProviderQualified && ref.Spec != nil {
		return ref.Spec.ToolName
	}
	return ToolName
}

func SummaryToolVersion(ctx context.Context, binary string, model string) string {
	return SummaryToolVersionForRoot(ctx, "", binary, model)
}

func SummaryToolVersionForRoot(ctx context.Context, rootDir string, binary string, model string) string {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return Version(ctx, binary)
	}
	ref := reg.ParseModelRef(model)
	if ref.ProviderQualified && ref.Spec != nil {
		return ref.Spec.ToolVersion
	}
	return Version(ctx, binary)
}
```

In `Run`, gate direct calls with
`UsesDirectSummaryForRoot(opts.RootDir, opts.Model)` so configured aliases are
recognized. Inside `Run`, load `RegistryForRoot(opts.RootDir)` once and
propagate any registry/config error instead of treating it as "not direct"; a
bad alias config must not silently fall back to the external `summarize` CLI.
The boolean wrappers may return false on registry errors only for legacy
callers that cannot return an error. Source freshness paths that only have a
model string may continue to call the compatibility wrappers until exact alias
freshness is explicitly needed; persisted direct summary results still use the
`llmclient` response tool identity.

- [ ] **Step 4: Migrate runDirectSummary to llmclient**

In `internal/summarizecli/direct.go`, replace manual request construction with:

```go
func runDirectSummary(ctx context.Context, opts Options, inputText string) (Result, error) {
	messages := make([]llmclient.Message, 0, 2)
	if prompt := strings.TrimSpace(promptWithLengthAndLanguageHints(opts.Prompt, opts.Length, opts.Language)); prompt != "" {
		messages = append(messages, llmclient.SystemMessage(prompt))
	}
	messages = append(messages, llmclient.UserTextMessage(strings.TrimSpace(inputText)))

	response, err := llmclient.Chat(ctx, llmclient.Request{
		Model: opts.Model,
		Messages: messages,
		SamplerParams: opts.InferenceParams.Sent,
		ResponseContract: llmclient.ResponseContractText,
		Timeout: opts.Timeout,
		Task: llmprovider.TaskSummary,
		Resolve: llmprovider.ResolveOptions{
			RootDir: opts.RootDir,
			Env: opts.Env,
			Model: opts.Model,
			Task: llmprovider.TaskSummary,
		},
	})
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	return Result{Summary: model.SummaryResult{
		Text: strings.TrimSpace(response.Text),
		RawJSON: response.RawJSON,
		Model: response.Model,
		Status: "ok",
		FetchedAt: now,
		Tool: response.Tool,
		ToolVersion: response.ToolVersion,
	}}, nil
}
```

Remove direct request/response structs from `internal/summarizecli/types.go` after all compile errors are addressed. Keep `resolveModelAndEnv` unchanged for the external CLI path.

- [ ] **Step 5: Keep unsupported empty prefixes clear**

In `internal/summarizecli/provider.go`, change `providerDisplayName` to use registry specs:

```go
func providerDisplayName(provider llmprovider.Provider) string {
	reg := llmprovider.NewRegistry()
	if spec, ok := reg.Spec(provider); ok {
		return spec.DisplayName
	}
	return string(provider)
}
```

Ensure `unsupportedProviderModelError("omlx/")` returns a message containing `oMLX`.

- [ ] **Step 6: Run summary-related tests**

Run:

```sh
go test ./internal/llmprovider ./internal/llmclient ./internal/summarizecli ./internal/sourceenrich ./internal/applenotes ./internal/xmediatranscribe ./internal/ask
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3 in an isolated execution worktree**

```sh
git add internal/summarizecli internal/sourceenrich internal/applenotes internal/xmediatranscribe internal/ask
git commit -m "refactor: route direct summaries through llmclient"
```

## Task 4: Migrate Text Categorization Through llmclient

**Files:**
- Modify: `internal/itemcategorize/types.go`
- Modify: `internal/itemcategorize/options.go`
- Modify: `internal/itemcategorize/llm.go`
- Modify or delete: `internal/itemcategorize/llm_http.go`
- Test: `internal/itemcategorize/run_test.go`

- [ ] **Step 1: Add categorization backend tests**

Append tests to `internal/itemcategorize/run_test.go`:

```go
func TestCallLLMOMLXTextCategorization(t *testing.T) {
	t.Parallel()

	var capturedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		var payload struct{ Model string `json:"model"` }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		capturedModel = payload.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"local-models\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model: "omlx/qwen3.5-coder",
		OMLXBase: server.URL + "/v1",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if capturedModel != "qwen3.5-coder" {
		t.Fatalf("captured model = %q", capturedModel)
	}
	if result.Model != "omlx/qwen3.5-coder" || result.PrimaryCategory != "ai" {
		t.Fatalf("result = %+v", result)
	}
}
```

If adding `OMLXBase` to `Options` is judged too provider-specific after the resolver exists, use `Options{ProviderOverrides: map[llmprovider.Provider]llmprovider.ProviderOverrides{llmprovider.ProviderOMLX: {BaseURL: server.URL + "/v1"}}}` instead. Prefer the generic `ProviderOverrides` field if it keeps existing public fields compatible.

Also add:

- `TestCallLLMConfiguredAliasTextCategorization`: temp root with
  `llm_backends.localai`, `Options{RootDir: root, Model: "localai/test-model"}`,
  no auth header when key empty, and API request model `test-model`.
- `TestCallLLMPlainModelStillRoutesOpenRouter`: model `google/gemini-test`, OpenRouter server captures `model == "google/gemini-test"`, result model remains `google/gemini-test`.
- `TestCallLLMOpenRouterImagesStillSendImageParts`: existing OpenRouter image categorization still sends `image_url` content parts.
- `TestCallLLMRejectsImagesForOMLX`: error includes provider display name and model string.
- `TestProviderOverridesExplicitValuesWin`: set legacy OpenRouter fields to one
  endpoint/key and `ProviderOverrides[ProviderOpenRouter]` to another; assert
  the explicit generic override endpoint/key is used.

- [ ] **Step 2: Run itemcategorize tests and verify new backend cases fail**

Run:

```sh
go test ./internal/itemcategorize
```

Expected: FAIL for missing oMLX/alias path through categorization.

- [ ] **Step 3: Add generic provider overrides while preserving old options**

In `internal/itemcategorize/types.go`, keep existing fields and add:

```go
RootDir string
ProviderOverrides map[llmprovider.Provider]llmprovider.ProviderOverrides
OMLXBase string
OMLXKey string
```

If `ProviderOverrides` is used in tests, `OMLXBase` and `OMLXKey` may be omitted. If `OMLXBase` and `OMLXKey` are added for CLI/config symmetry, translate them into `ProviderOverrides` in `resolveOpts`.
At the start of `resolveOpts`, set `opts.RootDir = cfg.RootDir` when it is
empty. Tests that call `callLLM` directly for configured aliases must set
`Options.RootDir` to the temp config root.

In `internal/itemcategorize/options.go`, create:

```go
func providerOverrides(opts Options) map[llmprovider.Provider]llmprovider.ProviderOverrides {
	overrides := map[llmprovider.Provider]llmprovider.ProviderOverrides{}
	overrides[llmprovider.ProviderOpenRouter] = llmprovider.ProviderOverrides{
		BaseURL: opts.OpenRouterBase,
		APIKey: opts.OpenRouterKey,
		Referer: opts.OpenRouterRef,
		Title: opts.OpenRouterTitle,
		UserAgent: opts.UserAgent,
	}
	overrides[llmprovider.ProviderOllama] = llmprovider.ProviderOverrides{BaseURL: opts.OllamaBase, APIKey: opts.OllamaKey}
	overrides[llmprovider.ProviderLMStudio] = llmprovider.ProviderOverrides{BaseURL: opts.LMStudioBase, APIKey: opts.LMStudioKey}
	if strings.TrimSpace(opts.OMLXBase) != "" || strings.TrimSpace(opts.OMLXKey) != "" {
		overrides[llmprovider.ProviderOMLX] = llmprovider.ProviderOverrides{BaseURL: opts.OMLXBase, APIKey: opts.OMLXKey}
	}
	for provider, value := range opts.ProviderOverrides {
		overrides[provider] = mergeProviderOverride(overrides[provider], value)
	}
	return overrides
}

func mergeProviderOverride(base llmprovider.ProviderOverrides, explicit llmprovider.ProviderOverrides) llmprovider.ProviderOverrides {
	if strings.TrimSpace(explicit.BaseURL) != "" {
		base.BaseURL = explicit.BaseURL
	}
	if strings.TrimSpace(explicit.APIKey) != "" {
		base.APIKey = explicit.APIKey
	}
	if strings.TrimSpace(explicit.Referer) != "" {
		base.Referer = explicit.Referer
	}
	if strings.TrimSpace(explicit.Title) != "" {
		base.Title = explicit.Title
	}
	if strings.TrimSpace(explicit.UserAgent) != "" {
		base.UserAgent = explicit.UserAgent
	}
	return base
}
```

Keep existing `resolveOpts` defaults for OpenRouter, Ollama, and LM Studio until the full call path uses `llmprovider.ResolveTarget`. Add oMLX config/env resolution:

```go
if strings.TrimSpace(opts.OMLXBase) == "" {
	opts.OMLXBase = firstNonEmpty(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_OMLX_BASE_URL"), "http://127.0.0.1:8000/v1")
}
if _, ok := parseProviderModel(opts.Model, llmprovider.ProviderOMLX); ok && strings.TrimSpace(opts.OMLXKey) == "" {
	value, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "DBRAIN_OMLX_API_KEY")
	if err != nil {
		return err
	}
	opts.OMLXKey = value
}
```

- [ ] **Step 4: Replace provider HTTP calls with llmclient**

In `internal/itemcategorize/llm.go`, reduce `callLLM` to:

```go
func callLLM(ctx context.Context, bundle string, photoData [][]byte, opts Options) (Result, error) {
	if err := unsupportedProviderModelError(opts.Model); err != nil {
		return Result{}, err
	}

	requestModel := strings.TrimSpace(opts.Model)
	resultModel := requestModel
	reg, err := llmprovider.RegistryForRoot(opts.RootDir)
	if err != nil {
		return Result{}, err
	}
	if !reg.ParseModelRef(requestModel).ProviderQualified {
		requestModel = "openrouter/" + requestModel
	}

	userParts := []llmclient.ContentPart{{Type: llmclient.ContentText, Text: bundle}}
	for _, data := range photoData {
		userParts = append(userParts, llmclient.ContentPart{Type: llmclient.ContentImage, ImageData: data, MIMEType: "image/jpeg"})
	}

	resp, err := llmclient.Chat(ctx, llmclient.Request{
		Model: requestModel,
		Messages: []llmclient.Message{
			llmclient.SystemMessage(effectiveSystemPrompt(opts)),
			{Role: "user", Parts: userParts},
		},
		SamplerParams: opts.InferenceParams.Sent,
		ResponseContract: llmclient.ResponseContractJSON,
		Timeout: opts.Timeout,
		Task: llmprovider.TaskCategorize,
		Resolve: llmprovider.ResolveOptions{
			RootDir: opts.RootDir,
			Model: requestModel,
			Task: llmprovider.TaskCategorize,
			Overrides: providerOverrides(opts),
		},
	})
	if err != nil {
		return Result{}, err
	}
	modelForResult := resultModel
	if ref := reg.ParseModelRef(resultModel); ref.ProviderQualified {
		modelForResult = resp.Model
	}
	return parseCategorizationJSON(resp.Text, modelForResult, opts.Vocab)
}
```

After this compiles, delete `callOllama`, `callOpenRouter`, `callLMStudio`, `doPost`, duplicated request structs, and duplicated param conversion helpers if no tests still rely on them. If some tests are deliberately unit-testing old helpers, migrate those assertions to `llmclient` tests.

- [ ] **Step 5: Run categorization and client tests**

Run:

```sh
go test ./internal/llmclient ./internal/itemcategorize ./internal/modelbakeoff
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4 in an isolated execution worktree**

```sh
git add internal/itemcategorize internal/llmclient internal/modelbakeoff
git commit -m "refactor: route categorization through llmclient"
```

## Task 5: Bakeoff, Research, MCP, And OCR Compatibility Coverage

**Files:**
- Modify: `internal/modelbakeoff/types.go`
- Modify: `internal/modelbakeoff/run.go`
- Modify: `internal/modelbakeoff/report.go`
- Test: `internal/modelbakeoff/run_test.go`
- Test: `internal/modelbakeoff/report_test.go`
- Test: `internal/brainresearch/research_test.go` or `internal/brainresearch/synthesize_test.go`
- Test: `internal/mcpserver/server_test.go`
- Test: `internal/xphotoocr/run_test.go`

- [ ] **Step 1: Add bakeoff metadata tests**

Add to `internal/modelbakeoff/run_test.go`:

```go
func TestModelRunMetadataUsesProviderRegistry(t *testing.T) {
	t.Parallel()

	ref := llmprovider.ParseModelRef("omlx/qwen3.5-coder")
	parity := parityParamsForRun(llmprovider.ParityPresetDbrainModelfile, ref.Provider)
	run := newModelRunMetadata("omlx/qwen3.5-coder", ref, parity, llmprovider.ParityPresetDbrainModelfile)

	if run.Provider != "omlx" || run.APIModel != "qwen3.5-coder" {
		t.Fatalf("provider metadata = %+v", run)
	}
	if run.Transport != string(llmprovider.TransportOpenAIChat) {
		t.Fatalf("transport = %q", run.Transport)
	}
	if run.ParamStrictness != llmprovider.StrictnessNonStrict {
		t.Fatalf("strictness = %q", run.ParamStrictness)
	}
	if run.PromptParityStatus != "requires-live-verification" || run.ReasoningModeStatus != "unknown" {
		t.Fatalf("parity metadata = %+v", run)
	}
}
```

Refactor `runModel` to call
`newModelRunMetadata(candidateModel, ref, parity, opts.ParityPreset)` so this
test can exercise metadata without creating a full store.
Add a second metadata test with a temp root containing `llm_backends.localai`,
parse `localai/test-model` through `RegistryForRoot(root)`, and assert
`Provider == "localai"`, `APIModel == "test-model"`,
`Transport == "openai_chat_completions"`, and `Local == true`.
Add a no-preset metadata test for a local provider and assert
`PromptParityStatus == ""`; add an OpenRouter + `dbrain-modelfile` test and
assert `PromptParityStatus == "not-applicable"`.

- [ ] **Step 2: Add transport field to bakeoff schema and report**

In `internal/modelbakeoff/types.go`, add:

```go
Transport string `json:"transport,omitempty"`
Local *bool `json:"local,omitempty"`
```

Use a pointer for `Local` so plain/unqualified models can omit the field. Keep
`SchemaVersion = "model_bakeoff.v2"` and fold `Transport`/`Local` into v2:
`main` still has `model_bakeoff.v1`, and this branch introduced v2.

In `internal/modelbakeoff/report.go`, render:

```go
if run.Transport != "" {
	fmt.Fprintf(&b, "- Transport: `%s`\n", run.Transport)
}
if run.Local != nil {
	fmt.Fprintf(&b, "- Local backend: `%t`\n", *run.Local)
}
```

- [ ] **Step 3: Replace bakeoff provider switches with registry metadata**

In `internal/modelbakeoff/run.go`, add:

```go
func newModelRunMetadata(candidateModel string, ref llmprovider.ModelRef, parity llmprovider.ParityParams, preset string) ModelRun {
	run := ModelRun{
		Model: candidateModel,
		Provider: string(ref.Provider),
		APIModel: ref.APIModel,
		Status: "ok",
		RequestedParams: parity.Requested,
		SentParams: parity.Sent,
		OmittedParams: parity.Omitted,
		ParamStrictness: parity.Strictness,
		RuntimeContext: runtimeContextForRun(ref),
	}
	if ref.Spec != nil {
		run.Transport = string(ref.Spec.Transport)
		local := ref.Spec.Local
		run.Local = &local
		run.PromptParityStatus = promptParityStatusForSpec(preset, ref.Spec)
		run.ReasoningModeStatus = ref.Spec.ReasoningPolicy.StatusWithDirectCall
	}
	return run
}

func promptParityStatusForSpec(preset string, spec *llmprovider.ProviderSpec) string {
	if spec == nil || strings.TrimSpace(preset) == "" || preset == llmprovider.ParityPresetNone {
		return ""
	}
	if spec.Local {
		return spec.PromptPolicy.ParityStatusWithPreset
	}
	return "not-applicable"
}
```

In `runModel`, build `reg, err := llmprovider.RegistryForRoot(cfg.RootDir)`
once before parsing candidate models. Parse each candidate with
`reg.ParseModelRef(candidateModel)` so configured aliases populate provider,
transport, local/hosted, prompt parity, reasoning, and parameter metadata.
Then remove `promptParityStatus` and `reasoningModeStatus` provider switches
after tests pass.

- [ ] **Step 4: Add research inheritance test**

Use the smallest stable test surface. Prefer `internal/summarizecli` direct tests for transport behavior and add one `brainresearch` test only for model propagation:

```go
func TestResearchPlannerCanUseOMLXModelThroughSummarizeCLI(t *testing.T) {
	// Seed the existing brainresearch test store with one simple item/source.
	// Configure opts.PlannerModel = "omlx/qwen3.5-coder".
	// Set cfg.RootDir to a temp root whose config.yaml contains:
	// omlx:
	//   base_url: <httptest server>/v1
	// The server returns a compact valid planner JSON:
	// {"concepts":[{"key":"local_models","preferred":"local models","terms":["local models"],"required":true}],"query_variants":[{"query":"local models","reason":"direct question term"}]}
	// Assert the server was hit and the returned pack planner metadata records model "omlx/qwen3.5-coder".
}
```

When implementing, use existing `brainresearch` test helpers for seeded stores instead of creating new test infrastructure. Assert stable metadata and retrieval behavior, not answer prose.

- [ ] **Step 5: Add MCP pass-through test**

In `internal/mcpserver/server_test.go`, add a focused test around `toolResearchPack`:

```go
func TestResearchPackPlannerModelOMLXPassesThroughMCP(t *testing.T) {
	// Build a test Server with temp cfg.RootDir and seeded store.
	// Mock oMLX /v1/chat/completions via config.yaml.
	// Call toolResearchPack with planner_model "omlx/qwen3.5-coder" and use_model_planner true.
	// Assert the HTTP mock was hit.
	// Assert the structured result still has research-pack shape.
	// Assert no MCP code imports internal/llmclient.
}
```

The last assertion can be a simple source scan test if a package-level import guard already exists; otherwise rely on code review. Do not add provider dispatch to `internal/mcpserver`.

- [ ] **Step 6: Add hosted OCR compatibility regression**

Extend `internal/xphotoocr/run_test.go` with a config-driven hosted OCR test:

```go
func TestRunHostedOCRDoesNotRequireLocalBackendConfig(t *testing.T) {
	t.Parallel()

	cfg, st, _ := seedDownloadedPhotoItem(t, "x:test-photo-hosted-no-local-backend", "2049000000000000999")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer hosted-key" {
			t.Fatalf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"model":"google/gemini-3.1-flash-lite-preview","choices":[{"message":{"content":"Hosted OCR still works."}}]}`))
	}))
	defer server.Close()

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit: 10,
		Model: "openrouter/google/gemini-3.1-flash-lite-preview",
		OpenRouterBase: server.URL,
		OpenRouterKey: "hosted-key",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.HostedAttempts != 1 || stats.PhotosOCRed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}
```

This test should not configure Ollama, LM Studio, oMLX, or `llm_backends`.

- [ ] **Step 7: Run coverage tests for migrated and protected paths**

Run:

```sh
go test ./internal/modelbakeoff ./internal/brainresearch ./internal/mcpserver ./internal/xphotoocr
```

Expected: PASS.

- [ ] **Step 8: Commit Task 5 in an isolated execution worktree**

```sh
git add internal/modelbakeoff internal/brainresearch internal/mcpserver internal/xphotoocr
git commit -m "test: cover backend metadata and hosted ocr compatibility"
```

## Task 6: Docs, Config, Changelog, And Final Gates

**Files:**
- Modify: `internal/app/env_docs.go`
- Modify: `config.yaml.sample`
- Modify: `README.md`
- Modify: `COMMANDS.md`
- Modify: `skills/dbrain-model-bakeoff/SKILL.md`
- Modify: `CHANGELOG.md`
- Test: `internal/app/app_test.go`

- [ ] **Step 1: Update env/config docs source**

In `internal/app/env_docs.go`, add entries:

```go
{Key: "DBRAIN_OMLX_BASE_URL", ConfigPath: "omlx.base_url", Default: "http://127.0.0.1:8000/v1", Description: "oMLX OpenAI-compatible endpoint for local text model calls."},
{Key: "DBRAIN_OMLX_API_KEY", ConfigPath: "omlx.api_key", Default: "", Description: "Optional API key for oMLX local calls; omitted from Authorization when empty; supports secret refs."},
{Key: "(config only)", ConfigPath: "llm_backends.<alias>.transport", Default: "openai_chat_completions", Description: "Configured OpenAI-compatible backend alias transport."},
{Key: "(config only)", ConfigPath: "llm_backends.<alias>.base_url", Default: "", Description: "Configured OpenAI-compatible backend endpoint, for example http://127.0.0.1:8080/v1."},
{Key: "(config only)", ConfigPath: "llm_backends.<alias>.api_key", Default: "", Description: "Optional API key or secret ref for a configured backend alias."},
{Key: "(config only)", ConfigPath: "llm_backends.<alias>.local", Default: "false", Description: "Marks a configured backend alias as local for provenance and bakeoff reporting."},
```

Keep existing OpenRouter OCR entries unchanged.

- [ ] **Step 2: Update config sample**

In `config.yaml.sample`, add after `lmstudio`:

```yaml
omlx:
  base_url: "http://127.0.0.1:8000/v1" # DBRAIN_OMLX_BASE_URL
  api_key: "" # DBRAIN_OMLX_API_KEY; optional, secret ref supported

llm_backends:
  localai:
    transport: "openai_chat_completions"
    base_url: "http://127.0.0.1:8080/v1"
    api_key: "" # optional; supports direct values, env:, op://, and keychain:// refs
    local: true
```

Leave `ocr.model` default as OpenRouter/Gemini.

- [ ] **Step 3: Update user docs**

In `README.md` and `COMMANDS.md`, document these examples:

```sh
dbrain source run --model omlx/qwen3.5-coder --limit 5
dbrain categorize run --model omlx/qwen3.5-coder --limit 5
dbrain research "What do I know about local models?" --planner-model omlx/qwen3.5-coder --model omlx/qwen3.5-coder
go run ./cmd/devtools/model_bakeoff --model ollama/dbrain:2026042701 --model lmstudio/qwen/qwen3.6-35b-a3b --model omlx/qwen3.5-coder --parity-preset dbrain-modelfile
```

Add a compatibility note:

```md
Hosted OCR is still configured through `ocr.model` and OpenRouter settings. You do not need Ollama, LM Studio, oMLX, or any local backend for the default OpenRouter/Gemini OCR path.
```

- [ ] **Step 4: Update bakeoff skill**

In `skills/dbrain-model-bakeoff/SKILL.md`, add one local backend comparison example including oMLX and one configured alias example. State that transport, provider, API model, local/hosted flag, and param strictness are recorded in the report.

- [ ] **Step 5: Update changelog**

Add a dated entry to `CHANGELOG.md` under the current date heading or create one:

```md
- Added a shared LLM backend registry/client for direct local and hosted model calls, including first-class `omlx/<model>` and configured OpenAI-compatible backend aliases, while preserving existing Ollama, OpenRouter, LM Studio, external `summarize` CLI, and OpenRouter/Gemini OCR behavior.
```

- [ ] **Step 6: Run docs/app tests**

Run:

```sh
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 7: Run full required gates**

Run:

```sh
task fmt
task lint
task test-ci
task build
```

Expected: all PASS. If `task test-ci` fails from a sandbox/environment limitation, rerun with the approved elevated `task test-ci` path and record the exact failure or success.

- [ ] **Step 8: Run focused package tests again after formatting**

Run:

```sh
go test ./internal/runtimeenv ./internal/llmprovider ./internal/llmclient ./internal/summarizecli ./internal/itemcategorize ./internal/sourceenrich ./internal/applenotes ./internal/xmediatranscribe ./internal/ask ./internal/xphotoocr ./internal/brainresearch ./internal/researchrun ./internal/mcpserver ./internal/modelbakeoff ./internal/app
```

Expected: PASS.

- [ ] **Step 9: Scan for accidental local-only assumptions**

Run:

```sh
rg -n "LMStudio|LM Studio|omlx|oMLX|llm_backends|OpenRouter|OCR|Authorization" internal docs README.md COMMANDS.md config.yaml.sample
```

Verify:

- OpenRouter/Gemini OCR docs remain present.
- No code path says local backend config is required for hosted OCR.
- oMLX image/OCR support is not advertised.
- Configured aliases are documented as OpenAI-compatible chat only.
- Empty local API keys omit Authorization.

- [ ] **Step 10: Commit Task 6 in an isolated execution worktree**

```sh
git add internal/app/env_docs.go config.yaml.sample README.md COMMANDS.md skills/dbrain-model-bakeoff/SKILL.md CHANGELOG.md
git commit -m "docs: document llm backend abstraction"
```

## Self-Review Checklist

- [ ] Every accepted spec goal maps to a task above.
- [ ] `omlx/<model>` is covered in parser, resolver, summary, categorization, research/MCP pass-through, bakeoff, docs, and config.
- [ ] Configured OpenAI-compatible aliases are covered in parser, resolver, summary, categorization, bakeoff metadata, docs, and config.
- [ ] Hosted OpenRouter/Gemini OCR is protected by an explicit regression test and docs note.
- [ ] Unqualified summary models still use the external `summarize` CLI path.
- [ ] Unqualified categorization models still route as plain OpenRouter model ids.
- [ ] OpenRouter summary referer/title remain configured-only while summary `User-Agent` remains defaulted; OpenRouter categorization/OCR headers keep their existing defaults.
- [ ] Resolver tests prove unrelated provider secrets are not read.
- [ ] OpenAI-compatible empty-key requests omit `Authorization`.
- [ ] MCP has no provider dispatch and no dependency on `internal/llmclient`.
- [ ] `categorize vocab` remains Ollama-only unless separately migrated; docs/help must not imply otherwise.
- [ ] OCR/VLM support for oMLX is not claimed.
- [ ] Full gates include `task fmt`, `task lint`, `task test-ci`, and `task build`.

## Deferred Follow-Ups

- Migrate `dbrain categorize vocab` to `llmclient` after the main abstraction lands, or keep help text explicitly Ollama-only.
- Consider moving `internal/xphotoocr` Ollama/OpenRouter LLM calls to `llmclient` after image capability policy is proven with real VLM smoke tests.
- Add research-specific model config defaults (`research.planner_model`, `research.synthesis_model`, `research.answer_review_model`) after the backend layer is stable.
- Add provider health checks only after request-time diagnostics prove insufficient.
