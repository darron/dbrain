package brainresearch

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/darron/dbrain/internal/ask"
)

var (
	handleAnchorRE    = regexp.MustCompile(`(^|[^A-Za-z0-9_])(@[A-Za-z0-9_]{2,32})`)
	underscoreAliasRE = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]+_[A-Za-z][A-Za-z0-9_]+\b`)
	hashtagAliasRE    = regexp.MustCompile(`(^|[^A-Za-z0-9_])(#[A-Za-z][A-Za-z0-9_-]{2,64})`)
)

func extractProtectedAnchors(raw string) []ProtectedAnchor {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	anchors := make([]ProtectedAnchor, 0)
	for _, sourceKey := range sourceKeyCandidates(raw) {
		anchors = append(anchors, anchorFromSourceKey(sourceKey, "current_user_text"))
	}
	for _, match := range handleAnchorRE.FindAllStringSubmatch(raw, -1) {
		if len(match) < 3 {
			continue
		}
		anchors = append(anchors, anchorFromHandle(match[2], "current_user_text"))
	}
	for _, match := range hashtagAliasRE.FindAllStringSubmatch(raw, -1) {
		if len(match) < 3 {
			continue
		}
		anchors = append(anchors, anchorFromHashtag(match[2], "current_user_text"))
	}
	if shouldPromoteUnderscoreAliases(raw) {
		for _, value := range underscoreAliasRE.FindAllString(raw, -1) {
			anchors = append(anchors, anchorFromUnderscoreAlias(value, "current_user_text"))
		}
	}
	return dedupeProtectedAnchors(anchors)
}

func HasCurrentProtectedAnchor(raw string) bool {
	return len(extractProtectedAnchors(raw)) > 0
}

func anchorFromHandle(raw string, source string) ProtectedAnchor {
	canonical, exact, phrase, expansion := anchorTerms(raw)
	return ProtectedAnchor{
		Kind:           "handle",
		Relation:       "authored_by",
		Raw:            strings.TrimSpace(raw),
		Canonical:      canonical,
		Source:         source,
		Confidence:     "exact",
		ExactTerms:     exact,
		PhraseTerms:    phrase,
		ExpansionTerms: expansion,
	}
}

func anchorFromUnderscoreAlias(raw string, source string) ProtectedAnchor {
	canonical, exact, phrase, expansion := anchorTerms(raw)
	return ProtectedAnchor{
		Kind:           "entity_alias",
		Relation:       "authored_by",
		Raw:            strings.TrimSpace(raw),
		Canonical:      canonical,
		Source:         source,
		Confidence:     "inferred",
		ExactTerms:     exact,
		PhraseTerms:    phrase,
		ExpansionTerms: expansion,
	}
}

func anchorFromHashtag(raw string, source string) ProtectedAnchor {
	canonical, exact, phrase, expansion := anchorTerms(strings.TrimPrefix(raw, "#"))
	exact = append([]string{strings.TrimSpace(raw)}, exact...)
	return ProtectedAnchor{
		Kind:           "tag_alias",
		Relation:       "tag",
		Raw:            strings.TrimSpace(raw),
		Canonical:      canonical,
		Source:         source,
		Confidence:     "inferred",
		ExactTerms:     uniqueStringsPreserveCase(exact),
		PhraseTerms:    phrase,
		ExpansionTerms: expansion,
	}
}

func anchorFromSourceKey(raw string, source string) ProtectedAnchor {
	value := strings.TrimSpace(raw)
	return ProtectedAnchor{
		Kind:       "source_key",
		Relation:   "source_key",
		Raw:        value,
		Canonical:  strings.ToLower(value),
		Source:     source,
		Confidence: "exact",
		ExactTerms: []string{value, strings.ToLower(value)},
	}
}

func anchorTerms(raw string) (canonical string, exact []string, phrase []string, expansion []string) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "@")
	value = strings.TrimPrefix(value, "#")
	canonical = strings.ToLower(value)
	phraseValue := strings.NewReplacer("_", " ", "-", " ").Replace(canonical)
	exact = uniqueStringsPreserveCase([]string{
		strings.TrimSpace(raw),
		value,
		canonical,
		"@" + value,
		"@" + canonical,
	})
	phrase = uniqueStrings([]string{phraseValue})
	expansion = uniqueStrings(strings.Fields(phraseValue))
	return canonical, exact, phrase, expansion
}

func dedupeProtectedAnchors(anchors []ProtectedAnchor) []ProtectedAnchor {
	out := make([]ProtectedAnchor, 0, len(anchors))
	seen := map[string]int{}
	for _, anchor := range anchors {
		anchor = normalizeProtectedAnchor(anchor)
		if anchor.Raw == "" && anchor.Canonical == "" {
			continue
		}
		key := protectedAnchorDedupeKey(anchor)
		if pos, exists := seen[key]; exists {
			out[pos] = mergeProtectedAnchor(out[pos], anchor)
			continue
		}
		seen[key] = len(out)
		out = append(out, anchor)
	}
	return out
}

func protectedAnchorDedupeKey(anchor ProtectedAnchor) string {
	identity := firstNonEmpty(anchor.ResolvedID, anchor.Canonical, strings.ToLower(anchor.Raw))
	if identity == "" {
		return ""
	}
	if anchor.ResolvedID != "" || anchor.Canonical != "" {
		return strings.Join([]string{anchor.Relation, identity}, "\x00")
	}
	return strings.Join([]string{anchor.Kind, anchor.Relation, identity}, "\x00")
}

func normalizeProtectedAnchor(anchor ProtectedAnchor) ProtectedAnchor {
	anchor.Kind = strings.ToLower(strings.TrimSpace(anchor.Kind))
	anchor.Relation = strings.ToLower(strings.TrimSpace(anchor.Relation))
	anchor.Raw = strings.TrimSpace(anchor.Raw)
	anchor.Canonical = strings.ToLower(strings.TrimSpace(anchor.Canonical))
	anchor.ResolvedID = strings.TrimSpace(anchor.ResolvedID)
	anchor.Source = strings.TrimSpace(anchor.Source)
	anchor.Confidence = strings.TrimSpace(anchor.Confidence)
	anchor.ExactTerms = uniqueStringsPreserveCase(trimNonEmpty(anchor.ExactTerms))
	anchor.PhraseTerms = uniqueStrings(trimNonEmpty(anchor.PhraseTerms))
	anchor.ExpansionTerms = uniqueStrings(trimNonEmpty(anchor.ExpansionTerms))
	return anchor
}

func mergeProtectedAnchor(current ProtectedAnchor, next ProtectedAnchor) ProtectedAnchor {
	if current.ResolvedID == "" {
		current.ResolvedID = next.ResolvedID
	}
	if current.Source == "" {
		current.Source = next.Source
	}
	if current.Confidence == "" || next.Confidence == "exact" {
		current.Confidence = next.Confidence
	}
	current.ExactTerms = uniqueStringsPreserveCase(append(current.ExactTerms, next.ExactTerms...))
	current.PhraseTerms = uniqueStrings(append(current.PhraseTerms, next.PhraseTerms...))
	current.ExpansionTerms = uniqueStrings(append(current.ExpansionTerms, next.ExpansionTerms...))
	return current
}

func shouldPromoteUnderscoreAliases(raw string) bool {
	lower := strings.ToLower(raw)
	if strings.ContainsAny(raw, "`{};=") {
		return false
	}
	if strings.Contains(lower, "user_id") || strings.Contains(lower, "created_at") || strings.Contains(lower, "max_retries") {
		return false
	}
	for _, term := range []string{"@", "tweet", "tweets", "post", "posts", "author", "handle", "collection", "essays"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return quotedUnderscoreAlias(raw)
}

func quotedUnderscoreAlias(raw string) bool {
	for _, quote := range []rune{'"', '\''} {
		inQuote := false
		var b strings.Builder
		for _, r := range raw {
			if r == quote {
				if inQuote && underscoreAliasRE.MatchString(b.String()) {
					return true
				}
				inQuote = !inQuote
				b.Reset()
				continue
			}
			if inQuote {
				b.WriteRune(r)
			}
		}
	}
	return false
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func uniqueStringsPreserveCase(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func EvidenceMatchesProtectedAnchor(row ask.Evidence, anchor ProtectedAnchor) bool {
	anchor = normalizeProtectedAnchor(anchor)
	if anchor.Kind == "source_key" {
		for _, term := range anchor.ExactTerms {
			if strings.EqualFold(strings.TrimSpace(row.SourceKey), term) {
				return true
			}
		}
	}
	haystack := strings.ToLower(strings.Join([]string{
		row.SourceKey,
		row.URL,
		row.Author,
		row.UserTags,
		strings.Join(row.EntityMatches, " "),
		row.Title,
		row.Summary,
		row.Excerpt,
		row.NotePath,
	}, "\n"))
	for _, section := range row.ContentSections {
		haystack += "\n" + strings.ToLower(section.Text)
	}
	for _, value := range append(append([]string{anchor.ResolvedID, anchor.Canonical}, anchor.ExactTerms...), anchor.PhraseTerms...) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if containsTerm(haystack, value) || strings.Contains(haystack, value) {
			return true
		}
	}
	return false
}

func EvidenceMatchesAnyProtectedAnchor(row ask.Evidence, anchors []ProtectedAnchor) bool {
	for _, anchor := range anchors {
		if EvidenceMatchesProtectedAnchor(row, anchor) {
			return true
		}
	}
	return false
}

func normalizedAnchorLookupValues(anchor ProtectedAnchor) []string {
	values := []string{anchor.Canonical, anchor.Raw, strings.TrimPrefix(anchor.Raw, "@"), strings.TrimPrefix(anchor.Raw, "#")}
	values = append(values, anchor.ExactTerms...)
	values = append(values, anchor.PhraseTerms...)
	out := make([]string, 0, len(values)*2)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, strings.ToLower(value))
		out = append(out, normalizeAnchorToken(value))
	}
	return uniqueStrings(trimNonEmpty(out))
}

func normalizeAnchorToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "@")
	value = strings.TrimPrefix(value, "#")
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || unicode.IsSpace(r):
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
