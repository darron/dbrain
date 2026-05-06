package mcpserver

import (
	"fmt"
	"strings"
)

func formatGetPayload(payload map[string]interface{}) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "[%s] %s\n", payload["source_key"], payload["title"])
	if url, _ := payload["url"].(string); strings.TrimSpace(url) != "" {
		b.WriteString("URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if note, _ := payload["note"].(string); strings.TrimSpace(note) != "" {
		b.WriteString("Note: ")
		b.WriteString(note)
		b.WriteString("\n")
	}
	b.WriteString("Content mode: ")
	b.WriteString(payload["content_mode"].(string))
	b.WriteString("\n")
	if query, _ := payload["query"].(string); strings.TrimSpace(query) != "" {
		b.WriteString("Query: ")
		b.WriteString(strings.TrimSpace(query))
		b.WriteString("\n")
	}

	if item, ok := payload["item"].(map[string]interface{}); ok {
		if tags, _ := item["user_tags"].(string); strings.TrimSpace(tags) != "" {
			b.WriteString("User tags: ")
			b.WriteString(strings.TrimSpace(tags))
			b.WriteString("\n")
		}
	}
	if source, ok := payload["source"].(map[string]interface{}); ok {
		if tags, _ := source["user_tags"].(string); strings.TrimSpace(tags) != "" {
			b.WriteString("User tags: ")
			b.WriteString(strings.TrimSpace(tags))
			b.WriteString("\n")
		}
	}

	sections, _ := payload["content_sections"].([]getSection)
	if len(sections) == 0 {
		b.WriteString("\nNo content sections returned. Use content_mode=\"evidence\", \"raw\", or \"rendered\" for content.\n")
		return b.String()
	}
	for _, section := range sections {
		b.WriteString("\n## ")
		b.WriteString(section.Name)
		b.WriteString(" (")
		b.WriteString(section.Role)
		_, _ = fmt.Fprintf(&b, ", chars=%d", section.Chars)
		if section.Truncated {
			b.WriteString(", truncated")
		}
		b.WriteString(")\n")
		b.WriteString(section.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func formatGetManyPayload(payload map[string]interface{}, texts []string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Fetched %d of %d lookups", payload["count"], len(payload["lookups"].([]string)))
	if errors, ok := payload["errors"].([]getManyError); ok && len(errors) > 0 {
		_, _ = fmt.Fprintf(&b, " (%d errors)", len(errors))
	}
	b.WriteString(".\n")
	if query, _ := payload["query"].(string); strings.TrimSpace(query) != "" {
		b.WriteString("Query: ")
		b.WriteString(strings.TrimSpace(query))
		b.WriteString("\n")
	}
	if len(texts) > 0 {
		for i, text := range texts {
			if i > 0 {
				b.WriteString("\n---\n")
			}
			b.WriteString(strings.TrimSpace(text))
			b.WriteString("\n")
		}
	}
	if errors, ok := payload["errors"].([]getManyError); ok && len(errors) > 0 {
		b.WriteString("\nErrors:\n")
		for _, entry := range errors {
			b.WriteString("- ")
			b.WriteString(entry.Lookup)
			b.WriteString(": ")
			b.WriteString(entry.Error)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func uniqueGetLookups(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
