package mcpserver

import "strings"

func toolDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "dbrain_search",
			"description": "Search the local brain across items and linked sources.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":        map[string]interface{}{"type": "string", "description": "Search query."},
					"limit":        map[string]interface{}{"type": "integer", "description": "Maximum number of results.", "default": 10},
					"source_types": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters like github, web, x_bookmark, apple_note."},
				},
				"required": []string{"query"},
			},
			"outputSchema": searchOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_get",
			"description": "Load a specific item or source from the local brain by source key, URL, external id, or note path. Defaults to capped DB-backed evidence sections; rendered Markdown is available with content_mode=rendered.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookup":                map[string]interface{}{"type": "string", "description": "Source key, external id, URL, or note path."},
					"query":                 map[string]interface{}{"type": "string", "description": "Optional query used to window evidence sections around matching text. Applies to content_mode=evidence."},
					"content_mode":          map[string]interface{}{"type": "string", "enum": []string{"brief", "evidence", "raw", "rendered"}, "description": "Content to return: brief metadata only, capped DB evidence sections, capped raw DB sections, or rendered Markdown note.", "default": "evidence"},
					"max_chars_per_section": map[string]interface{}{"type": "integer", "description": "Maximum characters per returned content section. Hard-capped to prevent accidental huge context.", "default": defaultGetSectionChars},
					"include_content":       map[string]interface{}{"type": "boolean", "description": "Deprecated compatibility flag. false maps to content_mode=brief; true maps to content_mode=evidence unless content_mode is set.", "default": true},
				},
				"required": []string{"lookup"},
			},
			"outputSchema": getOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_get_many",
			"description": "Batch-load several specific items or sources from the local brain. Use after search/research_pack when inspecting multiple evidence rows; returns per-lookup payloads and partial errors without failing the whole batch.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookups":               map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Source keys, external ids, URLs, or note paths. Maximum 20."},
					"query":                 map[string]interface{}{"type": "string", "description": "Optional query used to window evidence sections around matching text. Applies to content_mode=evidence."},
					"content_mode":          map[string]interface{}{"type": "string", "enum": []string{"brief", "evidence", "raw", "rendered"}, "description": "Content to return for each lookup.", "default": "evidence"},
					"max_chars_per_section": map[string]interface{}{"type": "integer", "description": "Maximum characters per returned content section. Hard-capped to prevent accidental huge context.", "default": defaultGetSectionChars},
					"include_content":       map[string]interface{}{"type": "boolean", "description": "Deprecated compatibility flag. false maps to content_mode=brief; true maps to content_mode=evidence unless content_mode is set.", "default": true},
				},
				"required": []string{"lookups"},
			},
			"outputSchema": getManyOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_research_pack",
			"description": "Build a compact read-only research pack for a question. Expands text queries with bounded model-assisted query planning when configured, hyphenated tag aliases, entity matches, optional graph links, and an optional topic brief so agents can answer broad corpus questions with one call.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question":            map[string]interface{}{"type": "string", "description": "Question to investigate from the local brain."},
					"topic":               map[string]interface{}{"type": "string", "description": "Optional explicit topic for the topic brief. If omitted, broad questions infer one."},
					"limit":               map[string]interface{}{"type": "integer", "description": "Maximum evidence documents.", "default": 8},
					"source_types":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters."},
					"include_related":     map[string]interface{}{"type": "boolean", "description": "Whether to append linked related evidence.", "default": false},
					"related_limit":       map[string]interface{}{"type": "integer", "description": "Maximum related evidence documents.", "default": 2},
					"seed_limit":          map[string]interface{}{"type": "integer", "description": "Maximum primary topic nodes when a topic brief is included.", "default": 6},
					"include_topic_brief": map[string]interface{}{"type": "boolean", "description": "Force topic brief on or off. Defaults to on only when a broad topic can be inferred."},
					"max_chars_per_doc":   map[string]interface{}{"type": "integer", "description": "Maximum summary/excerpt characters per evidence document.", "default": 700},
					"planner_model":       map[string]interface{}{"type": "string", "description": "Optional model for query planning; empty uses the configured summary model."},
					"use_model_planner":   map[string]interface{}{"type": "boolean", "description": "Use the configured model for bounded query planning before retrieval. Defaults to true unless disable_planner is set.", "default": true},
					"disable_planner":     map[string]interface{}{"type": "boolean", "description": "Disable model-assisted query planning and use deterministic planning only.", "default": false},
				},
				"required": []string{"question"},
			},
			"outputSchema": researchPackOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_entity_map",
			"description": "Search derived entities built from stable local metadata such as X authors, GitHub repos, GitHub owners, and site domains.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "Entity search query. Can be empty to list the top derived entities."},
					"kind":  map[string]interface{}{"type": "string", "description": "Optional kind filter: person, org, project, or site."},
					"limit": map[string]interface{}{"type": "integer", "description": "Maximum number of entities.", "default": 20},
				},
			},
			"outputSchema": entityMapOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_topic_map",
			"description": "Build a compact topic map from the local brain by combining search seeds with item/source graph expansion.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":         map[string]interface{}{"type": "string", "description": "Concept, keyword, or theme to map."},
					"source_types":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters."},
					"seed_limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of primary seed nodes.", "default": 6},
					"related_limit": map[string]interface{}{"type": "integer", "description": "Maximum related nodes to expand from each seed.", "default": 2},
				},
				"required": []string{"topic"},
			},
			"outputSchema": topicMapOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_topic_brief",
			"description": "Build a richer topic brief from the local brain, including grouped entity pivots and a rendered markdown preview.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":         map[string]interface{}{"type": "string", "description": "Concept, keyword, or theme to map."},
					"source_types":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters."},
					"seed_limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of primary seed nodes.", "default": 6},
					"related_limit": map[string]interface{}{"type": "integer", "description": "Maximum related nodes to expand from each seed.", "default": 2},
				},
				"required": []string{"topic"},
			},
			"outputSchema": topicBriefOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_related",
			"description": "Traverse the local item/source graph. For an item lookup, return linked sources. For a source lookup, return item backlinks.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookup": map[string]interface{}{"type": "string", "description": "Source key, external id, URL, or note path for an item or source."},
				},
				"required": []string{"lookup"},
			},
			"outputSchema": relatedOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_items",
			"description": "Read item counts from the local brain, optionally filtered by source type and grouped by source type or none.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source_type": map[string]interface{}{"type": "string", "description": "Optional item source type filter."},
					"group_by":    map[string]interface{}{"type": "string", "description": "Grouping: source-type or none.", "default": "source-type"},
				},
			},
			"outputSchema": countOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_sources",
			"description": "Read source counts from the local brain, optionally filtered by source type, extract tool, extract status, or summary status.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source_type":    map[string]interface{}{"type": "string", "description": "Optional source type filter."},
					"extract_tool":   map[string]interface{}{"type": "string", "description": "Optional extract tool filter."},
					"summary_status": map[string]interface{}{"type": "string", "description": "Optional summary status filter."},
					"extract_status": map[string]interface{}{"type": "string", "description": "Optional extract status filter."},
					"group_by":       map[string]interface{}{"type": "string", "description": "Grouping: source-type, summary-status, extract-status, or none.", "default": "source-type"},
				},
			},
			"outputSchema": countOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_activity",
			"description": "Read recent activity timestamps and counts for the local brain pipeline.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"window_seconds": map[string]interface{}{"type": "integer", "description": "Lookback window in seconds.", "default": 900},
				},
			},
			"outputSchema": activityOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_backlog",
			"description": "Read the remaining queued work in the local brain pipeline.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			"outputSchema": backlogOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
	}
}

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
		"note":                  scalarSchema("string", "Absolute path to the rendered note."),
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

func researchPackOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"schema_version":     scalarSchema("string", "Version of the research pack schema."),
		"question":           scalarSchema("string", "Original research question."),
		"mode":               scalarSchema("string", "Whether this pack contains evidence only or a topic brief plus evidence."),
		"query_plan":         researchQueryPlanSchema(),
		"coverage":           researchCoverageSchema(),
		"topic":              scalarSchema("string", "Inferred topic phrase when a topic brief was attached."),
		"used_topic_brief":   scalarSchema("boolean", "Whether a topic brief was inferred and attached."),
		"evidence":           arraySchema(evidenceSchema()),
		"exact_tag_evidence": arraySchema(evidenceSchema()),
		"topic_brief":        topicBriefOutputSchema(),
		"next_steps":         arraySchema(researchNextStepSchema()),
	}, "schema_version", "question", "mode", "query_plan", "coverage", "used_topic_brief", "evidence")
}

func researchQueryPlanSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"text_query":          scalarSchema("string", "Text query submitted to corpus search after stopword removal."),
		"query_terms":         arraySchema(scalarSchema("string", "Normalized query term.")),
		"tag_queries":         arraySchema(scalarSchema("string", "Hyphenated user_tag aliases searched in addition to text search.")),
		"query_variants":      arraySchema(researchQueryVariantSchema()),
		"concepts":            arraySchema(researchQueryConceptSchema()),
		"planner":             scalarSchema("string", "Planner path used for retrieval, such as deterministic or model_assisted."),
		"planner_model":       scalarSchema("string", "Model used for query planning when model-assisted planning ran."),
		"planner_error":       scalarSchema("string", "Non-fatal planner error when deterministic fallback was used."),
		"source_types":        arraySchema(scalarSchema("string", "Optional source type filters.")),
		"limit":               scalarSchema("integer", "Maximum evidence documents requested."),
		"max_chars_per_doc":   scalarSchema("integer", "Maximum summary/excerpt characters per evidence document."),
		"include_related":     scalarSchema("boolean", "Whether graph-related evidence was requested."),
		"related_limit":       scalarSchema("integer", "Maximum related evidence documents."),
		"topic":               scalarSchema("string", "Topic used for the topic brief when present."),
		"topic_source":        scalarSchema("string", "How the topic was selected: explicit, inferred, or normalized_question."),
		"include_topic_brief": scalarSchema("boolean", "Whether a topic brief was requested for this pack."),
	}, "text_query", "query_terms", "tag_queries", "limit", "max_chars_per_doc", "include_related", "include_topic_brief")
}

func researchQueryVariantSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"query":  scalarSchema("string", "Bounded keyword query variant used for retrieval."),
		"reason": scalarSchema("string", "Why this query variant was added."),
	}, "query")
}

func researchQueryConceptSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":       scalarSchema("string", "Canonical concept key used for evidence scoring."),
		"preferred": scalarSchema("string", "Preferred search term for this concept."),
		"terms":     arraySchema(scalarSchema("string", "Alias or alternate phrase that can satisfy the concept.")),
		"required":  scalarSchema("boolean", "Whether missing this concept should penalize evidence."),
	}, "key", "terms", "required")
}

func researchCoverageSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"evidence_count":      scalarSchema("integer", "Number of evidence rows returned."),
		"by_kind":             arraySchema(researchBucketSchema()),
		"by_source_type":      arraySchema(researchBucketSchema()),
		"top_user_tags":       arraySchema(researchBucketSchema()),
		"exact_tag_matches":   arraySchema(researchBucketSchema()),
		"item_text_matches":   scalarSchema("integer", "Total item rows matching the topic/text phrase."),
		"source_text_matches": scalarSchema("integer", "Total source rows matching the topic/text phrase."),
		"topic_node_count":    scalarSchema("integer", "Number of nodes included in the attached topic brief."),
		"topic_edge_count":    scalarSchema("integer", "Number of edges included in the attached topic brief."),
		"displayed_limit":     scalarSchema("integer", "Requested evidence limit for this pack."),
		"related_limit":       scalarSchema("integer", "Requested related-evidence limit for this pack."),
		"recall_note":         scalarSchema("string", "Human-readable reminder that returned evidence is capped and how much matching corpus exists."),
	}, "evidence_count", "by_kind", "by_source_type")
}

func researchBucketSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":   scalarSchema("string", "Bucket key."),
		"count": scalarSchema("integer", "Bucket count."),
	}, "key", "count")
}

func researchNextStepSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"action": scalarSchema("string", "Semantic action identifier, such as inspect_top_evidence or expand_related."),
		"label":  scalarSchema("string", "Human-readable action label."),
		"reason": scalarSchema("string", "Why this follow-up helps."),
		"params": genericObjectSchema("Suggested action parameters."),
	}, "action", "label")
}

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

func countOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"group_by": scalarSchema("string", "Grouping applied to the count buckets."),
		"total":    scalarSchema("integer", "Total count across buckets."),
		"buckets":  arraySchema(countBucketSchema()),
	}, "group_by", "total", "buckets")
}

func activityOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"now":                          scalarSchema("string", "Current UTC time in RFC3339 format."),
		"window":                       scalarSchema("string", "Lookback window duration string."),
		"latest_item_updated_at":       scalarSchema("string", "Latest item update timestamp."),
		"latest_source_updated_at":     scalarSchema("string", "Latest source update timestamp."),
		"latest_source_summary_at":     scalarSchema("string", "Latest source summary timestamp."),
		"items_updated_in_window":      scalarSchema("integer", "Number of items updated in the window."),
		"sources_updated_in_window":    scalarSchema("integer", "Number of sources updated in the window."),
		"sources_summarized_in_window": scalarSchema("integer", "Number of sources summarized in the window."),
	}, "now", "window", "items_updated_in_window", "sources_updated_in_window", "sources_summarized_in_window")
}

func backlogOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"x_hydration_pending":               scalarSchema("integer", "Pending X hydration items."),
		"link_discovery_pending":            scalarSchema("integer", "Pending link discovery items."),
		"source_extraction_pending":         scalarSchema("integer", "Pending source extraction rows."),
		"source_summary_pending":            scalarSchema("integer", "Pending source summary rows."),
		"source_extraction_pending_by_type": arraySchema(countBucketSchema()),
		"source_summary_pending_by_type":    arraySchema(countBucketSchema()),
		"drained":                           scalarSchema("boolean", "Whether all current queues are empty."),
	}, "x_hydration_pending", "link_discovery_pending", "source_extraction_pending", "source_summary_pending", "drained")
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
		"retrieval":      retrievalInfoSchema(),
	}, "source_key", "kind", "title", "url", "note_path", "summary", "excerpt")
}

func retrievalInfoSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"score":         scalarSchema("integer", "Final retrieval score used to rank this evidence row."),
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

func countBucketSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":   scalarSchema("string", "Bucket key."),
		"count": scalarSchema("integer", "Bucket count."),
	}, "key", "count")
}

func objectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func genericObjectSchema(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object"}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func arraySchema(items interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":  "array",
		"items": items,
	}
}

func scalarSchema(valueType string, description string) map[string]interface{} {
	schema := map[string]interface{}{
		"type": valueType,
	}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func enumSchema(description string, values ...string) map[string]interface{} {
	schema := scalarSchema("string", description)
	enumValues := make([]interface{}, 0, len(values))
	for _, value := range values {
		enumValues = append(enumValues, value)
	}
	schema["enum"] = enumValues
	return schema
}
