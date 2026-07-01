package modelbakeoff

import (
	"fmt"
	"sort"
	"strings"
)

func RenderMarkdown(result Result, maxTextChars int) string {
	var b strings.Builder
	b.WriteString("# dbrain Model Bakeoff\n\n")
	fmt.Fprintf(&b, "- Schema: `%s`\n", result.SchemaVersion)
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
			if run.Provider != "" {
				fmt.Fprintf(&b, "- Provider: `%s`\n", run.Provider)
			}
			if run.APIModel != "" {
				fmt.Fprintf(&b, "- API model: `%s`\n", run.APIModel)
			}
			if run.Transport != "" {
				fmt.Fprintf(&b, "- Transport: `%s`\n", run.Transport)
			}
			if run.Local != nil {
				fmt.Fprintf(&b, "- Local backend: `%t`\n", *run.Local)
			}
			if run.ParamStrictness != "" {
				fmt.Fprintf(&b, "- Param strictness: `%s`\n", run.ParamStrictness)
			}
			if run.PromptParityStatus != "" {
				fmt.Fprintf(&b, "- Prompt parity: `%s`\n", run.PromptParityStatus)
			}
			if run.ReasoningModeStatus != "" {
				fmt.Fprintf(&b, "- Reasoning mode: `%s`\n", run.ReasoningModeStatus)
			}
			if run.RuntimeContext.Status != "" {
				fmt.Fprintf(&b, "- Runtime context: `%s`\n", run.RuntimeContext.Status)
			}
			writeParamMap(&b, "Requested params", run.RequestedParams)
			writeParamMap(&b, "Sent params", run.SentParams)
			writeOmittedParams(&b, run.OmittedParams)
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

func writeParamMap(b *strings.Builder, label string, params map[string]any) {
	if len(params) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s: ", label)
	first := true
	for _, key := range sortedKeys(params) {
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(b, "`%s=%v`", key, params[key])
	}
	b.WriteByte('\n')
}

func writeOmittedParams(b *strings.Builder, omitted map[string]string) {
	if len(omitted) == 0 {
		return
	}
	b.WriteString("- Omitted params:\n")
	for _, key := range sortedStringKeys(omitted) {
		fmt.Fprintf(b, "  - `%s`: %s\n", key, omitted[key])
	}
}

func sortedKeys(params map[string]any) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
