package modelbakeoff

import (
	"fmt"
	"strings"
)

func RenderMarkdown(result Result, maxTextChars int) string {
	var b strings.Builder
	b.WriteString("# dbrain Model Bakeoff\n\n")
	fmt.Fprintf(&b, "- Mode: `%s`\n", result.Mode)
	fmt.Fprintf(&b, "- Targets: `%d`\n", len(result.Targets))
	fmt.Fprintf(&b, "- Models: `%s`\n", strings.Join(result.Models, "`, `"))
	fmt.Fprintf(&b, "- Duration: `%dms`\n", result.DurationMS)
	fmt.Fprintf(&b, "- Errors: `%d`\n\n", result.Errors)

	if len(result.Summary) > 0 {
		b.WriteString("## Summary\n\n")
		b.WriteString("| Model | OK | Errors | Avg ms | Avg chars | Avg baseline overlap |\n")
		b.WriteString("|---|---:|---:|---:|---:|---:|\n")
		for _, row := range result.Summary {
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %.1f%% |\n",
				escapeCell(row.Model),
				row.OK,
				row.Errors,
				row.AverageDurationMS,
				row.AverageOutputChars,
				row.AverageBaselineWordOverlap*100,
			)
		}
		b.WriteString("\n")
	}

	for _, target := range result.Targets {
		b.WriteString("## ")
		b.WriteString(firstNonEmpty(target.Title, target.SourceKey, target.Lookup))
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "- Lookup: `%s`\n", target.Lookup)
		if target.SourceKey != "" {
			fmt.Fprintf(&b, "- Source key: `%s`\n", target.SourceKey)
		}
		if target.SourceType != "" {
			fmt.Fprintf(&b, "- Type: `%s`\n", target.SourceType)
		}
		if target.CanonicalURL != "" {
			fmt.Fprintf(&b, "- URL: %s\n", target.CanonicalURL)
		}
		b.WriteString("\n")

		for _, run := range target.Runs {
			b.WriteString("### ")
			b.WriteString(run.Model)
			b.WriteString("\n\n")
			fmt.Fprintf(&b, "- Status: `%s`\n", run.Status)
			fmt.Fprintf(&b, "- Duration: `%dms`\n", run.DurationMS)
			if run.WordOverlap > 0 {
				fmt.Fprintf(&b, "- Baseline overlap: `%.1f%%`\n", run.WordOverlap*100)
			}
			if run.Error != "" {
				fmt.Fprintf(&b, "- Error: `%s`\n\n", escapeBackticks(run.Error))
				continue
			}
			b.WriteString("\n")
			if run.Summary != nil {
				b.WriteString(truncate(run.Summary.Text, maxTextChars))
				b.WriteString("\n\n")
			}
			if run.Categorize != nil {
				fmt.Fprintf(&b, "- Primary category: `%s`\n", run.Categorize.PrimaryCategory)
				fmt.Fprintf(&b, "- Categories: `%s`\n", strings.Join(run.Categorize.Categories, "`, `"))
				fmt.Fprintf(&b, "- Tags: `%s`\n\n", strings.Join(run.Categorize.Tags, "`, `"))
			}
		}
	}

	return b.String()
}

func truncate(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return strings.TrimSpace(value[:maxChars]) + "\n\n…"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func escapeCell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}

func escapeBackticks(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}
