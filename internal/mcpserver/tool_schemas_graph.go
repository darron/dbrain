package mcpserver

func topicMapOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"topic":         scalarSchema("string", "Topic that was mapped."),
		"seed_limit":    scalarSchema("integer", "Maximum number of primary seed nodes."),
		"related_limit": scalarSchema("integer", "Maximum number of related nodes expanded per seed."),
		"synthesis":     topicSynthesisSchema(),
		"entities":      arraySchema(topicMapEntitySchema()),
		"pivots":        topicPivotsSchema(),
		"nodes":         arraySchema(topicMapNodeSchema()),
		"edges":         arraySchema(topicMapEdgeSchema()),
	}, "topic", "seed_limit", "related_limit", "nodes", "edges")
}

func topicBriefOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"topic":         scalarSchema("string", "Topic that was mapped."),
		"source_types":  arraySchema(scalarSchema("string", "Optional source type filters applied to the topic build.")),
		"seed_limit":    scalarSchema("integer", "Maximum number of primary seed nodes."),
		"related_limit": scalarSchema("integer", "Maximum number of related nodes expanded per seed."),
		"summary":       scalarSchema("string", "Compact natural-language summary of the topic graph."),
		"synthesis":     topicSynthesisSchema(),
		"pivots":        topicPivotsSchema(),
		"entities":      arraySchema(topicMapEntitySchema()),
		"nodes":         arraySchema(topicMapNodeSchema()),
		"edges":         arraySchema(topicMapEdgeSchema()),
		"markdown":      scalarSchema("string", "Rendered markdown topic note preview."),
	}, "topic", "seed_limit", "related_limit", "summary", "pivots", "nodes", "edges", "markdown")
}

func entityMapOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"count":    scalarSchema("integer", "Number of entities returned."),
		"entities": arraySchema(entitySchema()),
	}, "count", "entities")
}

func relatedOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"kind":            enumSchema("Whether the lookup resolved to an item or a source.", "item", "source"),
		"lookup":          scalarSchema("string", "Lookup value used."),
		"item":            genericObjectSchema("Slim item metadata when lookup resolved to an item."),
		"source":          genericObjectSchema("Slim source metadata when lookup resolved to a source."),
		"count":           scalarSchema("integer", "Number of related rows returned."),
		"related_items":   arraySchema(genericObjectSchema("Slim child item metadata, for example quoted posts.")),
		"related_sources": arraySchema(itemSourceRefSchema()),
		"backlinks":       arraySchema(sourceBacklinkSchema()),
	}, "kind", "count")
}

func entitySchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":             scalarSchema("string", "Stable derived entity key."),
		"name":            scalarSchema("string", "Display name for the entity."),
		"kind":            scalarSchema("string", "Entity kind such as person, org, project, or site."),
		"aliases":         arraySchema(scalarSchema("string", "Entity alias.")),
		"canonical_url":   scalarSchema("string", "Canonical URL when available."),
		"domain":          scalarSchema("string", "Domain when available."),
		"note_path":       scalarSchema("string", "Relative rendered entity note path."),
		"source_types":    arraySchema(scalarSchema("string", "Underlying source types contributing to the entity.")),
		"reference_count": scalarSchema("integer", "Number of distinct notes referencing the entity."),
		"references":      arraySchema(entityReferenceSchema()),
		"links":           arraySchema(entityLinkSchema()),
	}, "key", "name", "kind", "note_path", "reference_count")
}

func entityReferenceSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"ref_kind":     scalarSchema("string", "Whether the reference came from an item or source note."),
		"source_key":   scalarSchema("string", "Stable source key for the referencing note."),
		"title":        scalarSchema("string", "Best available title."),
		"note_path":    scalarSchema("string", "Relative rendered note path."),
		"url":          scalarSchema("string", "Canonical URL."),
		"source_type":  scalarSchema("string", "Underlying source type."),
		"relationship": scalarSchema("string", "How the note relates to the entity."),
	}, "ref_kind", "source_key", "title", "note_path", "relationship")
}

func entityLinkSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":          scalarSchema("string", "Stable key for the linked entity."),
		"name":         scalarSchema("string", "Display name for the linked entity."),
		"kind":         scalarSchema("string", "Kind for the linked entity."),
		"note_path":    scalarSchema("string", "Relative note path for the linked entity."),
		"relationship": scalarSchema("string", "How the linked entity relates to the current entity."),
	}, "key", "name", "kind", "note_path", "relationship")
}

func topicMapNodeSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key":  scalarSchema("string", "Stable node key."),
		"kind":        scalarSchema("string", "Whether the node is an item or source."),
		"title":       scalarSchema("string", "Best available title."),
		"url":         scalarSchema("string", "Canonical URL."),
		"note_path":   scalarSchema("string", "Relative rendered note path."),
		"source_type": scalarSchema("string", "Underlying source type when known."),
		"role":        scalarSchema("string", "Whether the node is a seed or related node."),
	}, "source_key", "kind", "title", "url", "note_path", "role")
}

func topicMapEntitySchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":                 scalarSchema("string", "Stable derived entity key."),
		"name":                scalarSchema("string", "Display name for the entity."),
		"kind":                scalarSchema("string", "Entity kind."),
		"note_path":           scalarSchema("string", "Relative rendered entity note path."),
		"canonical_url":       scalarSchema("string", "Canonical URL when available."),
		"reference_count":     scalarSchema("integer", "Total references to this entity across the brain."),
		"matched_references":  scalarSchema("integer", "Number of mapped nodes that reference the entity."),
		"matched_source_keys": arraySchema(scalarSchema("string", "Mapped node source keys that reference the entity.")),
	}, "key", "name", "kind", "note_path", "reference_count", "matched_references")
}

func topicPivotsSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"projects":      arraySchema(topicMapEntitySchema()),
		"orgs":          arraySchema(topicMapEntitySchema()),
		"sites":         arraySchema(topicMapEntitySchema()),
		"people":        arraySchema(topicMapEntitySchema()),
		"seed_nodes":    arraySchema(topicMapNodeSchema()),
		"related_nodes": arraySchema(topicMapNodeSchema()),
	})
}

func topicSynthesisSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"overview":       scalarSchema("string", "High-level synthesized explanation of the topic as it appears in the local corpus."),
		"angles":         arraySchema(scalarSchema("string", "Distinct angles or surfaces that show up repeatedly in the mapped corpus.")),
		"signals":        arraySchema(topicSignalSchema()),
		"open_questions": arraySchema(scalarSchema("string", "Question or tension worth revisiting in this topic.")),
		"why_it_matters": scalarSchema("string", "Why this topic looks worth keeping and revisiting."),
	})
}

func topicSignalSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"title":       scalarSchema("string", "Short label for the repeated signal."),
		"detail":      scalarSchema("string", "Evidence-backed detail for the signal."),
		"source_keys": arraySchema(scalarSchema("string", "Notes that support this signal.")),
	}, "title", "detail")
}

func topicMapEdgeSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"from":         scalarSchema("string", "Source key of the origin node."),
		"to":           scalarSchema("string", "Source key of the target node."),
		"relationship": scalarSchema("string", "Relationship label between the nodes."),
	}, "from", "to", "relationship")
}
