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
	fields := capabilityDiagnosticFields(reason)
	for index, field := range fields {
		if key, value, ok := strings.Cut(field, "="); ok {
			if redacted, ok := redactCapabilityPath(value); ok {
				fields[index] = key + "=" + redacted
			}
		} else if redacted, ok := redactCapabilityPath(field); ok {
			fields[index] = redacted
		}
	}
	return strings.Join(fields, " ")
}

func redactCapabilityPath(value string) (string, bool) {
	if len(value) >= 1 && (value[0] == '\'' || value[0] == '"') {
		if len(value) >= 2 && value[len(value)-1] == value[0] && isCapabilityPath(value[1:len(value)-1]) {
			return value[:1] + "[path]" + value[len(value)-1:], true
		}
		if isCapabilityPath(value[1:]) {
			return "[path]", true
		}
	}
	if isCapabilityPath(value) {
		return "[path]", true
	}
	return "", false
}

func capabilityDiagnosticFields(reason string) []string {
	var fields []string
	for index := 0; index < len(reason); {
		for index < len(reason) && (reason[index] == ' ' || reason[index] == '\t' || reason[index] == '\n' || reason[index] == '\r') {
			index++
		}
		start := index
		var quote byte
		for index < len(reason) {
			character := reason[index]
			if quote != 0 {
				index++
				if character == quote {
					quote = 0
				}
				continue
			}
			if (character == '\'' || character == '"') && (index == start || reason[index-1] == '=') {
				quote = character
				index++
				continue
			}
			if character == ' ' || character == '\t' || character == '\n' || character == '\r' {
				break
			}
			index++
		}
		if start < index {
			fields = append(fields, reason[start:index])
		}
	}
	return fields
}

func isCapabilityPath(value string) bool {
	value = strings.Trim(value, ".,;:()[]{}")
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `\??\`) || strings.HasPrefix(value, "file://") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}
