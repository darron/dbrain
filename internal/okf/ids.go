package okf

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

var slugReplaceRE = regexp.MustCompile(`[^a-z0-9]+`)

func ItemConceptID(item model.Item) string {
	return "item/" + escapeConceptKey(item.SourceKey)
}

func SourceConceptID(source model.SourceDocument) string {
	return "source/" + escapeConceptKey(source.SourceKey)
}

func EntityConceptID(kind, key string) string {
	return "entity/" + escapeConceptKey(kind+"/"+key)
}

func TopicConceptID(topic string) string {
	return "topic/" + escapeConceptKey(topic)
}

func ItemPath(item model.Item) string {
	sourceType := slugComponent(item.SourceType, "item")
	year := itemYear(item)
	label := firstNonEmpty(item.ExternalID, item.Title, item.AuthorHandle, item.SourceKey)
	slug := slugWithHash(label, item.SourceKey)
	return path.Join("items", sourceType, year, slug+".md")
}

func SourcePath(source model.SourceDocument) string {
	sourceType := slugComponent(source.SourceType, "source")
	label := firstNonEmpty(source.Domain, source.Title, source.SourceKey)
	slug := slugWithHash(label, source.SourceKey)
	return path.Join("sources", sourceType, slug+".md")
}

func EntityPath(kind, key string) string {
	kindSlug := slugComponent(kind, "entity")
	return path.Join("entities", kindSlug, slugWithHash(key, kind+"/"+key)+".md")
}

func TopicPath(topic string) string {
	return path.Join("topics", slugWithHash(topic, topic)+".md")
}

func itemYear(item model.Item) string {
	for _, value := range []string{item.PublishedAt, item.SavedAt, timeString(item.UpdatedAt)} {
		value = strings.TrimSpace(value)
		if len(value) >= 4 && allDigits(value[:4]) {
			return value[:4]
		}
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return fmt.Sprintf("%04d", parsed.UTC().Year())
		}
	}
	return "undated"
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func normalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return value
}

func escapeConceptKey(value string) string {
	replacer := strings.NewReplacer("%", "%25", "/", "%2F", ":", "%3A", " ", "%20")
	return replacer.Replace(strings.TrimSpace(value))
}

func slugWithHash(label, identity string) string {
	slug := slugComponent(label, "concept")
	hash := shortHash(identity)
	if len(slug) > 72 {
		slug = strings.Trim(slug[:72], "-")
	}
	return slug + "-" + hash
}

func slugComponent(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugReplaceRE.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		value = fallback
	}
	if len(value) > 96 {
		value = strings.Trim(value[:96], "-")
	}
	if value == "" {
		return fallback
	}
	return value
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:10]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "concept"
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
