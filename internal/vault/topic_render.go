package vault

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/topics"
)

func RenderTopic(graph topics.TopicMap) string {
	var b strings.Builder
	tags := []string{"source/topic", "topic/" + topicSlug(graph.Topic)}

	b.WriteString("---\n")
	writeYAMLScalar(&b, "brain_topic", graph.Topic)
	writeYAMLScalar(&b, "seed_limit", fmt.Sprintf("%d", graph.SeedLimit))
	writeYAMLScalar(&b, "related_limit", fmt.Sprintf("%d", graph.RelatedLimit))
	writeYAMLArray(&b, "source_types", graph.SourceTypes)
	writeYAMLArray(&b, "tags", tags)
	b.WriteString("---\n\n")

	b.WriteString("# ")
	b.WriteString(graph.Topic)
	b.WriteString("\n\n")

	b.WriteString("## Summary\n\n")
	b.WriteString(topics.SummaryText(graph))
	b.WriteString("\n")

	if strings.TrimSpace(graph.Synthesis.Overview) != "" {
		b.WriteString("\n## What This Topic Is\n\n")
		b.WriteString(strings.TrimSpace(graph.Synthesis.Overview))
		b.WriteString("\n")
	}

	if len(graph.Synthesis.Angles) > 0 {
		b.WriteString("\n## Main Angles\n\n")
		for _, angle := range graph.Synthesis.Angles {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(angle))
			b.WriteString("\n")
		}
	}

	if len(graph.Synthesis.Signals) > 0 {
		b.WriteString("\n## Repeated Signals\n\n")
		for _, signal := range graph.Synthesis.Signals {
			b.WriteString("- **")
			b.WriteString(signal.Title)
			b.WriteString("**")
			if strings.TrimSpace(signal.Detail) != "" && !strings.EqualFold(strings.TrimSpace(signal.Detail), strings.TrimSpace(signal.Title)) {
				b.WriteString(": ")
				b.WriteString(signal.Detail)
			}
			if len(signal.SourceKeys) > 0 {
				b.WriteString(" (`")
				b.WriteString(strings.Join(signal.SourceKeys, "`, `"))
				b.WriteString("`)")
			}
			b.WriteString("\n")
		}
	}

	if len(graph.Entities) > 0 {
		writeTopicEntitySection(&b, "Key Projects", graph.Pivots.Projects)
		writeTopicEntitySection(&b, "Key Organizations", graph.Pivots.Orgs)
		writeTopicEntitySection(&b, "Key Sites", graph.Pivots.Sites)
		writeTopicEntitySection(&b, "Key People", graph.Pivots.People)
	}

	if len(graph.Synthesis.OpenQuestions) > 0 {
		b.WriteString("\n## Open Questions\n\n")
		for _, question := range graph.Synthesis.OpenQuestions {
			b.WriteString("- ")
			b.WriteString(question)
			b.WriteString("\n")
		}
	}

	if len(graph.Nodes) > 0 {
		writeTopicNodeSection(&b, "Suggested Starting Notes", graph.Pivots.SeedNodes)
		writeTopicNodeSection(&b, "Related Notes", graph.Pivots.RelatedNodes)
		writeTopicNodeSection(&b, "Key Nodes", graph.Nodes)
	}

	if len(graph.Edges) > 0 {
		b.WriteString("\n## Relationships\n\n")
		for _, edge := range graph.Edges {
			from := topicNodeLabel(graph, edge.From)
			to := topicNodeLabel(graph, edge.To)
			b.WriteString("- ")
			b.WriteString(from)
			b.WriteString(" --`")
			b.WriteString(edge.Relationship)
			b.WriteString("`--> ")
			b.WriteString(to)
			b.WriteString("\n")
		}
	}

	if strings.TrimSpace(graph.Synthesis.WhyItMatters) != "" {
		b.WriteString("\n## Why It Matters\n\n")
		b.WriteString(strings.TrimSpace(graph.Synthesis.WhyItMatters))
		b.WriteString("\n")
	}

	return b.String()
}

func writeTopicEntitySection(b *strings.Builder, heading string, entities []topics.TopicEntity) {
	if len(entities) == 0 {
		return
	}
	b.WriteString("\n## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, entity := range entities {
		b.WriteString("- ")
		if strings.TrimSpace(entity.NotePath) != "" {
			b.WriteString(obsidianLink(entity.NotePath, entity.Name))
		} else {
			b.WriteString(entity.Name)
		}
		b.WriteString(" (`")
		b.WriteString(entity.Kind)
		b.WriteString("`, matched refs: `")
		b.WriteString(strconv.Itoa(entity.MatchedReferences))
		b.WriteString("`")
		if entity.ReferenceCount > entity.MatchedReferences {
			b.WriteString(", total refs: `")
			b.WriteString(strconv.Itoa(entity.ReferenceCount))
			b.WriteString("`")
		}
		b.WriteString(")")
		if strings.TrimSpace(entity.CanonicalURL) != "" {
			b.WriteString(" ")
			b.WriteString(entity.CanonicalURL)
		}
		b.WriteString("\n")
	}
}

func writeTopicNodeSection(b *strings.Builder, heading string, nodes []topics.TopicMapNode) {
	if len(nodes) == 0 {
		return
	}
	b.WriteString("\n## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, node := range nodes {
		b.WriteString("- ")
		if strings.TrimSpace(node.NotePath) != "" {
			b.WriteString(obsidianLink(node.NotePath, node.Title))
		} else {
			b.WriteString(node.Title)
		}
		b.WriteString(" (`")
		b.WriteString(node.Role)
		b.WriteString("`")
		if strings.TrimSpace(node.SourceType) != "" {
			b.WriteString(", `")
			b.WriteString(node.SourceType)
			b.WriteString("`")
		}
		b.WriteString(")")
		if strings.TrimSpace(node.URL) != "" {
			b.WriteString(" ")
			b.WriteString(node.URL)
		}
		b.WriteString("\n")
	}
}

func topicNodeLabel(graph topics.TopicMap, sourceKey string) string {
	for _, node := range graph.Nodes {
		if node.SourceKey != sourceKey {
			continue
		}
		if strings.TrimSpace(node.NotePath) != "" {
			return obsidianLink(node.NotePath, node.Title)
		}
		return node.Title
	}
	return sourceKey
}
