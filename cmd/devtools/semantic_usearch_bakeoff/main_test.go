//go:build usearch && cgo

package main

import (
	"context"
	"testing"
)

func TestExecuteRequiresReportPath(t *testing.T) {
	if err := execute(context.Background(), nil); err == nil {
		t.Fatal("expected --report to be required")
	}
}
