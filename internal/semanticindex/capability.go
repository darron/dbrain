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
	var sanitized strings.Builder
	sanitized.Grow(len(reason))
	for index := 0; index < len(reason); {
		if (reason[index] == '\'' || reason[index] == '"') && index+1 < len(reason) {
			if end, ok := capabilityPathEnd(reason, index+1); ok && end == len(reason) {
				sanitized.WriteString("[path]")
				index = end
				continue
			}
		}
		if end, ok := capabilityPathEnd(reason, index); ok {
			sanitized.WriteString("[path]")
			index = end
			continue
		}
		sanitized.WriteByte(reason[index])
		index++
	}
	return sanitized.String()
}

func capabilityPathEnd(reason string, index int) (int, bool) {
	if !isCapabilityPathStart(reason, index) || !isCapabilityPathBoundary(reason, index) {
		return 0, false
	}

	if quote := capabilityPathQuote(reason, index); quote != 0 {
		for end := index; end < len(reason); end++ {
			if reason[end] == quote {
				return end, true
			}
		}
		return len(reason), true
	}

	allowSpaces := index > 0 && reason[index-1] == '('
	for end := index; end < len(reason); end++ {
		if !allowSpaces && isCapabilityWhitespace(reason[end]) {
			return end, true
		}
		if isCapabilityPathTerminator(reason, index, end) {
			return end, true
		}
	}
	return len(reason), true
}

func isCapabilityPathStart(reason string, index int) bool {
	value := reason[index:]
	if isCapabilitySlashPath(value) || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `\??\`) || strings.HasPrefix(value, `\Device\`) || isCapabilityRootedWindowsPath(value) || strings.HasPrefix(value, "file://") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func isCapabilitySlashPath(value string) bool {
	if !strings.HasPrefix(value, "/") || len(value) < 2 {
		return false
	}
	return !isCapabilityWhitespace(value[1])
}

func isCapabilityRootedWindowsPath(value string) bool {
	if !strings.HasPrefix(value, `\`) || len(value) < 3 || isCapabilityWhitespace(value[1]) {
		return false
	}
	if isCapabilityEscapeSequence(value) {
		return false
	}
	return strings.IndexByte(value[1:], '\\') > 0
}

func isCapabilityEscapeSequence(value string) bool {
	escapes := strings.Split(value[1:], `\`)
	if len(escapes) < 2 {
		return false
	}
	for _, escape := range escapes {
		if len(escape) != 1 || !strings.ContainsRune("abfnrtv0", rune(escape[0])) {
			return false
		}
	}
	return true
}

func isCapabilityPathBoundary(reason string, index int) bool {
	if index == 0 {
		return true
	}
	switch reason[index-1] {
	case ' ', '\t', '\n', '\r', '=', '\'', '"', '(', '[', '{':
		return true
	default:
		return false
	}
}

func capabilityPathQuote(reason string, index int) byte {
	if index > 0 && (reason[index-1] == '\'' || reason[index-1] == '"') {
		return reason[index-1]
	}
	return 0
}

func isCapabilityWhitespace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r'
}

func isCapabilityPathTerminator(reason string, start, index int) bool {
	switch reason[index] {
	case ',', ';', '(', ')', '[', ']', '{', '}', '\'', '"':
		return true
	case ':':
		if strings.HasPrefix(reason[start:], "file://") && index == start+4 {
			return false
		}
		if isCapabilityDriveColon(reason, start, index) {
			return false
		}
		return true
	default:
		return false
	}
}

func isCapabilityDriveColon(reason string, start, index int) bool {
	if index <= start || index+1 >= len(reason) || (reason[index+1] != '/' && reason[index+1] != '\\') {
		return false
	}
	return (reason[index-1] >= 'A' && reason[index-1] <= 'Z') || (reason[index-1] >= 'a' && reason[index-1] <= 'z')
}
