package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/topics"
)

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
