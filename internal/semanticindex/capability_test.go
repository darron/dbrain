package semanticindex

import "testing"

func TestCapabilityAdmit(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		backend    string
		version    string
		wantOK     bool
		wantReason string
	}{
		{"ready", Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}, BackendUSearch, USearchVersion, true, ""},
		{"unsupported", Capability{State: CapabilityUnsupported}, BackendUSearch, USearchVersion, false, "native_backend_unsupported"},
		{"broken", Capability{State: CapabilitySupportedBroken, Backend: BackendUSearch, Version: USearchVersion, Reason: "probe failed"}, BackendUSearch, USearchVersion, false, "native_backend_broken: probe failed"},
		{"broken path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load C:\private\tmp failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"backend mismatch", Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}, "other", USearchVersion, false, "native_backend_provenance_mismatch"},
		{"version mismatch", Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}, BackendUSearch, "other", false, "native_backend_provenance_mismatch"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotOK, gotReason := test.capability.Admit(test.backend, test.version)
			if gotOK != test.wantOK || gotReason != test.wantReason {
				t.Fatalf("Admit(%q, %q) = (%t, %q), want (%t, %q)", test.backend, test.version, gotOK, gotReason, test.wantOK, test.wantReason)
			}
		})
	}
}
