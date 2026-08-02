package okf

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func BuildBundle(snapshot store.OKFExportSnapshot, opts ExportOptions) (Bundle, error) {
	normalized := normalizeExportOptions(opts)
	items := make([]itemDoc, 0, len(snapshot.Items))
	sources := make([]sourceDoc, 0, len(snapshot.Sources))
	entities := make([]entityDoc, 0, len(normalized.Entities))
	topicDocs := make([]topicDoc, 0, len(normalized.Topics))

	for _, item := range snapshot.Items {
		if !normalized.IncludeItems {
			continue
		}
		items = append(items, itemDoc{Item: item, Path: ItemPath(item), ConceptID: ItemConceptID(item)})
	}
	for _, source := range snapshot.Sources {
		if !normalized.IncludeSources {
			continue
		}
		sources = append(sources, sourceDoc{Source: source, Path: SourcePath(source), ConceptID: SourceConceptID(source)})
	}
	if normalized.IncludeEntities {
		for _, entity := range normalized.Entities {
			entities = append(entities, entityDoc{Entity: entity, Path: EntityPath(string(entity.Kind), entity.Key), ConceptID: EntityConceptID(string(entity.Kind), entity.Key)})
		}
	}
	if normalized.IncludeTopics {
		for _, topic := range normalized.Topics {
			topicDocs = append(topicDocs, topicDoc{Topic: topic, Path: TopicPath(topic.Topic), ConceptID: TopicConceptID(topic.Topic)})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path == items[j].Path {
			return items[i].Item.SourceKey < items[j].Item.SourceKey
		}
		return items[i].Path < items[j].Path
	})
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Path == sources[j].Path {
			return sources[i].Source.SourceKey < sources[j].Source.SourceKey
		}
		return sources[i].Path < sources[j].Path
	})
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Path == entities[j].Path {
			return entities[i].Entity.Key < entities[j].Entity.Key
		}
		return entities[i].Path < entities[j].Path
	})
	sort.Slice(topicDocs, func(i, j int) bool {
		if topicDocs[i].Path == topicDocs[j].Path {
			return strings.ToLower(topicDocs[i].Topic.Topic) < strings.ToLower(topicDocs[j].Topic.Topic)
		}
		return topicDocs[i].Path < topicDocs[j].Path
	})

	manifest := Manifest{
		OKFVersion: OKFVersion,
		Profile:    normalized.Profile,
		ExportedAt: normalized.Now.UTC().Format(time.RFC3339),
	}
	pathByConceptID := map[string]string{}
	conceptIDBySourceKey := map[string]string{}
	sourceConceptByID := map[int64]string{}
	itemConceptByID := map[int64]string{}
	timestampBySourceKey := map[string]string{}

	for _, item := range items {
		if err := addManifestConcept(&manifest, ManifestConcept{
			Path:        item.Path,
			Type:        "Item",
			Title:       itemTitle(item.Item),
			Description: itemDescription(item.Item),
			ConceptID:   item.ConceptID,
			Kind:        "item",
			SourceKey:   item.Item.SourceKey,
			SourceType:  item.Item.SourceType,
		}, pathByConceptID); err != nil {
			return Bundle{}, err
		}
		itemConceptByID[item.Item.ID] = item.ConceptID
		conceptIDBySourceKey[item.Item.SourceKey] = item.ConceptID
		timestampBySourceKey[item.Item.SourceKey] = timestampForItem(item.Item)
	}
	for _, source := range sources {
		if err := addManifestConcept(&manifest, ManifestConcept{
			Path:        source.Path,
			Type:        "Source",
			Title:       sourceTitle(source.Source),
			Description: sourceDescription(source.Source),
			ConceptID:   source.ConceptID,
			Kind:        "source",
			SourceKey:   source.Source.SourceKey,
			SourceType:  source.Source.SourceType,
		}, pathByConceptID); err != nil {
			return Bundle{}, err
		}
		sourceConceptByID[source.Source.ID] = source.ConceptID
		conceptIDBySourceKey[source.Source.SourceKey] = source.ConceptID
		timestampBySourceKey[source.Source.SourceKey] = timestampForSource(source.Source)
	}
	for _, entity := range entities {
		if err := addManifestConcept(&manifest, ManifestConcept{
			Path:        entity.Path,
			Type:        "Entity",
			Title:       entityTitle(entity.Entity),
			Description: entityDescription(entity.Entity),
			ConceptID:   entity.ConceptID,
			Kind:        "entity",
			SourceKey:   entity.Entity.Key,
			SourceType:  string(entity.Entity.Kind),
		}, pathByConceptID); err != nil {
			return Bundle{}, err
		}
	}
	for _, topic := range topicDocs {
		if err := addManifestConcept(&manifest, ManifestConcept{
			Path:        topic.Path,
			Type:        "Topic",
			Title:       topicTitle(topic.Topic),
			Description: topicDescription(topic.Topic),
			ConceptID:   topic.ConceptID,
			Kind:        "topic",
			SourceKey:   topic.Topic.Topic,
			SourceType:  "topic",
		}, pathByConceptID); err != nil {
			return Bundle{}, err
		}
	}

	docs := make([]Document, 0, len(items)+len(sources)+len(entities)+len(topicDocs)+1)
	for _, item := range items {
		doc, omitted, err := renderItemDocument(item, snapshot, normalized, pathByConceptID, sourceConceptByID, itemConceptByID)
		if err != nil {
			return Bundle{}, err
		}
		manifest.OmittedLinks = append(manifest.OmittedLinks, omitted...)
		docs = append(docs, doc)
	}
	for _, source := range sources {
		doc, omitted, err := renderSourceDocument(source, snapshot, normalized, pathByConceptID, itemConceptByID)
		if err != nil {
			return Bundle{}, err
		}
		manifest.OmittedLinks = append(manifest.OmittedLinks, omitted...)
		docs = append(docs, doc)
	}
	for _, entity := range entities {
		doc, omitted, err := renderEntityDocument(entity, normalized, pathByConceptID, conceptIDBySourceKey, timestampBySourceKey)
		if err != nil {
			return Bundle{}, err
		}
		manifest.OmittedLinks = append(manifest.OmittedLinks, omitted...)
		docs = append(docs, doc)
	}
	for _, topic := range topicDocs {
		doc, omitted, err := renderTopicDocument(topic, normalized, pathByConceptID, conceptIDBySourceKey, timestampBySourceKey)
		if err != nil {
			return Bundle{}, err
		}
		manifest.OmittedLinks = append(manifest.OmittedLinks, omitted...)
		docs = append(docs, doc)
	}

	bundleDoc := bundleMetadataDocument(normalized)
	docs = append(docs, bundleDoc)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	docs = append(docs, indexDocuments(docs)...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })

	stats := ExportResult{
		Profile:              normalized.Profile,
		ItemsWritten:         len(items),
		SourcesWritten:       len(sources),
		EntitiesWritten:      len(entities),
		TopicsWritten:        len(topicDocs),
		ConceptsWritten:      len(items) + len(sources) + len(entities) + len(topicDocs) + 1,
		OmittedByFilterLinks: len(manifest.OmittedLinks),
	}
	for _, doc := range docs {
		if strings.HasSuffix(doc.Path, "index.md") {
			stats.IndexesWritten++
		}
	}
	return Bundle{Documents: docs, Manifest: manifest, Stats: stats}, nil
}

func normalizeExportOptions(opts ExportOptions) ExportOptions {
	if strings.TrimSpace(opts.Profile) == "" {
		opts.Profile = ProfilePrivate
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if !opts.IncludeItems && !opts.IncludeSources && !opts.IncludeEntities && !opts.IncludeTopics {
		opts.IncludeItems = true
		opts.IncludeSources = true
	}
	return opts
}

func addManifestConcept(manifest *Manifest, concept ManifestConcept, pathByConceptID map[string]string) error {
	if err := ValidateConceptPath(concept.Path); err != nil {
		return fmt.Errorf("invalid concept path for %s: %w", concept.ConceptID, err)
	}
	key := manifestCollisionKey(concept.Path)
	for _, existing := range manifest.Concepts {
		if manifestCollisionKey(existing.Path) == key {
			return fmt.Errorf("duplicate okf output path %s for %s and %s", concept.Path, existing.ConceptID, concept.ConceptID)
		}
	}
	manifest.Concepts = append(manifest.Concepts, concept)
	pathByConceptID[concept.ConceptID] = concept.Path
	return nil
}

func bundleMetadataDocument(opts ExportOptions) Document {
	version := strings.TrimSpace(opts.DbrainVersion)
	if version == "" {
		version = "unknown"
	}
	return Document{
		Path:        "bundle.md",
		Type:        "Bundle Metadata",
		Title:       "dbrain OKF Bundle",
		Description: "Metadata for a generated dbrain OKF export.",
		Generated:   generatedMetadata(opts, opts.Now.UTC().Format(time.RFC3339)),
		Fields: []Field{
			{Name: "okf_version", Value: OKFVersion},
			{Name: "okf_profile", Value: opts.Profile},
			{Name: "exported_at", Value: opts.Now.UTC().Format(time.RFC3339)},
			{Name: "dbrain_version", Value: version},
		},
		Body: "# Bundle\n\nGenerated dbrain Open Knowledge Format bundle.\n",
	}
}

func generatedMetadata(opts ExportOptions, at string) Generated {
	version := strings.TrimSpace(opts.DbrainVersion)
	if version == "" {
		version = "unknown"
	}
	return Generated{By: "dbrain/" + version, At: strings.TrimSpace(at)}
}

func itemTitle(item model.Item) string {
	return firstNonEmpty(item.Title, item.ArticleTitle, item.CanonicalURL, item.SourceKey)
}

func sourceTitle(source model.SourceDocument) string {
	return firstNonEmpty(source.Title, source.CanonicalURL, source.SourceKey)
}

func itemDescription(item model.Item) string {
	if sentence := firstSentence(item.SummaryText); sentence != "" {
		return sentence
	}
	if sentence := firstSentence(item.Text); sentence != "" {
		return sentence
	}
	switch strings.TrimSpace(item.SourceType) {
	case "x_bookmark", "x":
		return "Saved X item from " + firstNonEmpty(item.AuthorHandle, item.AuthorName, item.Title, "unknown author") + "."
	case "apple_note", "apple-notes":
		return "Imported Apple Note titled \"" + itemTitle(item) + "\"."
	case "safari_tab", "safari-tabs":
		return "Imported Safari tab for " + firstNonEmpty(item.PrimaryDomain, item.Title, "unknown host") + "."
	case "github":
		return "Imported GitHub signal for " + itemTitle(item) + "."
	case "youtube":
		return "Imported YouTube signal for " + itemTitle(item) + "."
	default:
		return "Imported item from " + firstNonEmpty(item.SourceType, item.PrimaryDomain, "local dbrain") + "."
	}
}

func sourceDescription(source model.SourceDocument) string {
	if sentence := firstSentence(source.Description); sentence != "" {
		return sentence
	}
	if sentence := firstSentence(source.SummaryText); sentence != "" {
		return sentence
	}
	return "Linked source from " + firstNonEmpty(source.Domain, source.SourceType, "local dbrain") + "."
}
