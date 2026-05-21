package mcpserver

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

func formatResearchPack(pack brainresearch.Pack) string {
	var b strings.Builder
	b.WriteString("Research pack for: ")
	b.WriteString(pack.Question)
	b.WriteString("\n")
	b.WriteString("Mode: ")
	b.WriteString(pack.Mode)
	b.WriteString("\n")
	if strings.TrimSpace(pack.QueryPlan.TextQuery) != "" {
		b.WriteString("Text query: ")
		b.WriteString(pack.QueryPlan.TextQuery)
		b.WriteString("\n")
	}
	if len(pack.QueryPlan.TagQueries) > 0 {
		b.WriteString("Tag aliases: ")
		b.WriteString(strings.Join(pack.QueryPlan.TagQueries, ", "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(pack.QueryPlan.Planner) != "" {
		b.WriteString("Planner: ")
		b.WriteString(pack.QueryPlan.Planner)
		if strings.TrimSpace(pack.QueryPlan.PlannerModel) != "" {
			b.WriteString(" (")
			b.WriteString(pack.QueryPlan.PlannerModel)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if len(pack.QueryPlan.QueryVariants) > 0 {
		b.WriteString("Query variants: ")
		parts := make([]string, 0, min(len(pack.QueryPlan.QueryVariants), 4))
		for _, variant := range pack.QueryPlan.QueryVariants {
			parts = append(parts, variant.Query)
			if len(parts) >= 4 {
				break
			}
		}
		b.WriteString(strings.Join(parts, " | "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(pack.Topic) != "" {
		b.WriteString("Topic: ")
		b.WriteString(pack.Topic)
		if strings.TrimSpace(pack.QueryPlan.TopicSource) != "" {
			b.WriteString(" (")
			b.WriteString(pack.QueryPlan.TopicSource)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	b.WriteString("Coverage: ")
	_, _ = fmt.Fprintf(&b, "%d evidence", pack.Coverage.EvidenceCount)
	if len(pack.Coverage.ExactTagMatches) > 0 {
		b.WriteString("; exact tags ")
		b.WriteString(formatResearchBuckets(pack.Coverage.ExactTagMatches, 4))
	}
	if pack.Coverage.ItemTextMatches > 0 || pack.Coverage.SourceTextMatches > 0 {
		_, _ = fmt.Fprintf(&b, "; text matches items=%d sources=%d", pack.Coverage.ItemTextMatches, pack.Coverage.SourceTextMatches)
	}
	if pack.Coverage.TopicNodeCount > 0 {
		_, _ = fmt.Fprintf(&b, "; topic nodes=%d", pack.Coverage.TopicNodeCount)
	}
	if len(pack.Coverage.BySourceType) > 0 {
		b.WriteString("; source types ")
		b.WriteString(formatResearchBuckets(pack.Coverage.BySourceType, 4))
	}
	if len(pack.Coverage.TopUserTags) > 0 {
		b.WriteString("; top tags ")
		b.WriteString(formatResearchBuckets(pack.Coverage.TopUserTags, 5))
	}
	b.WriteString("\n")
	if pack.Coverage.RecallNote != "" {
		b.WriteString("Recall: ")
		b.WriteString(pack.Coverage.RecallNote)
		b.WriteString("\n")
	}
	if pack.TopicBrief != nil && strings.TrimSpace(pack.TopicBrief.Summary) != "" {
		b.WriteString("\nTopic brief:\n")
		b.WriteString(strings.TrimSpace(pack.TopicBrief.Summary))
		b.WriteString("\n")
	}
	b.WriteString("\nEvidence count: ")
	_, _ = fmt.Fprintf(&b, "%d", len(pack.Evidence))
	if len(pack.Evidence) > 0 {
		b.WriteString("\n")
		for _, doc := range pack.Evidence {
			writeResearchEvidenceLine(&b, doc, 280)
		}
	}
	if len(pack.ExactTagEvidence) > 0 {
		b.WriteString("\nExact tag examples:\n")
		for _, doc := range pack.ExactTagEvidence {
			writeResearchEvidenceLine(&b, doc, 220)
		}
	}
	if len(pack.NextSteps) > 0 {
		b.WriteString("\nSuggested next actions:\n")
		for _, step := range pack.NextSteps {
			b.WriteString("- ")
			b.WriteString(step.Action)
			if strings.TrimSpace(step.Label) != "" {
				b.WriteString(" (")
				b.WriteString(step.Label)
				b.WriteString(")")
			}
			if strings.TrimSpace(step.Reason) != "" {
				b.WriteString(": ")
				b.WriteString(step.Reason)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func writeResearchEvidenceLine(b *strings.Builder, doc ask.Evidence, maxChars int) {
	b.WriteString("- [")
	b.WriteString(doc.SourceKey)
	b.WriteString("] ")
	b.WriteString(doc.Title)
	if doc.Relationship != "" {
		b.WriteString(" (")
		b.WriteString(doc.Relationship)
		b.WriteString(")")
	}
	b.WriteString("\n")
	if strings.TrimSpace(doc.UserTags) != "" {
		b.WriteString("  User tags: ")
		b.WriteString(strings.TrimSpace(doc.UserTags))
		b.WriteString("\n")
	}
	if text := trimResearchText(firstNonEmpty(doc.Summary, doc.Excerpt), maxChars); text != "" {
		b.WriteString("  Evidence: ")
		b.WriteString(text)
		b.WriteString("\n")
	}
	if len(doc.Media) > 0 {
		b.WriteString("  Media: ")
		parts := make([]string, 0, len(doc.Media))
		for _, ref := range doc.Media {
			label := strings.TrimSpace(ref.MediaType)
			if label == "" {
				label = "media"
			}
			if strings.TrimSpace(ref.ArchiveURL) != "" {
				label += " " + strings.TrimSpace(ref.ArchiveURL)
			} else if strings.TrimSpace(ref.RemoteURL) != "" {
				label += " " + strings.TrimSpace(ref.RemoteURL)
			}
			parts = append(parts, label)
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString("\n")
	}
}

func formatResearchBuckets(buckets []brainresearch.Bucket, limit int) string {
	if limit > 0 && len(buckets) > limit {
		buckets = buckets[:limit]
	}
	parts := make([]string, 0, len(buckets))
	for _, bucket := range buckets {
		parts = append(parts, fmt.Sprintf("%s=%d", bucket.Key, bucket.Count))
	}
	return strings.Join(parts, ", ")
}

func trimResearchText(value string, maxChars int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "..."
}
