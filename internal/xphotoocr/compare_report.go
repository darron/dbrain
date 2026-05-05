package xphotoocr

import (
	"fmt"
	"strings"
	"time"
)

func RenderCompareMarkdown(result CompareResult, maxTextChars int) string {
	var b strings.Builder
	b.WriteString("# X Photo OCR Model Compare\n\n")
	_, _ = fmt.Fprintf(&b, "- Schema: `%s`\n", result.SchemaVersion)
	_, _ = fmt.Fprintf(&b, "- Images: %d\n", len(result.Images))
	_, _ = fmt.Fprintf(&b, "- Models: %s\n", strings.Join(result.Models, ", "))
	_, _ = fmt.Fprintf(&b, "- Started: %s\n", result.StartedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintf(&b, "- Duration: %dms\n\n", result.DurationMS)

	b.WriteString("## Summary\n\n")
	b.WriteString("| Model | OK | Errors | Avg Duration | Avg Chars | Avg Baseline Word Overlap |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range result.Summary {
		_, _ = fmt.Fprintf(&b, "| %s | %d | %d | %dms | %d | %.1f%% |\n",
			markdownTableCell(row.Model),
			row.OK,
			row.Errors,
			row.AverageDurationMS,
			row.AverageChars,
			row.AverageBaselineWordOverlap*100,
		)
	}

	for _, image := range result.Images {
		_, _ = fmt.Fprintf(&b, "\n## %d. %s\n\n", image.Index, image.SourceKey)
		if strings.TrimSpace(image.Title) != "" {
			_, _ = fmt.Fprintf(&b, "- Title: %s\n", image.Title)
		}
		if strings.TrimSpace(image.CanonicalURL) != "" {
			_, _ = fmt.Fprintf(&b, "- URL: %s\n", image.CanonicalURL)
		}
		_, _ = fmt.Fprintf(&b, "- Local path: `%s`\n", image.LocalPath)
		if strings.TrimSpace(image.InputSource) != "" {
			_, _ = fmt.Fprintf(&b, "- Input source: %s\n", image.InputSource)
		}
		if strings.TrimSpace(image.ExpandedURL) != "" {
			_, _ = fmt.Fprintf(&b, "- Expanded media URL: %s\n", image.ExpandedURL)
		}
		if strings.TrimSpace(image.ExistingOCR.Status) != "" || strings.TrimSpace(image.ExistingOCR.Text) != "" {
			_, _ = fmt.Fprintf(&b, "- Existing OCR: status=%s model=%s tool=%s\n", emptyReportValue(image.ExistingOCR.Status), emptyReportValue(image.ExistingOCR.Model), emptyReportValue(image.ExistingOCR.Tool))
		}
		for _, run := range image.Runs {
			_, _ = fmt.Fprintf(&b, "\n### %s\n\n", run.Model)
			_, _ = fmt.Fprintf(&b, "- Status: %s\n", run.Status)
			_, _ = fmt.Fprintf(&b, "- Duration: %dms\n", run.DurationMS)
			if strings.TrimSpace(run.Tool) != "" {
				_, _ = fmt.Fprintf(&b, "- Tool: %s\n", run.Tool)
			}
			if strings.TrimSpace(run.ReportedModel) != "" && run.ReportedModel != run.Model {
				_, _ = fmt.Fprintf(&b, "- Reported model: %s\n", run.ReportedModel)
			}
			if run.BaselineWordOverlap > 0 {
				_, _ = fmt.Fprintf(&b, "- Baseline word overlap: %.1f%%\n", run.BaselineWordOverlap*100)
			}
			if strings.TrimSpace(run.Error) != "" {
				_, _ = fmt.Fprintf(&b, "- Error: %s\n", run.Error)
			}
			writeReportTextBlock(&b, run.Text, maxTextChars)
		}
	}
	return b.String()
}

func writeReportTextBlock(b *strings.Builder, text string, maxChars int) {
	text = strings.TrimSpace(text)
	if text == "" {
		b.WriteString("\n_No text returned._\n")
		return
	}
	if maxChars > 0 && len([]rune(text)) > maxChars {
		runes := []rune(text)
		text = string(runes[:maxChars]) + "\n\n[truncated]"
	}
	text = strings.ReplaceAll(text, "```", "` ` `")
	b.WriteString("\n```text\n")
	b.WriteString(text)
	b.WriteString("\n```\n")
}

func markdownTableCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func emptyReportValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
