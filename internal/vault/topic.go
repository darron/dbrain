package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/topics"
)

var nonTopicSlug = regexp.MustCompile(`[^a-z0-9]+`)

func TopicNoteRelativePath(topic string) string {
	slug := topicSlug(topic)
	return filepath.ToSlash(filepath.Join("topics", slug+".md"))
}

func TopicIndexRelativePath() string {
	return filepath.ToSlash(filepath.Join("topics", "index.md"))
}

func WriteTopic(cfg config.Config, graph topics.TopicMap) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(TopicNoteRelativePath(graph.Topic)))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create topic note dir: %w", err)
	}

	body := RenderTopic(graph)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write topic note: %w", err)
	}
	return nil
}

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

func WriteTopicIndex(cfg config.Config, defs []topics.Definition) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(TopicIndexRelativePath()))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create topic index dir: %w", err)
	}

	body := RenderTopicIndex(defs)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write topic index: %w", err)
	}
	return nil
}

func RenderTopicIndex(defs []topics.Definition) string {
	sorted := append([]topics.Definition(nil), defs...)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Topic) < strings.ToLower(sorted[j].Topic)
	})

	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "brain_topic_index", "true")
	writeYAMLScalar(&b, "topic_count", strconv.Itoa(len(sorted)))
	writeYAMLArray(&b, "tags", []string{"source/topic-index"})
	b.WriteString("---\n\n")

	b.WriteString("# Topic Index\n\n")
	b.WriteString("Browsable index of generated topic notes.\n")

	if len(sorted) == 0 {
		b.WriteString("\nNo topic notes have been generated yet.\n")
		return b.String()
	}

	b.WriteString("\n## Topics\n\n")
	for _, def := range sorted {
		notePath := strings.TrimSpace(def.NotePath)
		if notePath == "" {
			notePath = TopicNoteRelativePath(def.Topic)
		}
		b.WriteString("- ")
		b.WriteString(obsidianLink(notePath, def.Topic))
		b.WriteString(" (`")
		b.WriteString(strconv.Itoa(def.SeedLimit))
		b.WriteString("` seeds, `")
		b.WriteString(strconv.Itoa(def.RelatedLimit))
		b.WriteString("` related")
		if len(def.SourceTypes) > 0 {
			b.WriteString(", filters: ")
			b.WriteString(strings.Join(def.SourceTypes, ", "))
		}
		b.WriteString(")\n")
	}

	return b.String()
}

func ListTopicDefinitions(cfg config.Config) ([]topics.Definition, error) {
	topicsDir := filepath.Join(cfg.VaultDir, "topics")
	entries := make([]topics.Definition, 0)

	err := filepath.WalkDir(topicsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		if filepath.Base(path) == "index.md" {
			return nil
		}

		relPath, err := filepath.Rel(cfg.VaultDir, path)
		if err != nil {
			return err
		}
		def, err := readTopicDefinitionFile(path, filepath.ToSlash(relPath))
		if err != nil {
			return err
		}
		entries = append(entries, def)
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Topic) < strings.ToLower(entries[j].Topic)
	})
	return entries, nil
}

func ReadTopicDefinition(cfg config.Config, topic string) (topics.Definition, error) {
	relPath := TopicNoteRelativePath(topic)
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(relPath))
	return readTopicDefinitionFile(fullPath, relPath)
}

func readTopicDefinitionFile(fullPath, relPath string) (topics.Definition, error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return topics.Definition{}, err
	}
	def, err := parseTopicDefinition(string(data))
	if err != nil {
		return topics.Definition{}, err
	}
	def.NotePath = filepath.ToSlash(relPath)
	return def, nil
}

func parseTopicDefinition(body string) (topics.Definition, error) {
	frontmatter, err := extractFrontmatter(body)
	if err != nil {
		return topics.Definition{}, err
	}

	var def topics.Definition
	var currentArray string

	for _, rawLine := range strings.Split(frontmatter, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			currentArray = ""
			continue
		}
		if currentArray != "" && strings.HasPrefix(line, "  - ") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "  - "))
			if currentArray == "source_types" {
				def.SourceTypes = append(def.SourceTypes, yamlUnquote(value))
			}
			continue
		}

		currentArray = ""
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch value {
		case "":
			currentArray = key
		case "[]":
			if key == "source_types" {
				def.SourceTypes = nil
			}
		default:
			switch key {
			case "brain_topic":
				def.Topic = yamlUnquote(value)
			case "seed_limit":
				def.SeedLimit = parseTopicInt(value, 6)
			case "related_limit":
				def.RelatedLimit = parseTopicInt(value, 2)
			}
		}
	}

	if strings.TrimSpace(def.Topic) == "" {
		return topics.Definition{}, fmt.Errorf("topic note missing brain_topic frontmatter")
	}
	if def.SeedLimit <= 0 {
		def.SeedLimit = 6
	}
	if def.RelatedLimit <= 0 {
		def.RelatedLimit = 2
	}
	return def, nil
}

func extractFrontmatter(body string) (string, error) {
	if !strings.HasPrefix(body, "---\n") {
		return "", fmt.Errorf("topic note missing frontmatter")
	}
	rest := strings.TrimPrefix(body, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return "", fmt.Errorf("topic note has malformed frontmatter")
	}
	return rest[:idx], nil
}

func parseTopicInt(value string, fallback int) int {
	raw := yamlUnquote(value)
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func yamlUnquote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return value
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

func topicSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonTopicSlug.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "topic"
	}
	if len(value) > 80 {
		return strings.Trim(value[:80], "-")
	}
	return value
}
