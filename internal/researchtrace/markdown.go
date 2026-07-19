package researchtrace

import (
	"fmt"
	"strings"
)

func renderMarkdown(trace ResearchTrace) string {
	var b strings.Builder
	b.WriteString("# dbrain research trace\n\n")
	writeMDLine(&b, "Run ID", trace.RunID)
	writeMDLine(&b, "Surface", trace.Surface)
	writeMDLine(&b, "Question", trace.Question)
	writeMDLine(&b, "Started", trace.StartedAt.Format("2006-01-02 15:04:05Z07:00"))
	writeMDLine(&b, "Completed", trace.CompletedAt.Format("2006-01-02 15:04:05Z07:00"))
	writeMDLine(&b, "Stop reason", trace.StopReason)
	if trace.Failure != nil {
		writeMDLine(&b, "Failure", strings.TrimSpace(trace.Failure.Stage+" "+trace.Failure.Code+" "+trace.Failure.Message))
	}

	if trace.ChatContinuity != nil {
		b.WriteString("\n## Chat Continuity\n\n")
		writeMDLine(&b, "Original question", trace.ChatContinuity.OriginalQuestion)
		writeMDLine(&b, "Retrieval question", trace.ChatContinuity.RetrievalQuestion)
		writeMDList(&b, "Prior question IDs", trace.ChatContinuity.PriorQuestionIDs)
		writeMDList(&b, "Pinned evidence keys", trace.ChatContinuity.PinnedEvidenceKeys)
		writeMDList(&b, "Merged prior evidence", trace.ChatContinuity.MergedPriorEvidence)
	}

	if trace.Pack != nil {
		b.WriteString("\n## Research Pack\n\n")
		writeMDLine(&b, "Mode", trace.Pack.Mode)
		writeMDLine(&b, "Semantic mode", string(trace.Pack.QueryPlan.SemanticMode))
		if comparison := trace.Pack.QueryPlan.ShadowComparison; comparison != nil {
			writeMDLine(&b, "Shadow status", string(comparison.Status))
			writeMDLine(&b, "Shadow reason", string(comparison.Reason))
			writeMDLine(&b, "Shadow counts", fmt.Sprintf("lexical=%d hybrid=%d", comparison.LexicalCount, comparison.HybridCount))
			writeMDLine(&b, "Shadow rank deltas", fmt.Sprintf("added=%d removed=%d reordered=%d", len(comparison.Added), len(comparison.Removed), len(comparison.Reordered)))
		}
		writeMDLine(&b, "Planner", trace.Pack.QueryPlan.Planner)
		writeMDLine(&b, "Planner model", trace.Pack.QueryPlan.PlannerModel)
		writeMDLine(&b, "Planner error", trace.Pack.QueryPlan.PlannerError)
		writeMDLine(&b, "Recall note", trace.Pack.Coverage.RecallNote)
		if len(trace.Pack.Evidence) > 0 {
			b.WriteString("\n### Evidence\n\n")
			for _, row := range trace.Pack.Evidence {
				title := firstNonEmpty(row.Title, row.URL, row.SourceKey)
				fmt.Fprintf(&b, "- `%s` %s", row.SourceKey, escapeLine(title))
				if row.NotePath != "" {
					fmt.Fprintf(&b, " (%s)", escapeLine(row.NotePath))
				}
				b.WriteString("\n")
			}
		}
	}

	if trace.EvidenceFlow != nil {
		b.WriteString("\n## Evidence Flow\n\n")
		writeMDLine(&b, "Schema", trace.EvidenceFlow.SchemaVersion)
		writeMDLine(&b, "Inspection status", trace.EvidenceFlow.InspectionStatus)
		writeMDLine(&b, "Preparation status", trace.EvidenceFlow.PreparationStatus)
		writeMDLine(&b, "Synthesis status", trace.EvidenceFlow.SynthesisStatus)
		writeMDLine(&b, "Retried", fmt.Sprintf("%t", trace.EvidenceFlow.Retried))
		writeMDList(&b, "Retrieved", trace.EvidenceFlow.RetrievedSourceKeys)
		writeMDList(&b, "Relevance admitted", trace.EvidenceFlow.RelevanceAdmittedSourceKeys)
		writeMDList(&b, "Relevance excluded", trace.EvidenceFlow.RelevanceExcludedSourceKeys)
		writeMDList(&b, "Prompt admitted", trace.EvidenceFlow.PromptAdmittedSourceKeys)
		writeMDList(&b, "Budget dropped", trace.EvidenceFlow.BudgetDroppedSourceKeys)
		writeMDList(&b, "Partially trimmed", trace.EvidenceFlow.PartiallyTrimmedSourceKeys)
		writeMDList(&b, "Answer cited", trace.EvidenceFlow.AnswerCitedSourceKeys)
		writeMDList(&b, "Invalid answer citations", trace.EvidenceFlow.InvalidAnswerCitationSourceKeys)
		writeMDList(&b, "Invariant errors", trace.EvidenceFlow.InvariantErrors)
	}

	if trace.Synthesis != nil {
		b.WriteString("\n## Synthesis\n\n")
		writeMDLine(&b, "Answer status", trace.Synthesis.AnswerStatus)
		writeMDLine(&b, "Model", trace.Synthesis.Model)
		if len(trace.Synthesis.Warnings) > 0 {
			writeMDList(&b, "Warnings", trace.Synthesis.Warnings)
		}
		if strings.TrimSpace(trace.Synthesis.Answer) != "" {
			b.WriteString("\n### Answer\n\n")
			b.WriteString(strings.TrimSpace(trace.Synthesis.Answer))
			b.WriteString("\n")
		}
		if len(trace.Synthesis.Citations) > 0 {
			b.WriteString("\n### Citations\n\n")
			for _, citation := range trace.Synthesis.Citations {
				fmt.Fprintf(&b, "- `%s`", citation.SourceKey)
				if citation.Title != "" {
					fmt.Fprintf(&b, " %s", escapeLine(citation.Title))
				}
				if citation.NotePath != "" {
					fmt.Fprintf(&b, " (%s)", escapeLine(citation.NotePath))
				}
				b.WriteString("\n")
			}
		}
	}

	if len(trace.Events) > 0 {
		b.WriteString("\n## Timeline\n\n")
		for _, event := range trace.Events {
			fmt.Fprintf(&b, "- `%s` %s", event.At.Format("15:04:05.000"), event.Name)
			if len(event.Data) > 0 {
				if count, ok := event.Data["candidate_count"]; ok {
					fmt.Fprintf(&b, " candidate_count=%v", count)
				}
				if keys, ok := event.Data["source_keys"]; ok {
					fmt.Fprintf(&b, " source_keys=%v", keys)
				}
			}
			b.WriteString("\n")
		}
	}

	if trace.Artifacts.PlannerInputPath != "" || trace.Artifacts.PlannerOutputPath != "" || trace.Artifacts.SynthesisInputPath != "" {
		b.WriteString("\n## Artifacts\n\n")
		writeMDLine(&b, "Planner input", trace.Artifacts.PlannerInputPath)
		writeMDLine(&b, "Planner output", trace.Artifacts.PlannerOutputPath)
		writeMDLine(&b, "Synthesis input", trace.Artifacts.SynthesisInputPath)
	}

	return b.String()
}

func writeMDLine(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "- **%s:** %s\n", label, escapeLine(value))
}

func writeMDList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "- **%s:**\n", label)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fmt.Fprintf(b, "  - %s\n", escapeLine(value))
	}
}

func escapeLine(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
