package entities

import (
	"sort"
	"strings"
)

func ensureBuilder(builders map[string]*builder, entity Entity) *builder {
	if existing, ok := builders[entity.Key]; ok {
		if existing.entity.Name == "" && entity.Name != "" {
			existing.entity.Name = entity.Name
		}
		if existing.entity.CanonicalURL == "" && entity.CanonicalURL != "" {
			existing.entity.CanonicalURL = entity.CanonicalURL
		}
		if existing.entity.Domain == "" && entity.Domain != "" {
			existing.entity.Domain = entity.Domain
		}
		if existing.entity.NotePath == "" && entity.NotePath != "" {
			existing.entity.NotePath = entity.NotePath
		}
		return existing
	}

	b := &builder{
		entity:          entity,
		aliases:         map[string]struct{}{},
		sourceTypes:     map[string]struct{}{},
		references:      map[string]Reference{},
		links:           map[string]Link{},
		referenceCounts: map[string]struct{}{},
	}
	builders[entity.Key] = b
	return b
}

func (b *builder) addAlias(value string) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, b.entity.Name) {
		return
	}
	b.aliases[value] = struct{}{}
}

func (b *builder) addSourceType(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.sourceTypes[value] = struct{}{}
}

func (b *builder) addReference(ref Reference) {
	if strings.TrimSpace(ref.SourceKey) == "" {
		return
	}
	ref.Title = strings.TrimSpace(ref.Title)
	key := ref.RefKind + "|" + ref.SourceKey + "|" + ref.Relationship
	b.references[key] = ref
	b.referenceCounts[ref.RefKind+"|"+ref.SourceKey] = struct{}{}
}

func (b *builder) addLink(link Link) {
	if strings.TrimSpace(link.Key) == "" {
		return
	}
	key := string(link.Kind) + "|" + link.Key + "|" + link.Relationship
	b.links[key] = link
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func sortedReferences(values map[string]Reference) []Reference {
	result := make([]Reference, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Relationship != result[j].Relationship {
			return result[i].Relationship < result[j].Relationship
		}
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	return result
}

func sortedLinks(values map[string]Link) []Link {
	result := make([]Link, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].Relationship != result[j].Relationship {
			return result[i].Relationship < result[j].Relationship
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}
