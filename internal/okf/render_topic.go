package okf

import (
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/topics"
)

func renderTopicDocument(topic topicDoc, _ ExportOptions, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) (Document, []OmittedLink, error) {
	doc := Document{
		Path:        topic.Path,
		Type:        "Topic",
		Title:       topicTitle(topic.Topic),
		Description: topicDescription(topic.Topic),
		Resource:    "dbrain://" + topic.ConceptID,
		Tags:        topicTags(topic.Topic),
		Fields: []Field{
			{Name: "dbrain_concept_id", Value: topic.ConceptID},
			{Name: "dbrain_kind", Value: "topic"},
			{Name: "dbrain_derived", Value: true},
			{Name: "dbrain_topic", Value: topic.Topic.Topic},
			{Name: "dbrain_evidence_count", Value: len(topic.Topic.Nodes)},
			{Name: "source_types", Value: topic.Topic.SourceTypes},
			{Name: "seed_limit", Value: topic.Topic.SeedLimit},
			{Name: "related_limit", Value: topic.Topic.RelatedLimit},
		},
	}

	var body strings.Builder
	writeSection(&body, "Overview", doc.Description)
	writeTopicOptions(&body, topic.Topic)
	writeTopicSynthesis(&body, topic.Topic)
	omittedEntities, err := writeTopicEntities(&body, topic, pathByConceptID)
	if err != nil {
		return Document{}, nil, err
	}
	omittedNodes, err := writeTopicNodes(&body, topic, pathByConceptID, conceptIDBySourceKey)
	if err != nil {
		return Document{}, nil, err
	}
	omittedEdges, err := writeTopicRelationships(&body, topic, pathByConceptID, conceptIDBySourceKey)
	if err != nil {
		return Document{}, nil, err
	}
	doc.Body = strings.TrimSpace(body.String()) + "\n"
	omitted := append(omittedEntities, omittedNodes...)
	omitted = append(omitted, omittedEdges...)
	return doc, omitted, nil
}

func topicTitle(topic topics.TopicMap) string {
	return firstNonEmpty(topic.Topic, "Topic")
}

func topicDescription(topic topics.TopicMap) string {
	if summary := strings.TrimSpace(topics.SummaryText(topic)); summary != "" {
		return summary
	}
	return "Derived topic view over local dbrain evidence."
}

func topicTags(topic topics.TopicMap) []string {
	tags := []string{"source/topic"}
	if value := normalizeTag(topic.Topic); value != "" {
		tags = append(tags, "topic/"+value)
	}
	for _, sourceType := range topic.SourceTypes {
		if value := normalizeTag(sourceType); value != "" {
			tags = append(tags, "source/"+value)
		}
	}
	return sortedUnique(tags)
}

func writeTopicOptions(b *strings.Builder, topic topics.TopicMap) {
	var body strings.Builder
	writeBullet(&body, "Topic", topic.Topic)
	writeBullet(&body, "Seed limit", fmt.Sprintf("%d", topic.SeedLimit))
	writeBullet(&body, "Related limit", fmt.Sprintf("%d", topic.RelatedLimit))
	if len(topic.SourceTypes) > 0 {
		writeBullet(&body, "Source types", code(strings.Join(topic.SourceTypes, ", ")))
	}
	writeBullet(&body, "Evidence nodes", fmt.Sprintf("%d", len(topic.Nodes)))
	writeBullet(&body, "Graph relationships", fmt.Sprintf("%d", len(topic.Edges)))
	writeSection(b, "Topic Map", body.String())
}

func writeTopicSynthesis(b *strings.Builder, topic topics.TopicMap) {
	var body strings.Builder
	if text := strings.TrimSpace(topic.Synthesis.Overview); text != "" {
		body.WriteString("## Derived Overview\n\n")
		body.WriteString(text)
		body.WriteString("\n\n")
	}
	if text := strings.TrimSpace(topic.Synthesis.WhyItMatters); text != "" {
		body.WriteString("## Why It Matters\n\n")
		body.WriteString(text)
		body.WriteString("\n\n")
	}
	writeTopicStringList(&body, "Angles", topic.Synthesis.Angles)
	writeTopicSignals(&body, topic.Synthesis.Signals)
	writeTopicStringList(&body, "Open Questions", topic.Synthesis.OpenQuestions)
	writeUnvalidatedSection(b, "Derived Synthesis", body.String())
}

func writeTopicStringList(b *strings.Builder, heading string, values []string) {
	if len(values) == 0 {
		return
	}
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeTopicSignals(b *strings.Builder, signals []topics.TopicSignal) {
	if len(signals) == 0 {
		return
	}
	b.WriteString("## Signals\n\n")
	for _, signal := range signals {
		title := strings.TrimSpace(signal.Title)
		detail := strings.TrimSpace(signal.Detail)
		if title == "" && detail == "" {
			continue
		}
		if title == "" {
			title = "Signal"
		}
		b.WriteString("- ")
		b.WriteString(title)
		if detail != "" {
			b.WriteString(": ")
			b.WriteString(detail)
		}
		if len(signal.SourceKeys) > 0 {
			b.WriteString(" (")
			b.WriteString(strings.Join(signal.SourceKeys, ", "))
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeTopicEntities(b *strings.Builder, topic topicDoc, pathByConceptID map[string]string) ([]OmittedLink, error) {
	if len(topic.Topic.Entities) == 0 {
		return nil, nil
	}
	entities := append([]topics.TopicEntity(nil), topic.Topic.Entities...)
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].MatchedReferences != entities[j].MatchedReferences {
			return entities[i].MatchedReferences > entities[j].MatchedReferences
		}
		if entities[i].Kind != entities[j].Kind {
			return entities[i].Kind < entities[j].Kind
		}
		return strings.ToLower(entities[i].Name) < strings.ToLower(entities[j].Name)
	})

	var body strings.Builder
	var omitted []OmittedLink
	for _, entity := range entities {
		conceptID := EntityConceptID(entity.Kind, entity.Key)
		targetPath := pathByConceptID[conceptID]
		if targetPath == "" {
			omitted = append(omitted, omittedByFilter(topic.Path, entity.NotePath, conceptID))
			body.WriteString("- ")
			body.WriteString(firstNonEmpty(entity.Name, entity.Key))
		} else {
			rel, err := RelativeLink(topic.Path, targetPath)
			if err != nil {
				return nil, err
			}
			body.WriteString("- ")
			body.WriteString(MarkdownLink(firstNonEmpty(entity.Name, entity.Key), rel))
		}
		_, _ = fmt.Fprintf(&body, " - %s; matched refs=%d; total refs=%d\n", entity.Kind, entity.MatchedReferences, entity.ReferenceCount)
	}
	writeSection(b, "Key Entities", body.String())
	return omitted, nil
}

func writeTopicNodes(b *strings.Builder, topic topicDoc, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) ([]OmittedLink, error) {
	if len(topic.Topic.Nodes) == 0 {
		return nil, nil
	}
	nodes := append([]topics.TopicMapNode(nil), topic.Topic.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Role == nodes[j].Role {
			return nodes[i].SourceKey < nodes[j].SourceKey
		}
		return nodes[i].Role < nodes[j].Role
	})

	var body strings.Builder
	var omitted []OmittedLink
	for _, node := range nodes {
		linked, err := writeTopicNodeLink(&body, topic.Path, node, pathByConceptID, conceptIDBySourceKey)
		if err != nil {
			return nil, err
		}
		if !linked {
			omitted = append(omitted, omittedByFilter(topic.Path, firstNonEmpty(node.NotePath, node.SourceKey), conceptIDBySourceKey[node.SourceKey]))
		}
	}
	writeSection(b, "Evidence Nodes", body.String())
	return omitted, nil
}

func writeTopicRelationships(b *strings.Builder, topic topicDoc, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) ([]OmittedLink, error) {
	if len(topic.Topic.Edges) == 0 {
		return nil, nil
	}
	edges := append([]topics.TopicMapEdge(nil), topic.Topic.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			if edges[i].To == edges[j].To {
				return edges[i].Relationship < edges[j].Relationship
			}
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})

	var body strings.Builder
	var omitted []OmittedLink
	seenOmitted := map[string]struct{}{}
	for _, edge := range edges {
		body.WriteString("- ")
		fromLinked, err := writeTopicSourceKeyLink(&body, topic.Path, edge.From, pathByConceptID, conceptIDBySourceKey)
		if err != nil {
			return nil, err
		}
		body.WriteString(" --")
		body.WriteString(firstNonEmpty(edge.Relationship, "related"))
		body.WriteString("--> ")
		toLinked, err := writeTopicSourceKeyLink(&body, topic.Path, edge.To, pathByConceptID, conceptIDBySourceKey)
		if err != nil {
			return nil, err
		}
		body.WriteString("\n")
		if !fromLinked {
			appendTopicOmitted(&omitted, seenOmitted, topic.Path, edge.From, conceptIDBySourceKey[edge.From])
		}
		if !toLinked {
			appendTopicOmitted(&omitted, seenOmitted, topic.Path, edge.To, conceptIDBySourceKey[edge.To])
		}
	}
	writeSection(b, "Relationships", body.String())
	return omitted, nil
}

func writeTopicNodeLink(b *strings.Builder, fromPath string, node topics.TopicMapNode, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) (bool, error) {
	b.WriteString("- ")
	linked, err := writeTopicSourceKeyLabelLink(b, fromPath, node.SourceKey, firstNonEmpty(node.Title, node.URL, node.SourceKey), pathByConceptID, conceptIDBySourceKey)
	if err != nil {
		return false, err
	}
	parts := make([]string, 0, 3)
	if strings.TrimSpace(node.Role) != "" {
		parts = append(parts, node.Role)
	}
	if strings.TrimSpace(node.Kind) != "" {
		parts = append(parts, node.Kind)
	}
	if strings.TrimSpace(node.SourceType) != "" {
		parts = append(parts, node.SourceType)
	}
	if len(parts) > 0 {
		b.WriteString(" - ")
		b.WriteString(strings.Join(parts, "; "))
	}
	b.WriteString("\n")
	return linked, nil
}

func writeTopicSourceKeyLink(b *strings.Builder, fromPath, sourceKey string, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) (bool, error) {
	return writeTopicSourceKeyLabelLink(b, fromPath, sourceKey, sourceKey, pathByConceptID, conceptIDBySourceKey)
}

func writeTopicSourceKeyLabelLink(b *strings.Builder, fromPath, sourceKey, label string, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) (bool, error) {
	conceptID := conceptIDBySourceKey[sourceKey]
	targetPath := pathByConceptID[conceptID]
	if targetPath == "" {
		b.WriteString(firstNonEmpty(label, sourceKey))
		return false, nil
	}
	rel, err := RelativeLink(fromPath, targetPath)
	if err != nil {
		return false, err
	}
	b.WriteString(MarkdownLink(firstNonEmpty(label, sourceKey), rel))
	return true, nil
}

func appendTopicOmitted(omitted *[]OmittedLink, seen map[string]struct{}, fromPath, targetPath, conceptID string) {
	key := fromPath + "\x00" + targetPath + "\x00" + conceptID
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*omitted = append(*omitted, omittedByFilter(fromPath, targetPath, conceptID))
}
