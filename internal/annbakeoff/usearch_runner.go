//go:build usearch && cgo

package annbakeoff

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/semanticindex"
)

const (
	defaultUSearchConnectivity    = 16
	defaultUSearchExpansionAdd    = 128
	defaultUSearchExpansionSearch = 128
)

// RunUSearch evaluates only the optional native candidate. It is unavailable
// from ordinary builds and has no connection to the semantic serving path.
func RunUSearch(ctx context.Context, opts Options) (Report, error) {
	parameters, err := usearchParameters(opts)
	if err != nil {
		return Report{}, err
	}
	return RunWith(ctx, opts, semanticindex.BackendUSearch, parameters, func(opts Options) (Index, error) {
		return semanticindex.NewUSearch(semanticindex.USearchOptions{
			Dimensions:      opts.Dimensions,
			Connectivity:    parameters["connectivity"],
			ExpansionAdd:    parameters["expansion_add"],
			ExpansionSearch: parameters["expansion_search"],
		})
	})
}

func usearchParameters(opts Options) (map[string]int, error) {
	if opts.Connectivity < 0 || opts.ExpansionAdd < 0 || opts.ExpansionSearch < 0 {
		return nil, fmt.Errorf("usearch parameters cannot be negative")
	}
	connectivity := opts.Connectivity
	if connectivity == 0 {
		connectivity = defaultUSearchConnectivity
	}
	expansionAdd := opts.ExpansionAdd
	if expansionAdd == 0 {
		expansionAdd = defaultUSearchExpansionAdd
	}
	expansionSearch := opts.ExpansionSearch
	if expansionSearch == 0 {
		expansionSearch = defaultUSearchExpansionSearch
	}
	return map[string]int{
		"connectivity":     connectivity,
		"expansion_add":    expansionAdd,
		"expansion_search": expansionSearch,
	}, nil
}
