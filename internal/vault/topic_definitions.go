package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/topics"
)

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
