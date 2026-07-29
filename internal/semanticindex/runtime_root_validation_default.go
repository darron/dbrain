//go:build !usearch || !cgo

package semanticindex

import (
	"context"
	"errors"
)

// ValidateUSearchRuntimeRoot is unavailable in builds without the optional
// native backend. Untagged status stays unsupported before this can be called.
func ValidateUSearchRuntimeRoot(context.Context, string, string, string, string, int, int64, int64, string, string) error {
	return errors.New("native root validation unavailable")
}
