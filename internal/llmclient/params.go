package llmclient

import "github.com/darron/dbrain/internal/llmprovider"

func accountParams(spec llmprovider.ProviderSpec, requested map[string]any) llmprovider.ParamAccounting {
	return llmprovider.AccountParamsForSpec(spec, requested)
}
