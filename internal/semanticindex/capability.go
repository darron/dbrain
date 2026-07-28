package semanticindex

import "strings"

const (
	BackendUSearch = "usearch"
	USearchVersion = "2.26.0"
)

type CapabilityState string

const (
	CapabilityUnsupported     CapabilityState = "unsupported"
	CapabilitySupportedReady  CapabilityState = "supported_ready"
	CapabilitySupportedBroken CapabilityState = "supported_broken"
)

// Capability describes whether this build can safely open a persisted native
// backend generation.
type Capability struct {
	State   CapabilityState `json:"state"`
	Backend string          `json:"backend,omitempty"`
	Version string          `json:"version,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

// Admit reports whether a persisted backend generation is compatible with this
// runtime capability. Reasons are stable status codes suitable for callers.
func (c Capability) Admit(backend, version string) (bool, string) {
	switch c.State {
	case CapabilityUnsupported:
		return false, "native_backend_unsupported"
	case CapabilitySupportedBroken:
		if reason := sanitizeCapabilityReason(c.Reason); reason != "" {
			return false, "native_backend_broken: " + reason
		}
		return false, "native_backend_broken"
	case CapabilitySupportedReady:
		if c.Backend != backend || c.Version != version {
			return false, "native_backend_provenance_mismatch"
		}
		return true, ""
	default:
		return false, "native_backend_broken"
	}
}

func sanitizeCapabilityReason(reason string) string {
	fields := strings.Fields(reason)
	for index, field := range fields {
		if key, value, ok := strings.Cut(field, "="); ok && isCapabilityPath(value) {
			fields[index] = key + "=[path]"
		} else if isCapabilityPath(field) {
			fields[index] = "[path]"
		}
	}
	return strings.Join(fields, " ")
}

func isCapabilityPath(value string) bool {
	value = strings.Trim(value, ".,;:()[]{}")
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `\??\`) || strings.HasPrefix(value, "file://") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}
