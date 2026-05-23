package mcpserver

func searchOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"count":             scalarSchema("integer", "Number of search hits returned."),
		"results":           arraySchema(searchResultSchema()),
		"tag_aliases":       arraySchema(scalarSchema("string", "Hyphenated tag aliases checked for exact user_tags matches.")),
		"exact_tag_matches": arraySchema(researchBucketSchema()),
	}, "count", "results")
}

func getOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"kind":                  enumSchema("item or source", "item", "source"),
		"title":                 scalarSchema("string", "Resolved title."),
		"source_key":            scalarSchema("string", "Resolved source key."),
		"url":                   scalarSchema("string", "Canonical URL when available."),
		"note":                  scalarSchema("string", "Relative rendered note path."),
		"note_path":             scalarSchema("string", "Relative rendered note path."),
		"query":                 scalarSchema("string", "Optional query used to window evidence sections around matches."),
		"content_mode":          enumSchema("Returned content mode.", "brief", "evidence", "raw", "rendered"),
		"max_chars_per_section": scalarSchema("integer", "Maximum characters returned per content section."),
		"available_sections":    arraySchema(getSectionSchema()),
		"content_sections":      arraySchema(getSectionSchema()),
		"content":               scalarSchema("string", "Rendered markdown note content when content_mode is rendered."),
		"item":                  genericObjectSchema("Slim item metadata when the lookup resolved to an item."),
		"source":                genericObjectSchema("Slim source metadata when the lookup resolved to a source."),
		"related_items":         arraySchema(genericObjectSchema("Slim child item metadata, for example quoted posts.")),
		"related_sources":       arraySchema(itemSourceRefSchema()),
		"backlinks":             arraySchema(sourceBacklinkSchema()),
		"media":                 arraySchema(mediaRefSchema()),
	}, "kind", "title", "source_key", "note", "content_mode", "available_sections", "content_sections")
}

func getManyOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"lookups":               arraySchema(scalarSchema("string", "Lookup values requested.")),
		"query":                 scalarSchema("string", "Optional query used to window evidence sections around matches."),
		"content_mode":          enumSchema("Returned content mode.", "brief", "evidence", "raw", "rendered"),
		"max_chars_per_section": scalarSchema("integer", "Maximum characters returned per content section."),
		"count":                 scalarSchema("integer", "Number of lookups successfully resolved."),
		"results":               arraySchema(getOutputSchema()),
		"errors":                arraySchema(getManyErrorSchema()),
	}, "lookups", "content_mode", "max_chars_per_section", "count", "results", "errors")
}

func getManyErrorSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"lookup": scalarSchema("string", "Lookup value that failed."),
		"error":  scalarSchema("string", "Error returned while resolving the lookup."),
	}, "lookup", "error")
}

func getSectionSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"name":      scalarSchema("string", "Section name, for example summary_text, extracted_text, x_post_text, or rendered_note."),
		"role":      scalarSchema("string", "Section role such as raw, derived, metadata, raw_json, or rendered."),
		"status":    scalarSchema("string", "Pipeline status for this section when applicable."),
		"model":     scalarSchema("string", "Model provenance for derived sections when applicable."),
		"tool":      scalarSchema("string", "Tool provenance when applicable."),
		"at":        scalarSchema("string", "Timestamp for the section when applicable."),
		"chars":     scalarSchema("integer", "Original section character count before truncation."),
		"text":      scalarSchema("string", "Section text when returned by the selected content_mode."),
		"truncated": scalarSchema("boolean", "Whether text was truncated to max_chars_per_section."),
	}, "name", "role", "chars", "truncated")
}

func searchResultSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key":     scalarSchema("string", "Stable key for the item or source."),
		"source_type":    scalarSchema("string", "Underlying source type."),
		"external_id":    scalarSchema("string", "External source id when present."),
		"title":          scalarSchema("string", "Best available title."),
		"author_handle":  scalarSchema("string", "Author handle when present."),
		"author_name":    scalarSchema("string", "Author display name when present."),
		"canonical_url":  scalarSchema("string", "Canonical URL."),
		"primary_domain": scalarSchema("string", "Primary domain for item rows or source domain for source rows."),
		"note_path":      scalarSchema("string", "Relative rendered note path."),
		"user_tags":      scalarSchema("string", "Comma-separated user tags for item or source rows."),
		"snippet":        scalarSchema("string", "Search snippet."),
		"media":          arraySchema(mediaRefSchema()),
	}, "source_key", "title", "canonical_url", "note_path")
}

func evidenceSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key":     scalarSchema("string", "Stable key for the evidence row."),
		"kind":           scalarSchema("string", "Evidence kind, such as item or source."),
		"title":          scalarSchema("string", "Best available title."),
		"url":            scalarSchema("string", "Canonical URL."),
		"note_path":      scalarSchema("string", "Rendered note path."),
		"summary":        scalarSchema("string", "Summary text if available."),
		"excerpt":        scalarSchema("string", "Excerpt used for retrieval."),
		"author":         scalarSchema("string", "Author when present."),
		"source_type":    scalarSchema("string", "Underlying source type."),
		"published_at":   scalarSchema("string", "Published timestamp when present."),
		"extracted_at":   scalarSchema("string", "Extraction timestamp when present."),
		"summarized_at":  scalarSchema("string", "Summary timestamp when present."),
		"user_tags":      scalarSchema("string", "Comma-separated user tags for item or source evidence."),
		"entity_matches": arraySchema(scalarSchema("string", "Derived entities that matched the query and reference this note.")),
		"related_to":     scalarSchema("string", "Parent source key when added as related evidence."),
		"relationship":   scalarSchema("string", "How this evidence relates to another node."),
		"media":          arraySchema(mediaRefSchema()),
		"retrieval":      retrievalInfoSchema(),
	}, "source_key", "kind", "title", "url", "note_path", "summary", "excerpt")
}

func mediaRefSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"media_asset_id":  scalarSchema("integer", "Stable local media asset id for the archived/proxied asset."),
		"ordinal":         scalarSchema("integer", "Media position on the owning item."),
		"expanded_url":    scalarSchema("string", "Provider page URL for this media object when available."),
		"remote_url":      scalarSchema("string", "Original provider media URL when available."),
		"media_type":      scalarSchema("string", "Media type such as photo, video, animated_gif, or audio."),
		"download_status": scalarSchema("string", "Local media download state."),
		"archive_url":     scalarSchema("string", "Archive URL when the media has been archived."),
		"archive_status":  scalarSchema("string", "Archive state."),
		"width":           scalarSchema("integer", "Media width in pixels when known."),
		"height":          scalarSchema("integer", "Media height in pixels when known."),
	}, "media_asset_id", "media_type")
}

func retrievalInfoSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"score":         scalarSchema("integer", "Final retrieval score used to rank this evidence row."),
		"lanes":         arraySchema(retrievalLaneSchema()),
		"signals":       arraySchema(retrievalSignalSchema()),
		"matched_terms": arraySchema(scalarSchema("string", "Query terms found in title, tags, summary, excerpt, URL, or author fields.")),
		"missing_terms": arraySchema(scalarSchema("string", "Query terms not found in the returned evidence fields.")),
	}, "score")
}

func retrievalSignalSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"name":   scalarSchema("string", "Name of the ranking signal, for example query_term_title or exact_phrase_user_tags."),
		"detail": scalarSchema("string", "Matched term, phrase, entity, or relationship detail when available."),
		"weight": scalarSchema("integer", "Score contribution from this signal."),
	}, "name", "weight")
}

func itemSourceRefSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_id":      scalarSchema("integer", "Internal source id."),
		"source_key":     scalarSchema("string", "Stable source key."),
		"canonical_url":  scalarSchema("string", "Canonical URL."),
		"source_type":    scalarSchema("string", "Source type."),
		"title":          scalarSchema("string", "Best available title."),
		"note_path":      scalarSchema("string", "Relative rendered note path."),
		"extract_status": scalarSchema("string", "Extraction status."),
		"summary_status": scalarSchema("string", "Summary status."),
		"user_tags":      scalarSchema("string", "Comma-separated source user tags."),
	}, "source_id", "source_key", "canonical_url", "source_type", "title", "note_path")
}

func sourceBacklinkSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"item_id":       scalarSchema("integer", "Internal item id."),
		"source_key":    scalarSchema("string", "Stable item source key."),
		"source_type":   scalarSchema("string", "Underlying item source type."),
		"canonical_url": scalarSchema("string", "Canonical item URL."),
		"title":         scalarSchema("string", "Best available title."),
		"note_path":     scalarSchema("string", "Relative rendered note path."),
		"author_handle": scalarSchema("string", "Author handle when present."),
		"author_name":   scalarSchema("string", "Author display name when present."),
		"published_at":  scalarSchema("string", "Published timestamp when present."),
		"user_tags":     scalarSchema("string", "Comma-separated user tags from the saved item that references the source."),
	}, "item_id", "source_key", "canonical_url", "title", "note_path")
}
