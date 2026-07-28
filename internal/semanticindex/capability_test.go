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
		{"broken keyed absolute path is redacted", Capability{State: CapabilitySupportedBroken, Reason: "open key=/absolute/path diagnostic=ABI-mismatch"}, BackendUSearch, USearchVersion, false, "native_backend_broken: open key=[path] diagnostic=ABI-mismatch"},
		{"broken file path is redacted", Capability{State: CapabilitySupportedBroken, Reason: "load file=/tmp/usearch/index diagnostic=permission-denied"}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file=[path] diagnostic=permission-denied"},
		{"broken quoted POSIX path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file="/tmp/usearch/index" diagnostic=permission-denied`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file=\"[path]\" diagnostic=permission-denied"},
		{"broken quoted Windows path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file='C:\\private\\index' diagnostic=ABI-mismatch`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file='[path]' diagnostic=ABI-mismatch"},
		{"broken quoted POSIX path with spaces is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file="/tmp/usearch data/index" diagnostic=permission-denied`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file=\"[path]\" diagnostic=permission-denied"},
		{"broken quoted Windows path with spaces is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file='C:\\private data\\index' diagnostic=ABI-mismatch`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file='[path]' diagnostic=ABI-mismatch"},
		{"broken unmatched quoted path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file="/tmp/usearch data/index diagnostic=permission-denied`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file=[path]"},
		{"broken UNC path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file=\\server\share\index diagnostic=ABI-mismatch`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file=[path] diagnostic=ABI-mismatch"},
		{"broken bare UNC path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \\server\share\index diagnostic=permission-denied`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] diagnostic=permission-denied"},
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
