package mcpserver

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
			"name":        "dbrain_okf_search",
			"description": "Search the generated Open Knowledge Format bundle at the configured OKF directory. Read-only; does not regenerate or validate the bundle.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "Search query. Empty lists top bundle concepts."},
					"limit": map[string]interface{}{"type": "integer", "description": "Maximum number of OKF concepts.", "default": 10},
				},
			},
			"outputSchema": okfSearchOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_okf_get",
			"description": "Read a concept from the generated Open Knowledge Format bundle by path, dbrain concept id, or source key. Read-only; does not regenerate or validate the bundle.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookup":           map[string]interface{}{"type": "string", "description": "OKF path, dbrain_concept_id, or dbrain_source_key."},
					"include_markdown": map[string]interface{}{"type": "boolean", "description": "Include rendered Markdown in structured output.", "default": false},
					"max_chars":        map[string]interface{}{"type": "integer", "description": "Maximum body/markdown characters returned. Hard-capped for MCP responses.", "default": 8000},
				},
				"required": []string{"lookup"},
			},
			"outputSchema": okfGetOutputSchema(),
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
					"use_semantic":        map[string]interface{}{"type": "boolean", "description": "Request optional semantic retrieval when a local lane is configured. Currently reports disabled until a validated local embedding lane is added.", "default": false},
					"disable_semantic":    map[string]interface{}{"type": "boolean", "description": "Disable optional semantic retrieval for deterministic lexical debugging.", "default": false},
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
