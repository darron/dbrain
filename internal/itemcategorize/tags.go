package itemcategorize

import "strings"

// MergeUserTags appends categorizer output to existing comma-separated
// user_tags while preserving the existing order and removing duplicates.
func MergeUserTags(existing string, r Result) string {
	seen := map[string]struct{}{}
	var parts []string
	candidates := append(splitTags(existing), r.Tags...)
	candidates = append(candidates, r.Categories...)
	for _, t := range candidates {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		parts = append(parts, t)
	}
	return strings.Join(parts, ",")
}

func splitTags(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
