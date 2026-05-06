package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
)

func renderChatTranscriptMarkdown(req ChatTranscriptSaveRequest, savedAt time.Time) string {
	var b strings.Builder
	b.WriteString("# dbrain chat transcript\n\n")
	b.WriteString("Saved: ")
	b.WriteString(savedAt.Format(time.RFC3339))
	b.WriteString("\n\n")
	b.WriteString("Scope: diagnostic export only; this file is not indexed into dbrain retrieval unless imported separately.\n\n")

	if selected := strings.TrimSpace(req.SelectedLookup); selected != "" {
		b.WriteString("Selected lookup: `")
		b.WriteString(selected)
		b.WriteString("`\n\n")
	}
	if len(req.PinnedEvidenceKeys) > 0 {
		b.WriteString("Pinned evidence:\n")
		for _, key := range req.PinnedEvidenceKeys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			b.WriteString("- `")
			b.WriteString(key)
			b.WriteString("`\n")
		}
		b.WriteByte('\n')
	}

	for i, turn := range req.Turns {
		b.WriteString("## Turn ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("\n\n")
		if value := strings.TrimSpace(turn.ID); value != "" {
			b.WriteString("ID: `")
			b.WriteString(value)
			b.WriteString("`\n\n")
		}
		if value := strings.TrimSpace(turn.CreatedAt); value != "" {
			b.WriteString("Created: ")
			b.WriteString(value)
			b.WriteString("\n\n")
		}
		if value := strings.TrimSpace(turn.Status); value != "" {
			b.WriteString("Status: `")
			b.WriteString(value)
			b.WriteString("`\n\n")
		}
		if value := strings.TrimSpace(turn.Error); value != "" {
			b.WriteString("Error: ")
			b.WriteString(value)
			b.WriteString("\n\n")
		}

		b.WriteString("### Question\n\n")
		b.WriteString(truncateTranscriptText(turn.Question, 16000))
		b.WriteString("\n\n")

		if value := strings.TrimSpace(turn.RetrievalQuestion); value != "" {
			b.WriteString("### Retrieval Query\n\n```text\n")
			b.WriteString(truncateTranscriptText(value, 16000))
			b.WriteString("\n```\n\n")
		}

		if value := strings.TrimSpace(turn.Answer); value != "" {
			b.WriteString("### Answer\n\n")
			b.WriteString(truncateTranscriptText(value, 64000))
			b.WriteString("\n\n")
		}

		if len(turn.Citations) > 0 {
			b.WriteString("### Citations\n\n")
			for _, citation := range turn.Citations {
				writeTranscriptCitation(&b, citation)
			}
			b.WriteByte('\n')
		}

		writeTranscriptResearchPack(&b, turn.ResearchPack)
	}

	return b.String()
}

func writeTranscriptCitation(b *strings.Builder, citation brainresearch.Citation) {
	sourceKey := strings.TrimSpace(citation.SourceKey)
	if sourceKey == "" {
		return
	}
	b.WriteString("- `")
	b.WriteString(sourceKey)
	b.WriteString("`")
	if title := strings.TrimSpace(citation.Title); title != "" {
		b.WriteString(" - ")
		b.WriteString(title)
	}
	if citation.Kind != "" || citation.NotePath != "" || citation.URL != "" {
		b.WriteString(" (")
		parts := make([]string, 0, 3)
		if value := strings.TrimSpace(citation.Kind); value != "" {
			parts = append(parts, value)
		}
		if value := strings.TrimSpace(citation.NotePath); value != "" {
			parts = append(parts, value)
		}
		if value := strings.TrimSpace(citation.URL); value != "" {
			parts = append(parts, value)
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString(")")
	}
	b.WriteByte('\n')
}
