package entities

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/store"
)

func BuildIndex(ctx context.Context, st *store.Store) ([]Entity, error) {
	items, err := st.ListAllEntityItems(ctx, 0)
	if err != nil {
		return nil, err
	}
	sources, err := st.ListAllEntitySources(ctx, 0)
	if err != nil {
		return nil, err
	}

	builders := map[string]*builder{}

	for _, item := range items {
		addItemEntities(builders, item)
	}
	for _, source := range sources {
		addSourceEntities(builders, source)
	}
	augmentEntityRelationships(builders)

	entities := make([]Entity, 0, len(builders))
	for _, b := range builders {
		entity := b.entity
		entity.Aliases = sortedKeys(b.aliases)
		entity.SourceTypes = sortedKeys(b.sourceTypes)
		entity.ReferenceCount = len(b.referenceCounts)
		entity.References = sortedReferences(b.references)
		entity.Links = sortedLinks(b.links)
		entities = append(entities, entity)
	}

	sort.Slice(entities, func(i, j int) bool {
		if entities[i].ReferenceCount != entities[j].ReferenceCount {
			return entities[i].ReferenceCount > entities[j].ReferenceCount
		}
		if entities[i].Kind != entities[j].Kind {
			return entities[i].Kind < entities[j].Kind
		}
		return strings.ToLower(entities[i].Name) < strings.ToLower(entities[j].Name)
	})
	return entities, nil
}

func Search(ctx context.Context, st *store.Store, query string, opts SearchOptions) ([]Entity, error) {
	entities, err := BuildIndex(ctx, st)
	if err != nil {
		return nil, err
	}
	return Filter(entities, query, opts), nil
}

func Filter(entities []Entity, query string, opts SearchOptions) []Entity {
	kind := normalizeKind(opts.Kind)
	query = strings.ToLower(strings.TrimSpace(query))

	filtered := make([]Entity, 0, len(entities))
	for _, entity := range entities {
		if kind != "" && string(entity.Kind) != kind {
			continue
		}
		if query == "" || entityMatches(entity, query) {
			filtered = append(filtered, entity)
		}
	}

	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}
	return filtered
}

func FormatText(entities []Entity) string {
	if len(entities) == 0 {
		return "Entities: 0"
	}

	var b strings.Builder
	b.WriteString("Entities:\n")
	for _, entity := range entities {
		_, _ = fmt.Fprintf(&b, "- [%s] %s (%s) refs=%d", entity.Kind, entity.Name, entity.Key, entity.ReferenceCount)
		if entity.NotePath != "" {
			_, _ = fmt.Fprintf(&b, " note=%s", entity.NotePath)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
