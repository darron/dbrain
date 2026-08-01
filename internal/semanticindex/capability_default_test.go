//go:build !usearch || !cgo

package semanticindex

import (
	"encoding/json"
	"testing"
)

func TestRuntimeCapabilityDefault(t *testing.T) {
	capability := RuntimeCapability()
	if capability.State != CapabilityUnsupported {
		t.Fatalf("RuntimeCapability().State = %q, want %q", capability.State, CapabilityUnsupported)
	}

	payload, err := json.Marshal(capability)
	if err != nil {
		t.Fatalf("marshal capability: %v", err)
	}
	if got, want := string(payload), `{"state":"unsupported"}`; got != want {
		t.Fatalf("marshal capability = %s, want %s", got, want)
	}
}
