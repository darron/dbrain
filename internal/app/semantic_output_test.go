package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/semanticbuild"
)

func TestSemanticProgressOutputIncludesQuarantined(t *testing.T) {
	progress := semanticbuild.Progress{Stage: "embed", Scanned: 2, Quarantined: 1}
	for name, write := range map[string]func(*bytes.Buffer) error{
		"snapshot": func(dst *bytes.Buffer) error { return writeSemanticProgressSnapshot(dst, progress) },
		"final":    func(dst *bytes.Buffer) error { return writeSemanticProgress(dst, progress) },
	} {
		t.Run(name, func(t *testing.T) {
			var dst bytes.Buffer
			if err := write(&dst); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(dst.String()), "quarantined") || !strings.Contains(dst.String(), "1") {
				t.Fatalf("progress output = %q, want quarantine count", dst.String())
			}
		})
	}
}
