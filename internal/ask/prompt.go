package ask

import (
	"os"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/summarizecli"
)

const answerPrompt = `Answer the user's question using only the provided evidence from a local second-brain.
Requirements:
- Be concise and factual.
- Cite claims inline using [source_key].
- If the evidence is insufficient, say so clearly.
- Prefer the strongest and most directly relevant evidence.
- End with a "Sources" section listing each cited source_key, title, and note path.`

func writePromptInput(cfg config.Config, question string, evidence []Evidence) (string, func(), error) {
	var b strings.Builder
	b.WriteString("# Question\n\n")
	b.WriteString(question)
	b.WriteString("\n")

	b.WriteString("\n# Evidence\n")
	for _, doc := range evidence {
		b.WriteString("\n## ")
		b.WriteString(doc.SourceKey)
		b.WriteString("\n\n")
		b.WriteString("- Kind: ")
		b.WriteString(doc.Kind)
		b.WriteString("\n")
		if doc.SourceType != "" {
			b.WriteString("- Source type: ")
			b.WriteString(doc.SourceType)
			b.WriteString("\n")
		}
		if doc.Title != "" {
			b.WriteString("- Title: ")
			b.WriteString(doc.Title)
			b.WriteString("\n")
		}
		if doc.Author != "" {
			b.WriteString("- Author: ")
			b.WriteString(doc.Author)
			b.WriteString("\n")
		}
		if doc.UserTags != "" {
			b.WriteString("- User tags: ")
			b.WriteString(doc.UserTags)
			b.WriteString("\n")
		}
		if doc.PublishedAt != "" {
			b.WriteString("- Published at: ")
			b.WriteString(doc.PublishedAt)
			b.WriteString("\n")
		}
		if doc.ExtractedAt != "" {
			b.WriteString("- Extracted at: ")
			b.WriteString(doc.ExtractedAt)
			b.WriteString("\n")
		}
		if doc.SummarizedAt != "" {
			b.WriteString("- Summarized at: ")
			b.WriteString(doc.SummarizedAt)
			b.WriteString("\n")
		}
		if len(doc.EntityMatches) > 0 {
			b.WriteString("- Entity matches: ")
			b.WriteString(strings.Join(doc.EntityMatches, ", "))
			b.WriteString("\n")
		}
		if doc.Relationship != "" {
			b.WriteString("- Relationship: ")
			b.WriteString(doc.Relationship)
			if doc.RelatedTo != "" {
				b.WriteString(" (")
				b.WriteString(doc.RelatedTo)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		b.WriteString("- URL: ")
		b.WriteString(doc.URL)
		b.WriteString("\n")
		b.WriteString("- Note: ")
		b.WriteString(doc.NotePath)
		b.WriteString("\n")
		if strings.TrimSpace(doc.Summary) != "" {
			b.WriteString("\n### Summary\n\n")
			b.WriteString(strings.TrimSpace(doc.Summary))
			b.WriteString("\n")
		}
		if strings.TrimSpace(doc.Excerpt) != "" {
			b.WriteString("\n### Excerpt\n\n")
			b.WriteString(strings.TrimSpace(doc.Excerpt))
			b.WriteString("\n")
		}
	}

	file, err := cfg.CreateTemp("dbrain-ask-*.md")
	if err != nil {
		return "", nil, err
	}
	if _, err := file.WriteString(b.String()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, nil
}

func askCLI(opts Options) string {
	return summarizecli.ResolveCLIProvider(opts.CLI, opts.Model)
}
