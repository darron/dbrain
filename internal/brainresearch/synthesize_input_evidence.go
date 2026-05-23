package brainresearch

import (
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

func splitPrimaryAndRelated(docs []ask.Evidence) ([]ask.Evidence, []ask.Evidence) {
	primary := make([]ask.Evidence, 0, len(docs))
	related := make([]ask.Evidence, 0)
	for _, doc := range docs {
		if strings.TrimSpace(doc.RelatedTo) != "" || strings.TrimSpace(doc.Relationship) != "" {
			related = append(related, doc)
		} else {
			primary = append(primary, doc)
		}
	}
	return primary, related
}

func evidenceChunk(doc ask.Evidence) string {
	text := strings.TrimSpace(doc.Summary)
	textKind := "summary"
	if text == "" {
		text = strings.TrimSpace(doc.Excerpt)
		textKind = "excerpt"
	}
	if text == "" && len(doc.ContentSections) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("- source_key: ")
	b.WriteString(doc.SourceKey)
	b.WriteString("\n  title: ")
	b.WriteString(doc.Title)
	b.WriteString("\n  kind: ")
	b.WriteString(doc.Kind)
	if doc.SourceType != "" {
		b.WriteString("\n  source_type: ")
		b.WriteString(doc.SourceType)
	}
	if doc.URL != "" {
		b.WriteString("\n  url: ")
		b.WriteString(doc.URL)
	}
	if doc.NotePath != "" {
		b.WriteString("\n  note_path: ")
		b.WriteString(doc.NotePath)
	}
	if doc.UserTags != "" {
		b.WriteString("\n  user_tags: ")
		b.WriteString(doc.UserTags)
	}
	if doc.EvidenceRole != "" {
		b.WriteString("\n  evidence_role: ")
		b.WriteString(doc.EvidenceRole)
	}
	if doc.Chunk != nil {
		b.WriteString("\n  chunk:")
		b.WriteString("\n    role: ")
		b.WriteString(doc.Chunk.Role)
		b.WriteString("\n    index: ")
		b.WriteString(strconv.Itoa(doc.Chunk.Index))
		if doc.Chunk.Hash != "" {
			b.WriteString("\n    hash: ")
			b.WriteString(doc.Chunk.Hash)
		}
		if doc.Chunk.Heading != "" {
			b.WriteString("\n    heading: ")
			b.WriteString(doc.Chunk.Heading)
		}
	}
	if doc.Relationship != "" {
		b.WriteString("\n  relationship: ")
		b.WriteString(doc.Relationship)
		if doc.RelatedTo != "" {
			b.WriteString(" (")
			b.WriteString(doc.RelatedTo)
			b.WriteString(")")
		}
	}
	if len(doc.ContentSections) > 0 {
		b.WriteString("\n  content_sections:")
		for _, section := range doc.ContentSections {
			if strings.TrimSpace(section.Text) == "" {
				continue
			}
			b.WriteString("\n    - name: ")
			b.WriteString(section.Name)
			b.WriteString("\n      role: ")
			b.WriteString(section.Role)
			if section.Status != "" {
				b.WriteString("\n      status: ")
				b.WriteString(section.Status)
			}
			if section.Model != "" {
				b.WriteString("\n      model: ")
				b.WriteString(section.Model)
			}
			if section.Tool != "" {
				b.WriteString("\n      tool: ")
				b.WriteString(section.Tool)
			}
			b.WriteString("\n      text: |\n")
			for _, line := range strings.Split(section.Text, "\n") {
				b.WriteString("        ")
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
		return b.String()
	}
	b.WriteString("\n  ")
	b.WriteString(textKind)
	b.WriteString(": |\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
