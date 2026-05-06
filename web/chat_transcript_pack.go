package web

import (
	"strings"

	"github.com/darron/dbrain/internal/brainresearch"
)

func writeTranscriptResearchPack(b *strings.Builder, pack brainresearch.Pack) {
	hasPack := strings.TrimSpace(pack.Question) != "" ||
		len(pack.Evidence) > 0 ||
		len(pack.ExactTagEvidence) > 0 ||
		pack.TopicBrief != nil ||
		strings.TrimSpace(pack.Coverage.RecallNote) != ""
	if !hasPack {
		return
	}

	b.WriteString("### Research Pack\n\n")
	if value := strings.TrimSpace(pack.Question); value != "" {
		b.WriteString("Question: ")
		b.WriteString(value)
		b.WriteString("\n\n")
	}
	if value := strings.TrimSpace(pack.Coverage.RecallNote); value != "" {
		b.WriteString("Recall: ")
		b.WriteString(value)
		b.WriteString("\n\n")
	}
	if plan := pack.QueryPlan; plan.TextQuery != "" || len(plan.QueryTerms) > 0 || len(plan.TagQueries) > 0 {
		b.WriteString("Query plan:\n")
		if value := strings.TrimSpace(plan.TextQuery); value != "" {
			b.WriteString("- text: `")
			b.WriteString(value)
			b.WriteString("`\n")
		}
		if len(plan.QueryTerms) > 0 {
			b.WriteString("- terms: ")
			b.WriteString(strings.Join(plan.QueryTerms, ", "))
			b.WriteByte('\n')
		}
		if len(plan.TagQueries) > 0 {
			b.WriteString("- tags: ")
			b.WriteString(strings.Join(plan.TagQueries, ", "))
			b.WriteByte('\n')
		}
		if strings.TrimSpace(plan.Planner) != "" {
			b.WriteString("- planner: ")
			b.WriteString(plan.Planner)
			if strings.TrimSpace(plan.PlannerModel) != "" {
				b.WriteString(" (")
				b.WriteString(plan.PlannerModel)
				b.WriteString(")")
			}
			b.WriteByte('\n')
		}
		if strings.TrimSpace(plan.PlannerError) != "" {
			b.WriteString("- planner_error: ")
			b.WriteString(plan.PlannerError)
			b.WriteByte('\n')
		}
		if len(plan.QueryVariants) > 0 {
			b.WriteString("- variants:\n")
			for _, variant := range plan.QueryVariants {
				b.WriteString("  - `")
				b.WriteString(variant.Query)
				b.WriteString("`")
				if strings.TrimSpace(variant.Reason) != "" {
					b.WriteString(" (")
					b.WriteString(variant.Reason)
					b.WriteString(")")
				}
				b.WriteByte('\n')
			}
		}
		if len(plan.Concepts) > 0 {
			b.WriteString("- concepts:\n")
			for _, concept := range plan.Concepts {
				b.WriteString("  - ")
				b.WriteString(concept.Key)
				if !concept.Required {
					b.WriteString(" (optional)")
				}
				b.WriteString(": ")
				b.WriteString(strings.Join(concept.Terms, ", "))
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}
	if pack.TopicBrief != nil {
		b.WriteString("Topic brief: ")
		b.WriteString(truncateTranscriptText(pack.TopicBrief.Summary, 8000))
		b.WriteString("\n\n")
	}

	writeTranscriptEvidenceSection(b, "Evidence", pack.Evidence)
	writeTranscriptEvidenceSection(b, "Exact Tag Evidence", pack.ExactTagEvidence)
}
