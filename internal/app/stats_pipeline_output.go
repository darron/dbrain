package app

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/store"
)

func writePipelineStats(dst interface{ Write([]byte) (int, error) }, stats store.PipelineStats, model string) error {
	if _, err := fmt.Fprintf(dst, "Hydration\n"); err != nil {
		return err
	}
	if err := writePipelineTable(dst, stats.Hydration); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(dst, "\nExtraction\n"); err != nil {
		return err
	}
	if err := writePipelineTable(dst, stats.Extraction); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(dst, "\nSummary\n"); err != nil {
		return err
	}
	summaryTarget := strings.TrimSpace(model)
	if summaryTarget == "" {
		if _, err := fmt.Fprintf(dst, "Coverage policy: any valid summary on current extracted content\n"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(dst, "Model target: %s\n", summaryTarget); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "Freshness target: %s / %s / %s\n", stats.SummaryPromptVersion, fallbackDisplay(stats.SummaryTool, "summary"), fallbackDisplay(stats.SummaryToolVersion, "unknown")); err != nil {
			return err
		}
	}
	if err := writePipelineTable(dst, stats.Summary); err != nil {
		return err
	}

	if len(stats.Transcription) > 0 {
		if _, err := fmt.Fprintf(dst, "\nTranscription\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "X video items that still need transcript materialization before they are fully covered.\n"); err != nil {
			return err
		}
		if err := writePipelineTable(dst, stats.Transcription); err != nil {
			return err
		}
	}

	if len(stats.OCR) > 0 {
		if _, err := fmt.Fprintf(dst, "\nOCR\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "Downloaded social media photo items that still need OCR or vision extraction.\n"); err != nil {
			return err
		}
		if err := writePipelineTable(dst, stats.OCR); err != nil {
			return err
		}
	}

	if len(stats.MediaArchive) > 0 {
		if _, err := fmt.Fprintf(dst, "\nMedia archive\n"); err != nil {
			return err
		}
		if err := writePipelineTable(dst, stats.MediaArchive); err != nil {
			return err
		}
	}

	return nil
}

func writePipelineTable(dst interface{ Write([]byte) (int, error) }, rows []store.PipelineStageRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(dst, "(none)\n")
		return err
	}

	headers := []string{"Type", "Total", "Current", "Pending", "Blocked", "Terminal", "Failed", "Unknown", "Valid", "Current %"}
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}

	values := make([][]string, 0, len(rows))
	for _, row := range rows {
		record := []string{
			pipelineKindLabel(row.Kind),
			fmt.Sprintf("%d", row.Total),
			fmt.Sprintf("%d", row.Current),
			fmt.Sprintf("%d", row.Pending),
			fmt.Sprintf("%d", row.Blocked),
			fmt.Sprintf("%d", row.Terminal),
			fmt.Sprintf("%d", row.Failed),
			fmt.Sprintf("%d", row.Unknown),
			fmt.Sprintf("%t", row.PartitionValid),
			fmt.Sprintf("%.1f%%", row.PercentCurrent),
		}
		for i, value := range record {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
		values = append(values, record)
	}

	if _, err := fmt.Fprintf(dst, "%s\n", formatPipelineTableRow(headers, widths)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "%s\n", formatPipelineTableDivider(widths)); err != nil {
		return err
	}
	for _, record := range values {
		if _, err := fmt.Fprintf(dst, "%s\n", formatPipelineTableRow(record, widths)); err != nil {
			return err
		}
	}
	return nil
}

func formatPipelineTableRow(values []string, widths []int) string {
	parts := make([]string, 0, len(values))
	for i, value := range values {
		align := "%-*s"
		if i > 0 {
			align = "%*s"
		}
		parts = append(parts, fmt.Sprintf(align, widths[i], value))
	}
	return "| " + strings.Join(parts, " | ") + " |"
}

func formatPipelineTableDivider(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width))
	}
	return "|-" + strings.Join(parts, "-|-") + "-|"
}

func pipelineKindLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "":
		return "(empty)"
	default:
		return value
	}
}

func fallbackDisplay(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
