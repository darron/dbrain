package app

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/brainresearch"
)

func writeResearchSynthesis(out interface {
	Write([]byte) (int, error)
}, synthesis brainresearch.SynthesisResult) {
	_, _ = fmt.Fprintf(out, "Answer status: %s\n", synthesis.AnswerStatus)
	if synthesis.Model != "" {
		_, _ = fmt.Fprintf(out, "Model: %s\n", synthesis.Model)
	}
	if synthesis.Tool != "" {
		_, _ = fmt.Fprintf(out, "Tool: %s", synthesis.Tool)
		if synthesis.ToolVersion != "" {
			_, _ = fmt.Fprintf(out, " %s", synthesis.ToolVersion)
		}
		_, _ = fmt.Fprintln(out)
	}
	if len(synthesis.Warnings) > 0 {
		_, _ = fmt.Fprintf(out, "Warnings: %s\n", strings.Join(synthesis.Warnings, ", "))
	}
	if synthesis.Answer != "" {
		_, _ = fmt.Fprintln(out, "\nAnswer:")
		_, _ = fmt.Fprintf(out, "%s\n", synthesis.Answer)
	}
	if len(synthesis.Citations) > 0 {
		_, _ = fmt.Fprintln(out, "\nCitations:")
		for _, citation := range synthesis.Citations {
			_, _ = fmt.Fprintf(out, "- %s", citation.SourceKey)
			if citation.NotePath != "" {
				_, _ = fmt.Fprintf(out, " (%s)", citation.NotePath)
			}
			_, _ = fmt.Fprintln(out)
		}
	}
	_, _ = fmt.Fprintln(out)
}

func writeResearchPack(out interface {
	Write([]byte) (int, error)
}, pack brainresearch.Pack) {
	_, _ = fmt.Fprintf(out, "Research pack: %s\n", pack.Question)
	_, _ = fmt.Fprintf(out, "Mode: %s\n", pack.Mode)
	_, _ = fmt.Fprintf(out, "Evidence: %d\n", len(pack.Evidence))
	writeResearchSemanticDiagnostics(out, pack.QueryPlan)
	if pack.Coverage.RecallNote != "" {
		_, _ = fmt.Fprintf(out, "Recall: %s\n", pack.Coverage.RecallNote)
	}
	if len(pack.Coverage.ExactTagMatches) > 0 {
		_, _ = fmt.Fprintln(out, "Exact tag matches:")
		for _, bucket := range pack.Coverage.ExactTagMatches {
			_, _ = fmt.Fprintf(out, "- %s (%d)\n", bucket.Key, bucket.Count)
		}
	}
	if pack.TopicBrief != nil {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Topic brief: %s\n", pack.TopicBrief.Topic)
		if pack.TopicBrief.Summary != "" {
			_, _ = fmt.Fprintf(out, "%s\n", pack.TopicBrief.Summary)
		}
	}

	if len(pack.Evidence) == 0 {
		_, _ = fmt.Fprintln(out, "\nNo matching evidence found.")
		return
	}

	_, _ = fmt.Fprintln(out, "\nRetrieved evidence:")
	for _, doc := range pack.Evidence {
		_, _ = fmt.Fprintf(out, "- [%s] %s\n", doc.SourceKey, doc.Title)
		_, _ = fmt.Fprintf(out, "  kind: %s\n", doc.Kind)
		_, _ = fmt.Fprintf(out, "  url: %s\n", doc.URL)
		_, _ = fmt.Fprintf(out, "  note: %s\n", doc.NotePath)
		if doc.SourceType != "" {
			_, _ = fmt.Fprintf(out, "  source_type: %s\n", doc.SourceType)
		}
		if len(doc.EntityMatches) > 0 {
			_, _ = fmt.Fprintf(out, "  entity_matches: %s\n", strings.Join(doc.EntityMatches, ", "))
		}
		if doc.Relationship != "" {
			_, _ = fmt.Fprintf(out, "  relationship: %s", doc.Relationship)
			if doc.RelatedTo != "" {
				_, _ = fmt.Fprintf(out, " (%s)", doc.RelatedTo)
			}
			_, _ = fmt.Fprintln(out)
		}
		if doc.Summary != "" {
			_, _ = fmt.Fprintf(out, "  summary: %s\n", singleLine(doc.Summary))
		} else if doc.Excerpt != "" {
			_, _ = fmt.Fprintf(out, "  excerpt: %s\n", singleLine(doc.Excerpt))
		}
	}
}

func writeResearchSemanticDiagnostics(out interface {
	Write([]byte) (int, error)
}, plan brainresearch.QueryPlan) {
	parts := []string{"Semantic: " + string(plan.SemanticMode)}
	if len(plan.RetrievalLanes) > 0 {
		lanes := make([]string, 0, len(plan.RetrievalLanes))
		for _, lane := range plan.RetrievalLanes {
			value := strings.TrimSpace(lane.Name) + "=" + strings.TrimSpace(lane.Status)
			if reason := strings.TrimSpace(lane.Reason); reason != "" {
				value += "(" + reason + ")"
			}
			lanes = append(lanes, value)
		}
		parts = append(parts, "lanes "+strings.Join(lanes, ", "))
	}
	comparisons := make([]string, 0, 2)
	if plan.ShadowComparison != nil {
		comparisons = append(comparisons, "initial="+compactShadowComparison(plan.ShadowComparison))
	}
	if plan.RetryShadowComparison != nil {
		comparisons = append(comparisons, "retry="+compactShadowComparison(plan.RetryShadowComparison))
	}
	if len(comparisons) > 0 {
		parts = append(parts, "shadow "+strings.Join(comparisons, "; "))
	}
	_, _ = fmt.Fprintln(out, strings.Join(parts, "; "))
}

func compactShadowComparison(comparison *brainresearch.ShadowComparison) string {
	status := string(comparison.Status)
	if comparison.Reason != "" {
		status += "(" + string(comparison.Reason) + ")"
	}
	return fmt.Sprintf("%s L%d/H%d +%d/-%d/~%d", status, comparison.LexicalCount, comparison.HybridCount, len(comparison.Added), len(comparison.Removed), len(comparison.Reordered))
}

func singleLine(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) <= 220 {
		return value
	}
	return string(runes[:220]) + "..."
}
