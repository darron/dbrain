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
	if strings.HasPrefix(value, `\`) && isCapabilityInvalidEscapeToken(reason, index) {
		return false
	}
	if isCapabilitySlashPath(value) || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `\??\`) || strings.HasPrefix(value, `\Device\`) || isCapabilityRootedWindowsPath(value) || strings.HasPrefix(value, "file://") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func isCapabilitySlashPath(value string) bool {
	if !strings.HasPrefix(value, "/") || len(value) < 2 {
		return false
	}
	character := value[1]
	if isCapabilityWhitespace(character) {
		return false
	}
	switch character {
	case ',', ';', ':', '(', ')', '[', ']', '{', '}', '\'', '"':
		return false
	default:
		return true
	}
}

func isCapabilityRootedWindowsPath(value string) bool {
	if !strings.HasPrefix(value, `\`) || len(value) < 3 || isCapabilityWhitespace(value[1]) {
		return false
	}
	if isCapabilityEscapeSequence(value) {
		return false
	}
	if strings.IndexAny(value[1:], `\\/`) > 0 {
		return true
	}
	for end := 1; end < len(value); end++ {
		if isCapabilityRootedWindowsTerminator(value[end]) {
			return end > 2
		}
	}
	return len(value) > 2
}

func isCapabilityRootedWindowsTerminator(character byte) bool {
	if isCapabilityWhitespace(character) {
		return true
	}
	switch character {
	case ',', ';', ':', '(', ')', '[', ']', '{', '}', '\'', '"':
		return true
	default:
		return false
	}
}

func isCapabilityInvalidEscapeToken(reason string, index int) bool {
	if !hasCapabilityInvalidEscapeContext(reason[:index]) {
		return false
	}
	for position := index; position < len(reason) && reason[position] == '\\'; {
		next, ok := capabilityEscapeComponentEnd(reason, position)
		if !ok {
			return false
		}
		if next == len(reason) {
			return true
		}
		if reason[next] == '\\' {
			position = next
			continue
		}
		return isCapabilityWhitespace(reason[next]) || isCapabilityEscapeDelimiter(reason[next])
	}
	return false
}

func hasCapabilityInvalidEscapeContext(prefix string) bool {
	prefix = strings.ToLower(prefix)
	for _, marker := range []string{
		"invalid escape ", "invalid escapes ",
		"invalid escape:", "invalid escapes:",
		"invalid escape sequence ", "invalid escape sequences ",
		"invalid escape sequence:", "invalid escape sequences:",
	} {
		if strings.LastIndex(prefix, marker) >= 0 {
			return true
		}
	}
	return false
}

func capabilityEscapeComponentEnd(value string, start int) (int, bool) {
	if start+1 >= len(value) || value[start] != '\\' {
		return 0, false
	}
	character := value[start+1]
	switch character {
	case 'x':
		return capabilityBoundedEscapeEnd(value, start, 2, false)
	case 'u':
		return capabilityBoundedEscapeEnd(value, start, 4, false)
	case 'U':
		return capabilityBoundedEscapeEnd(value, start, 8, true)
	default:
		if character >= '0' && character <= '7' {
			end := start + 2
			for end < len(value) && end < start+4 && value[end] >= '0' && value[end] <= '7' {
				end++
			}
			return end, true
		}
		return start + 2, true
	}
}

func capabilityBoundedEscapeEnd(value string, start, maximum int, exact bool) (int, bool) {
	payloadStart := start + 2
	end := payloadStart
	for end < len(value) && end-payloadStart < maximum {
		character := value[end]
		if character == '\\' || character == '/' || isCapabilityWhitespace(character) || isCapabilityEscapeDelimiter(character) {
			break
		}
		end++
	}
	if exact {
		return end, end-payloadStart == maximum
	}
	return end, end > payloadStart
}

func isCapabilityEscapeSequence(value string) bool {
	for index := 0; index+1 < len(value) && value[index] == '\\'; index += 2 {
		if !strings.ContainsRune("abfnrtv0", rune(value[index+1])) {
			return false
		}
		if index+2 == len(value) {
			return true
		}
		if value[index+2] != '\\' {
			return isCapabilityWhitespace(value[index+2]) || isCapabilityEscapeDelimiter(value[index+2])
		}
	}
	return false
}

func isCapabilityEscapeDelimiter(character byte) bool {
	switch character {
	case '.', ',', ';', ':', '?', '(', ')', '[', ']', '{', '}', '\'', '"':
		return true
	default:
		return false
	}
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
