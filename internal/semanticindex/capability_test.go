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
		{"broken path after contraction is redacted", Capability{State: CapabilitySupportedBroken, Reason: `can't open /private/index`}, BackendUSearch, USearchVersion, false, "native_backend_broken: can't open [path]"},
		{"broken dlopen embedded path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `dlopen(/Users/alice/private/libusearch_c.dylib, 0x0001): image not found`}, BackendUSearch, USearchVersion, false, "native_backend_broken: dlopen([path], 0x0001): image not found"},
		{"broken open embedded path with spaces is redacted", Capability{State: CapabilitySupportedBroken, Reason: `open(/private/tmp/native library.dylib): failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: open([path]): failed"},
		{"broken nested quotes and brackets path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `dlopen(["/Users/alice/private/lib usearch.dylib"], 0x0001): image not found`}, BackendUSearch, USearchVersion, false, "native_backend_broken: dlopen([\"[path]\"], 0x0001): image not found"},
		{"broken path before closing punctuation is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load /Users/alice/private/libusearch_c.dylib, diagnostic=missing: image not found`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path], diagnostic=missing: image not found"},
		{"broken file URI is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file:///Users/alice/private/lib.dylib failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken keyed file URI is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load path=file:///Users/alice/private/lib.dylib diagnostic=missing`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load path=[path] diagnostic=missing"},
		{"broken parenthesized file URI is redacted", Capability{State: CapabilitySupportedBroken, Reason: `open(file:///Users/alice/private/lib.dylib): failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: open([path]): failed"},
		{"broken Windows file URI is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file:///C:/Users/alice/private.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken Win32 extended path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \\?\C:\Users\alice\private.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken Win32 device path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \\.\C:\Users\alice\private.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken hidden POSIX path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load /.hidden/private.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken volume POSIX path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load /.vol/private.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken rooted NT device path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \Device\HarddiskVolume3\Users\alice\private\libusearch_c.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken rooted Windows path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \Users\alice\private\libusearch_c.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken NT namespace path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \??\C:\private\libusearch_c.dll failed`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] failed"},
		{"broken UNC path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load file=\\server\share\index diagnostic=ABI-mismatch`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load file=[path] diagnostic=ABI-mismatch"},
		{"broken bare UNC path is redacted", Capability{State: CapabilitySupportedBroken, Reason: `load \\server\share\index diagnostic=permission-denied`}, BackendUSearch, USearchVersion, false, "native_backend_broken: load [path] diagnostic=permission-denied"},
		{"safe slash punctuation is preserved", Capability{State: CapabilitySupportedBroken, Reason: `expected cosine / inner-product`}, BackendUSearch, USearchVersion, false, "native_backend_broken: expected cosine / inner-product"},
		{"safe escaped text is preserved", Capability{State: CapabilitySupportedBroken, Reason: `invalid escape \n`}, BackendUSearch, USearchVersion, false, `native_backend_broken: invalid escape \n`},
		{"safe escaped sequence is preserved", Capability{State: CapabilitySupportedBroken, Reason: `invalid escapes \n\t`}, BackendUSearch, USearchVersion, false, `native_backend_broken: invalid escapes \n\t`},
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
