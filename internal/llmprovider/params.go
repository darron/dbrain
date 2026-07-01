package llmprovider

// Parity preset identifiers used by model_bakeoff --parity-preset.
const (
	ParityPresetNone            = "none"
	ParityPresetDbrainModelfile = "dbrain-modelfile"
)

// Strictness labels describe how strict a parity run is about the parameters
// actually sent to a provider.
const (
	StrictnessNone      = "none"
	StrictnessStrict    = "strict"
	StrictnessNonStrict = "non-strict"
)

// ParamAccounting records the requested, sent, and omitted inference
// parameters for a model call. Requested is the caller's requested shape; Sent
// is what goes on the wire; Omitted maps requested-but-not-sent keys to a
// reason.
type ParamAccounting struct {
	Requested  map[string]any
	Sent       map[string]any
	Omitted    map[string]string
	Strictness string
}

type ParityParams = ParamAccounting

// EmptyParityParams is the zero shape used when no parity preset is active.
func EmptyParityParams() ParityParams {
	return ParityParams{
		Requested:  map[string]any{},
		Sent:       map[string]any{},
		Omitted:    map[string]string{},
		Strictness: StrictnessNone,
	}
}

// DbrainModelfilePreset returns the sampler values defined in the repo
// Modelfile. These are the canonical dbrain parity values for local runs.
func DbrainModelfilePreset() map[string]any {
	return map[string]any{
		"temperature":    0.6,
		"top_p":          0.95,
		"top_k":          20,
		"min_p":          0.0,
		"repeat_penalty": 1.0,
	}
}

// DbrainParityForProvider returns the dbrain Modelfile parity parameters
// adjusted for what each provider path can actually accept.
//
// Ollama: all Modelfile sampler values are sent through the native /api/chat
// options object, so strict parity is achievable.
//
// LM Studio: min_p is omitted because it is not documented for the
// OpenAI-compatible chat completions surface and requires live verification
// before being sent. The run is therefore labelled non-strict.
//
// Other providers (including OpenRouter) receive no local parity parameters
// from this preset.
func DbrainParityForProvider(provider Provider) ParityParams {
	spec, ok := defaultRegistry().Spec(provider)
	if !ok {
		return EmptyParityParams()
	}
	return DbrainParityForSpec(spec)
}

func DbrainParityForSpec(spec ProviderSpec) ParamAccounting {
	if !spec.ParamPolicy.LocalModelfileParity && !spec.ParamPolicy.OpenAICompatibleLocal {
		return EmptyParityParams()
	}
	return AccountParamsForSpec(spec, DbrainModelfilePreset())
}

func AccountParamsForSpec(spec ProviderSpec, requested map[string]any) ParamAccounting {
	if len(requested) == 0 {
		return EmptyParityParams()
	}
	out := ParamAccounting{
		Requested:  CloneAnyMap(requested),
		Sent:       map[string]any{},
		Omitted:    map[string]string{},
		Strictness: StrictnessStrict,
	}
	for key, value := range requested {
		switch key {
		case "temperature", "top_p", "top_k", "repeat_penalty":
			out.Sent[key] = value
		case "min_p":
			if spec.Transport == TransportOllamaChat {
				out.Sent[key] = value
			} else {
				out.Omitted[key] = "not verified for OpenAI-compatible chat completions"
			}
		default:
			out.Omitted[key] = "not mapped for this transport"
		}
	}
	if len(out.Omitted) > 0 {
		out.Strictness = StrictnessNonStrict
	}
	return out
}

// CloneAnyMap returns a shallow copy of the input map. Exported so other
// packages can clone inference parameter maps when building provider-specific
// request bodies.
func CloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
