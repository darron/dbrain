package sourceenrich

import (
	"os"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

const SummaryPromptVersion = "dbrain-v1"

const summaryPrompt = `Summarize this source for a local second-brain knowledge base.
Focus on durable knowledge, concrete facts, named entities, tools, libraries, APIs, claims, and actionable takeaways.
Use only the provided extracted text and explicit source metadata below.
If the extract is partial, teaser text, or truncated, say that plainly and summarize only what is actually present.
Do not infer facts, timelines, citations, linked sources, or entities that are not explicitly stated in the extracted text.
Do not use outside knowledge.
Preserve source framing. If the piece is opinion, satire, irony, marketing, advocacy, or a personal essay, say so explicitly.
Attribute subjective, speculative, promotional, or self-reported claims to the author or source. Do not rewrite claims as established fact.
If the source is a walkthrough, guide, or pitch, summarize it as a walkthrough, guide, or pitch rather than as neutral documentation.
Use Markdown with exactly these headings:
### What It Is
### Key Ideas
### Why It Matters
### Entities
### Follow-ups
Keep it factual and concise.
Use bullets only in Entities and Follow-ups.
Do not mention ads, sponsors, or irrelevant boilerplate.`

func buildSummaryPrompt(source model.SourceDocument, extract model.ExtractResult) string {
	var b strings.Builder
	b.WriteString(summaryPrompt)

	contextLines := make([]string, 0, 3)
	if value := strings.TrimSpace(source.CanonicalURL); value != "" {
		contextLines = append(contextLines, "Source URL: "+value)
	}
	title := strings.TrimSpace(extract.Title)
	if title == "" {
		title = strings.TrimSpace(source.Title)
	}
	if title != "" {
		contextLines = append(contextLines, "Source Title: "+title)
	}
	site := strings.TrimSpace(extract.SiteName)
	if site == "" {
		site = strings.TrimSpace(source.SiteName)
	}
	if site == "" {
		site = strings.TrimSpace(source.Domain)
	}
	if site != "" {
		contextLines = append(contextLines, "Source Site: "+site)
	}

	if len(contextLines) == 0 {
		return b.String()
	}

	b.WriteString("\n\nAdditional context:\n")
	for _, line := range contextLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
}

func summaryInputFile(cfg config.Config, extract model.ExtractResult) (string, func(), error) {
	input := summaryInput(extract)
	if strings.TrimSpace(input) == "" {
		return "", func() {}, nil
	}

	file, err := cfg.CreateTemp("dbrain-summary-*.md")
	if err != nil {
		return "", nil, err
	}
	if _, err := file.WriteString(input); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() {
		_ = os.Remove(file.Name())
	}, nil
}

func summaryInput(extract model.ExtractResult) string {
	content := strings.TrimSpace(extract.Content)
	if content == "" {
		return ""
	}
	parts := make([]string, 0, 4)
	if title := strings.TrimSpace(extract.Title); title != "" {
		parts = append(parts, "Title: "+title)
	}
	if description := strings.TrimSpace(extract.Description); description != "" {
		parts = append(parts, "Description: "+description)
	}
	if siteName := strings.TrimSpace(extract.SiteName); siteName != "" {
		parts = append(parts, "Site: "+siteName)
	}
	parts = append(parts, content)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
