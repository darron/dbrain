package vault

import "strings"

func writeYAMLScalar(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(yamlQuote(value))
	b.WriteString("\n")
}

func writeYAMLArray(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		b.WriteString(key)
		b.WriteString(": []\n")
		return
	}
	b.WriteString(key)
	b.WriteString(":\n")
	for _, value := range values {
		b.WriteString("  - ")
		b.WriteString(yamlQuote(value))
		b.WriteString("\n")
	}
}

func yamlQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
