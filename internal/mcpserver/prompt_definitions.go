package mcpserver

func promptDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "brain_research",
			"description": "Research a question against the local brain using the MCP tools and cite the strongest evidence.",
			"arguments": []map[string]interface{}{
				{"name": "question", "description": "Question to investigate.", "required": true},
				{"name": "source_types", "description": "Optional comma-separated source type filters such as github, web, x_bookmark, or apple_note.", "required": false},
				{"name": "include_related", "description": "Whether to append linked related evidence (true or false).", "required": false},
			},
		},
		{
			"name":        "brain_browse",
			"description": "Browse outward from a known item or source by following the local item/source graph.",
			"arguments": []map[string]interface{}{
				{"name": "lookup", "description": "Source key, URL, or note path for the starting item or source.", "required": true},
			},
		},
		{
			"name":        "brain_entity_browse",
			"description": "Browse derived entities such as X authors, GitHub repos, GitHub owners, and site domains from the local brain.",
			"arguments": []map[string]interface{}{
				{"name": "query", "description": "Entity search query.", "required": true},
				{"name": "kind", "description": "Optional entity kind filter such as person, org, project, or site.", "required": false},
			},
		},
		{
			"name":        "brain_topic_map",
			"description": "Build a compact topic map for a concept by combining search hits, graph expansion, and note inspection.",
			"arguments": []map[string]interface{}{
				{"name": "topic", "description": "Concept, keyword, or theme to map.", "required": true},
				{"name": "source_types", "description": "Optional comma-separated source type filters such as github, web, x_bookmark, or apple_note.", "required": false},
				{"name": "max_nodes", "description": "Maximum number of primary nodes to include in the map.", "required": false},
			},
		},
		{
			"name":        "brain_topic_brief",
			"description": "Build a richer topic brief with grouped entity pivots, key notes, and a rendered markdown preview.",
			"arguments": []map[string]interface{}{
				{"name": "topic", "description": "Concept, keyword, or theme to summarize.", "required": true},
				{"name": "source_types", "description": "Optional comma-separated source type filters such as github, web, x_bookmark, or apple_note.", "required": false},
				{"name": "max_nodes", "description": "Maximum number of primary nodes to include in the brief.", "required": false},
			},
		},
		{
			"name":        "brain_status",
			"description": "Inspect the brain pipeline backlog and recent activity, then summarize whether work is active, stalled, or drained.",
			"arguments": []map[string]interface{}{
				{"name": "window_minutes", "description": "Activity lookback window in minutes.", "required": false},
			},
		},
	}
}
