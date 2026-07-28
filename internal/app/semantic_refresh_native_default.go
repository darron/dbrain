//go:build !usearch || !cgo

package app

import (
	"fmt"

	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticrefresh"
)

func newSemanticNativeLifecycle(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
	return nil, fmt.Errorf("native semantic lifecycle is unavailable in this build")
}
