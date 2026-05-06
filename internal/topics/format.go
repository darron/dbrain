package topics

import (
	"fmt"
	"strings"
)

func FormatText(graph TopicMap) string {
	var b strings.Builder
	b.WriteString("Topic map: ")
	b.WriteString(graph.Topic)
	b.WriteString("\n")
	b.WriteString("Nodes:\n")
	for _, node := range graph.Nodes {
		b.WriteString("- [")
		b.WriteString(node.SourceKey)
		b.WriteString("] ")
		b.WriteString(node.Title)
		if node.Role != "" {
			b.WriteString(" (")
			b.WriteString(node.Role)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if len(graph.Edges) > 0 {
		b.WriteString("Edges:\n")
		for _, edge := range graph.Edges {
			b.WriteString("- ")
			b.WriteString(edge.From)
			b.WriteString(" --")
			b.WriteString(edge.Relationship)
			b.WriteString("--> ")
			b.WriteString(edge.To)
			b.WriteString("\n")
		}
	}
	if len(graph.Entities) > 0 {
		b.WriteString("Entities:\n")
		appendTopicEntityLines(&b, "projects", graph.Pivots.Projects)
		appendTopicEntityLines(&b, "orgs", graph.Pivots.Orgs)
		appendTopicEntityLines(&b, "sites", graph.Pivots.Sites)
		appendTopicEntityLines(&b, "people", graph.Pivots.People)
	}
	if len(graph.Pivots.SeedNodes) > 0 {
		b.WriteString("Starting notes:\n")
		appendTopicNodeLines(&b, graph.Pivots.SeedNodes)
	}
	if len(graph.Pivots.RelatedNodes) > 0 {
		b.WriteString("Related notes:\n")
		appendTopicNodeLines(&b, graph.Pivots.RelatedNodes)
	}
	return strings.TrimSpace(b.String())
}

func SummaryText(graph TopicMap) string {
	parts := []string{
		fmt.Sprintf("Mapped %d notes and %d relationships for %q.", len(graph.Nodes), len(graph.Edges), graph.Topic),
	}
	if strings.TrimSpace(graph.Synthesis.Overview) != "" {
		parts = append(parts, graph.Synthesis.Overview)
	}
	if totalTopicPivotEntities(graph.Pivots) > 0 {
		parts = append(parts, fmt.Sprintf("Key entity pivots: %s.", describeTopicPivots(graph.Pivots)))
	}
	if len(graph.Pivots.SeedNodes) > 0 {
		parts = append(parts, fmt.Sprintf("Start with %s.", joinLabels(topicNodeTitles(graph.Pivots.SeedNodes))))
	}
	return strings.Join(parts, " ")
}

func totalTopicPivotEntities(pivots TopicPivots) int {
	return len(pivots.Projects) + len(pivots.Orgs) + len(pivots.Sites) + len(pivots.People)
}

func describeTopicPivots(pivots TopicPivots) string {
	parts := make([]string, 0, 4)
	if len(pivots.Projects) > 0 {
		parts = append(parts, "projects "+joinLabels(topicEntityNames(pivots.Projects)))
	}
	if len(pivots.Orgs) > 0 {
		parts = append(parts, "orgs "+joinLabels(topicEntityNames(pivots.Orgs)))
	}
	if len(pivots.Sites) > 0 {
		parts = append(parts, "sites "+joinLabels(topicEntityNames(pivots.Sites)))
	}
	if len(pivots.People) > 0 {
		parts = append(parts, "people "+joinLabels(topicEntityNames(pivots.People)))
	}
	return strings.Join(parts, "; ")
}

func topicEntityNames(values []TopicEntity) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Name)
	}
	return out
}

func topicNodeTitles(values []TopicMapNode) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Title)
	}
	return out
}

func joinLabels(values []string) string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	switch len(filtered) {
	case 0:
		return ""
	case 1:
		return filtered[0]
	case 2:
		return filtered[0] + " and " + filtered[1]
	default:
		return strings.Join(filtered[:len(filtered)-1], ", ") + ", and " + filtered[len(filtered)-1]
	}
}

func appendTopicEntityLines(b *strings.Builder, label string, values []TopicEntity) {
	if len(values) == 0 {
		return
	}
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(": ")
	for i, entity := range values {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(entity.Name)
		b.WriteString(" [")
		b.WriteString(entity.Kind)
		b.WriteString("] refs=")
		_, _ = fmt.Fprintf(b, "%d", entity.MatchedReferences)
		if len(entity.MatchedSourceKeys) > 0 {
			b.WriteString(" nodes=")
			b.WriteString(strings.Join(entity.MatchedSourceKeys, ", "))
		}
	}
	b.WriteString("\n")
}

func appendTopicNodeLines(b *strings.Builder, values []TopicMapNode) {
	for _, node := range values {
		b.WriteString("- [")
		b.WriteString(node.SourceKey)
		b.WriteString("] ")
		b.WriteString(node.Title)
		if node.SourceType != "" {
			b.WriteString(" (")
			b.WriteString(node.SourceType)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
}
