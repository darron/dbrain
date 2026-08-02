package okf

import (
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/entities"
)

func renderEntityDocument(entity entityDoc, opts ExportOptions, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string, timestampBySourceKey map[string]string) (Document, []OmittedLink, error) {
	sources, err := entitySourceReferences(entity, pathByConceptID, conceptIDBySourceKey)
	if err != nil {
		return Document{}, nil, err
	}
	doc := Document{
		Path:        entity.Path,
		Type:        "Entity",
		Title:       entityTitle(entity.Entity),
		Description: entityDescription(entity.Entity),
		Resource:    firstNonEmpty(entity.Entity.CanonicalURL, "dbrain://"+entity.ConceptID),
		Tags:        entityTags(entity.Entity),
		Generated:   generatedMetadata(opts, latestEvidenceTimestamp(entityReferenceSourceKeys(entity.Entity), timestampBySourceKey)),
		Sources:     sources,
		Fields: []Field{
			{Name: "dbrain_concept_id", Value: entity.ConceptID},
			{Name: "dbrain_kind", Value: "entity"},
			{Name: "dbrain_derived", Value: true},
			{Name: "dbrain_entity_kind", Value: string(entity.Entity.Kind)},
			{Name: "dbrain_entity_key", Value: entity.Entity.Key},
			{Name: "dbrain_evidence_count", Value: entity.Entity.ReferenceCount},
			{Name: "dbrain_note_path", Value: entity.Entity.NotePath},
			{Name: "canonical_url", Value: entity.Entity.CanonicalURL},
			{Name: "domain", Value: entity.Entity.Domain},
		},
	}

	var body strings.Builder
	writeSection(&body, "Overview", doc.Description)
	writeEntityDetails(&body, entity.Entity)
	writeEntityAliases(&body, entity.Entity)
	omittedRelated, err := writeEntityLinks(&body, entity, pathByConceptID)
	if err != nil {
		return Document{}, nil, err
	}
	omittedRefs, err := writeEntityReferences(&body, entity, pathByConceptID, conceptIDBySourceKey)
	if err != nil {
		return Document{}, nil, err
	}
	doc.Body = strings.TrimSpace(body.String()) + "\n"
	return doc, append(omittedRelated, omittedRefs...), nil
}

func entityReferenceSourceKeys(entity entities.Entity) []string {
	keys := make([]string, 0, len(entity.References))
	for _, ref := range entity.References {
		keys = append(keys, ref.SourceKey)
	}
	return keys
}

func entitySourceReferences(entity entityDoc, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) ([]SourceReference, error) {
	refs := append([]entities.Reference(nil), entity.Entity.References...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].SourceKey == refs[j].SourceKey {
			return refs[i].URL < refs[j].URL
		}
		return refs[i].SourceKey < refs[j].SourceKey
	})
	seen := map[string]struct{}{}
	sources := make([]SourceReference, 0, len(refs))
	for _, ref := range refs {
		resource := strings.TrimSpace(ref.URL)
		if targetPath := pathByConceptID[conceptIDBySourceKey[ref.SourceKey]]; targetPath != "" {
			rel, err := RelativeLink(entity.Path, targetPath)
			if err != nil {
				return nil, err
			}
			resource = rel
		}
		if resource == "" {
			continue
		}
		if _, ok := seen[resource]; ok {
			continue
		}
		seen[resource] = struct{}{}
		sources = append(sources, SourceReference{Resource: resource, Title: firstNonEmpty(ref.Title, ref.URL, ref.SourceKey)})
	}
	return sources, nil
}

func entityTitle(entity entities.Entity) string {
	return firstNonEmpty(entity.Name, entity.Key)
}

func entityDescription(entity entities.Entity) string {
	kind := strings.TrimSpace(string(entity.Kind))
	if kind == "" {
		kind = "entity"
	}
	count := entity.ReferenceCount
	if count == 1 {
		return fmt.Sprintf("Derived %s entity from 1 local dbrain reference.", kind)
	}
	return fmt.Sprintf("Derived %s entity from %d local dbrain references.", kind, count)
}

func entityTags(entity entities.Entity) []string {
	var tags []string
	if value := normalizeTag(string(entity.Kind)); value != "" {
		tags = append(tags, "entity/"+value)
	}
	for _, sourceType := range entity.SourceTypes {
		if value := normalizeTag(sourceType); value != "" {
			tags = append(tags, "source/"+value)
		}
	}
	if value := normalizeTag(entity.Domain); value != "" {
		tags = append(tags, "domain/"+value)
	}
	return sortedUnique(tags)
}

func writeEntityDetails(b *strings.Builder, entity entities.Entity) {
	b.WriteString("\n# Entity\n\n")
	writeBullet(b, "Entity key", code(entity.Key))
	writeBullet(b, "Kind", code(string(entity.Kind)))
	writeBullet(b, "Canonical URL", entity.CanonicalURL)
	writeBullet(b, "Domain", code(entity.Domain))
	writeBullet(b, "Evidence references", fmt.Sprintf("%d", entity.ReferenceCount))
	if len(entity.SourceTypes) > 0 {
		writeBullet(b, "Source types", code(strings.Join(entity.SourceTypes, ", ")))
	}
}

func writeEntityAliases(b *strings.Builder, entity entities.Entity) {
	if len(entity.Aliases) == 0 {
		return
	}
	var body strings.Builder
	for _, alias := range entity.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		body.WriteString("- ")
		body.WriteString(alias)
		body.WriteString("\n")
	}
	writeSection(b, "Aliases", body.String())
}

func writeEntityLinks(b *strings.Builder, entity entityDoc, pathByConceptID map[string]string) ([]OmittedLink, error) {
	if len(entity.Entity.Links) == 0 {
		return nil, nil
	}
	links := append([]entities.Link(nil), entity.Entity.Links...)
	sort.Slice(links, func(i, j int) bool {
		if links[i].Kind != links[j].Kind {
			return links[i].Kind < links[j].Kind
		}
		return strings.ToLower(links[i].Name) < strings.ToLower(links[j].Name)
	})

	var body strings.Builder
	var omitted []OmittedLink
	for _, link := range links {
		conceptID := EntityConceptID(string(link.Kind), link.Key)
		targetPath := pathByConceptID[conceptID]
		if targetPath == "" {
			omitted = append(omitted, omittedByFilter(entity.Path, link.NotePath, conceptID))
			writePlainEntityReference(&body, firstNonEmpty(link.Name, link.Key), link.Relationship, string(link.Kind), link.Key)
			continue
		}
		rel, err := RelativeLink(entity.Path, targetPath)
		if err != nil {
			return nil, err
		}
		body.WriteString("- ")
		body.WriteString(MarkdownLink(firstNonEmpty(link.Name, link.Key), rel))
		writeRelationshipSuffix(&body, link.Relationship, string(link.Kind), link.Key)
	}
	writeSection(b, "Related Entities", body.String())
	return omitted, nil
}

func writeEntityReferences(b *strings.Builder, entity entityDoc, pathByConceptID map[string]string, conceptIDBySourceKey map[string]string) ([]OmittedLink, error) {
	if len(entity.Entity.References) == 0 {
		return nil, nil
	}
	refs := append([]entities.Reference(nil), entity.Entity.References...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].SourceKey == refs[j].SourceKey {
			return refs[i].Relationship < refs[j].Relationship
		}
		return refs[i].SourceKey < refs[j].SourceKey
	})

	var body strings.Builder
	var omitted []OmittedLink
	for _, ref := range refs {
		conceptID := conceptIDBySourceKey[ref.SourceKey]
		targetPath := pathByConceptID[conceptID]
		if targetPath == "" {
			omitted = append(omitted, omittedByFilter(entity.Path, firstNonEmpty(ref.NotePath, ref.SourceKey), conceptID))
			writePlainEntityReference(&body, firstNonEmpty(ref.Title, ref.URL, ref.SourceKey), ref.Relationship, ref.SourceType, ref.SourceKey)
			continue
		}
		rel, err := RelativeLink(entity.Path, targetPath)
		if err != nil {
			return nil, err
		}
		body.WriteString("- ")
		body.WriteString(MarkdownLink(firstNonEmpty(ref.Title, ref.URL, ref.SourceKey), rel))
		writeRelationshipSuffix(&body, ref.Relationship, ref.SourceType, ref.SourceKey)
	}
	writeSection(b, "Referenced By", body.String())
	return omitted, nil
}

func writePlainEntityReference(b *strings.Builder, label, relationship, kind, key string) {
	b.WriteString("- ")
	b.WriteString(label)
	writeRelationshipSuffix(b, relationship, kind, key)
}

func writeRelationshipSuffix(b *strings.Builder, relationship, kind, key string) {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(relationship) != "" {
		parts = append(parts, relationship)
	}
	if strings.TrimSpace(kind) != "" {
		parts = append(parts, kind)
	}
	if strings.TrimSpace(key) != "" {
		parts = append(parts, key)
	}
	if len(parts) > 0 {
		b.WriteString(" - ")
		b.WriteString(strings.Join(parts, "; "))
	}
	b.WriteString("\n")
}
