package vault

import (
	"path/filepath"
	"regexp"
	"strings"
)

var nonTopicSlug = regexp.MustCompile(`[^a-z0-9]+`)

func TopicNoteRelativePath(topic string) string {
	slug := topicSlug(topic)
	return filepath.ToSlash(filepath.Join("topics", slug+".md"))
}

func TopicIndexRelativePath() string {
	return filepath.ToSlash(filepath.Join("topics", "index.md"))
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
