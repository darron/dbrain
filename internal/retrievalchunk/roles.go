package retrievalchunk

import "strings"

// IsRawSupportRole reports whether a projected section contains source evidence
// rather than derived synthesis. OCR and transcript remain distinct roles so
// changing support classification does not change chunk identity provenance.
func IsRawSupportRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return strings.HasPrefix(role, "raw") || role == "ocr" || role == "transcript"
}
