package vault

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func writeSourceFrontmatter(b *strings.Builder, source model.SourceDocument) {
	b.WriteString("---\n")
	writeYAMLScalar(b, "brain_source_key", source.SourceKey)
	writeYAMLScalar(b, "source_type", source.SourceType)
	writeYAMLScalar(b, "canonical_url", source.CanonicalURL)
	writeYAMLScalar(b, "normalized_url", source.NormalizedURL)
	writeYAMLScalar(b, "domain", source.Domain)
	writeYAMLScalar(b, "title", source.Title)
	writeYAMLScalar(b, "site_name", source.SiteName)
	writeYAMLScalar(b, "extract_status", source.ExtractStatus)
	writeYAMLScalar(b, "extracted_at", formatTime(source.ExtractedAt))
	writeYAMLScalar(b, "extract_tool", source.ExtractTool)
	writeYAMLScalar(b, "extract_tool_version", source.ExtractToolVersion)
	writeYAMLScalar(b, "summary_status", source.SummaryStatus)
	writeYAMLScalar(b, "summary_model", source.SummaryModel)
	writeYAMLScalar(b, "summary_prompt_version", source.SummaryPromptVersion)
	writeYAMLScalar(b, "summary_tool", source.SummaryTool)
	writeYAMLScalar(b, "summary_tool_version", source.SummaryToolVersion)
	writeYAMLScalar(b, "summarized_at", formatTime(source.SummarizedAt))
	writeYAMLScalar(b, "user_tags", source.UserTags)
	writeYAMLArray(b, "tags", []string{"source/link", "source/" + source.SourceType})
	b.WriteString("---\n\n")
}

func writeSourceDetailsSection(b *strings.Builder, source model.SourceDocument) {
	b.WriteString("## Source\n\n")
	b.WriteString("- URL: ")
	b.WriteString(source.CanonicalURL)
	b.WriteString("\n")
	if source.Domain != "" {
		b.WriteString("- Domain: `")
		b.WriteString(source.Domain)
		b.WriteString("`\n")
	}
	if source.SiteName != "" {
		b.WriteString("- Site: ")
		b.WriteString(source.SiteName)
		b.WriteString("\n")
	}
	if strings.TrimSpace(source.UserTags) != "" {
		b.WriteString("- User tags: ")
		b.WriteString(strings.TrimSpace(source.UserTags))
		b.WriteString("\n")
	}
	if source.ExtractStatus != "" {
		b.WriteString("- Extract status: `")
		b.WriteString(source.ExtractStatus)
		b.WriteString("`\n")
	}
	if source.ExtractFailureCount > 0 || strings.TrimSpace(source.ExtractFailureKind) != "" {
		b.WriteString("- Extract failures: ")
		if source.ExtractFailureCount > 0 {
			_, _ = fmt.Fprintf(b, "%d", source.ExtractFailureCount)
		} else {
			b.WriteString("unknown")
		}
		if source.ExtractFailureKind != "" {
			b.WriteString(" (`")
			b.WriteString(source.ExtractFailureKind)
			b.WriteString("`)")
		}
		if !source.ExtractFirstFailedAt.IsZero() || !source.ExtractLastFailedAt.IsZero() {
			b.WriteString(" from ")
			b.WriteString(formatTime(source.ExtractFirstFailedAt))
			b.WriteString(" to ")
			b.WriteString(formatTime(source.ExtractLastFailedAt))
		}
		b.WriteString("\n")
	}
	if !source.ExtractedAt.IsZero() {
		b.WriteString("- Extracted at: ")
		b.WriteString(formatTime(source.ExtractedAt))
		b.WriteString("\n")
	}
	if source.ExtractTool != "" || source.ExtractToolVersion != "" {
		b.WriteString("- Extracted with: `")
		b.WriteString(strings.TrimSpace(strings.Join([]string{source.ExtractTool, source.ExtractToolVersion}, " ")))
		b.WriteString("`\n")
	}
	if source.SummaryStatus != "" {
		b.WriteString("- Summary status: `")
		b.WriteString(source.SummaryStatus)
		b.WriteString("`\n")
	}
	if source.SummaryModel != "" {
		b.WriteString("- Summary model: `")
		b.WriteString(source.SummaryModel)
		b.WriteString("`\n")
	}
	if source.SummaryPromptVersion != "" {
		b.WriteString("- Summary prompt version: `")
		b.WriteString(source.SummaryPromptVersion)
		b.WriteString("`\n")
	}
	if source.SummaryTool != "" || source.SummaryToolVersion != "" {
		b.WriteString("- Summarized with: `")
		b.WriteString(strings.TrimSpace(strings.Join([]string{source.SummaryTool, source.SummaryToolVersion}, " ")))
		b.WriteString("`\n")
	}
	if !source.SummarizedAt.IsZero() {
		b.WriteString("- Summarized at: ")
		b.WriteString(formatTime(source.SummarizedAt))
		b.WriteString("\n")
	}
	if source.Description != "" {
		b.WriteString("- Description: ")
		b.WriteString(source.Description)
		b.WriteString("\n")
	}
	if source.ExtractError != "" {
		b.WriteString("- Extract error: ")
		b.WriteString(source.ExtractError)
		b.WriteString("\n")
	}
	if source.SummaryError != "" {
		b.WriteString("- Summary error: ")
		b.WriteString(source.SummaryError)
		b.WriteString("\n")
	}
}

func writeSourceTextSections(b *strings.Builder, source model.SourceDocument) {
	b.WriteString("\n## Summary\n\n")
	if strings.TrimSpace(source.SummaryText) != "" {
		b.WriteString(strings.TrimSpace(source.SummaryText))
		b.WriteString("\n")
	} else {
		b.WriteString("No summary stored yet.\n")
	}

	b.WriteString("\n## Extracted Text\n\n")
	if strings.TrimSpace(source.ExtractedText) != "" {
		b.WriteString(strings.TrimSpace(source.ExtractedText))
		b.WriteString("\n")
	} else {
		b.WriteString("No extracted text stored yet.\n")
	}
}

func writeSourceBacklinksSection(b *strings.Builder, backlinks []model.SourceBacklink) {
	if len(backlinks) == 0 {
		return
	}
	b.WriteString("\n## Referenced By\n\n")
	for _, ref := range backlinks {
		b.WriteString("- ")
		if ref.NotePath != "" {
			b.WriteString(obsidianLink(ref.NotePath, ref.Title))
		} else {
			b.WriteString(ref.Title)
		}
		if ref.AuthorHandle != "" || ref.AuthorName != "" {
			b.WriteString(" by ")
			label := strings.TrimSpace(ref.AuthorName)
			if ref.AuthorHandle != "" {
				if label != "" {
					label += " "
				}
				label += "@" + ref.AuthorHandle
			}
			b.WriteString(label)
		}
		b.WriteString("\n")
	}
}

func obsidianLink(notePath, title string) string {
	target := strings.TrimSuffix(filepath.ToSlash(notePath), ".md")
	label := strings.TrimSpace(title)
	if label == "" {
		label = target
	}
	return "[[" + target + "|" + label + "]]"
}
