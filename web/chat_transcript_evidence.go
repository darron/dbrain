package web

import (
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

func writeTranscriptEvidenceSection(b *strings.Builder, heading string, evidence []ask.Evidence) {
	if len(evidence) == 0 {
		return
	}
	b.WriteString("### ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, row := range evidence {
		sourceKey := strings.TrimSpace(row.SourceKey)
		if sourceKey == "" {
			continue
		}
		b.WriteString("- `")
		b.WriteString(sourceKey)
		b.WriteString("`")
		if title := strings.TrimSpace(row.Title); title != "" {
			b.WriteString(" - ")
			b.WriteString(title)
		}
		b.WriteByte('\n')
		if meta := evidenceMeta(row); meta != "" {
			b.WriteString("  - ")
			b.WriteString(meta)
			b.WriteByte('\n')
		}
		if summary := strings.TrimSpace(row.Summary); summary != "" {
			b.WriteString("  - summary: ")
			b.WriteString(truncateTranscriptText(summary, 2000))
			b.WriteByte('\n')
		}
		if excerpt := strings.TrimSpace(row.Excerpt); excerpt != "" {
			b.WriteString("  - excerpt: ")
			b.WriteString(truncateTranscriptText(excerpt, 2000))
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
}

func evidenceMeta(row ask.Evidence) string {
	parts := make([]string, 0, 6)
	if value := strings.TrimSpace(row.Kind); value != "" {
		parts = append(parts, "kind="+value)
	}
	if value := strings.TrimSpace(row.SourceType); value != "" {
		parts = append(parts, "type="+value)
	}
	if value := strings.TrimSpace(row.RelatedTo); value != "" {
		parts = append(parts, "related_to="+value)
	}
	if value := strings.TrimSpace(row.Relationship); value != "" {
		parts = append(parts, "relationship="+value)
	}
	if value := strings.TrimSpace(row.NotePath); value != "" {
		parts = append(parts, "note="+value)
	}
	if value := strings.TrimSpace(row.URL); value != "" {
		parts = append(parts, "url="+value)
	}
	return strings.Join(parts, "; ")
}
