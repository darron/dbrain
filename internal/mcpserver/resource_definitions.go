package mcpserver

func resourceDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"uri":         "dbrain://mcp/overview",
			"name":        "MCP Overview",
			"description": "Overview of the dbrain MCP server surface, including tools, resources, prompts, and suggested workflows.",
			"mimeType":    "text/markdown",
		},
		{
			"uri":         "dbrain://stats/activity",
			"name":        "Brain Activity",
			"description": "Recent pipeline activity timestamps and write counts for the local brain.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "dbrain://stats/backlog",
			"name":        "Brain Backlog",
			"description": "Remaining queued work in the local brain pipeline.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "dbrain://stats/items",
			"name":        "Brain Item Counts",
			"description": "Item counts for the local brain. Supports query params source_type and group_by.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "dbrain://stats/sources",
			"name":        "Brain Source Counts",
			"description": "Source counts for the local brain. Supports query params source_type, extract_tool, summary_status, extract_status, and group_by.",
			"mimeType":    "application/json",
		},
	}
}

func resourceTemplateDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"uriTemplate": "dbrain://item/{lookup}",
			"name":        "Brain Item",
			"description": "Rendered item note and metadata for a source key, external id, URL, or note path. URL-encode the lookup value.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "dbrain://source/{lookup}",
			"name":        "Brain Source",
			"description": "Rendered source note and metadata for a source key, canonical URL, or note path. URL-encode the lookup value.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "dbrain://search/{query}",
			"name":        "Brain Search",
			"description": "Search results for a query. Supports query params limit and repeated source_type values.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://entity/{query}",
			"name":        "Brain Entity Map",
			"description": "Derived entities for a query. Supports query params kind and limit.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://topic/{query}",
			"name":        "Brain Topic Map",
			"description": "Topic map for a concept. Supports query params repeated source_type values, seed_limit, and related_limit.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://topic-note/{query}",
			"name":        "Brain Topic Note Preview",
			"description": "Rendered markdown preview for a generated topic note. Supports query params repeated source_type values, seed_limit, and related_limit.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "dbrain://research/{query}",
			"name":        "Brain Research Pack",
			"description": "Research pack for a question. Supports query params repeated source_type values, limit, include_related, related_limit, and seed_limit.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://stats/items{?source_type,group_by}",
			"name":        "Brain Item Count Query",
			"description": "Item counts with optional source_type and group_by query parameters.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://stats/sources{?source_type,extract_tool,summary_status,extract_status,group_by}",
			"name":        "Brain Source Count Query",
			"description": "Source counts with optional filters and grouping query parameters.",
			"mimeType":    "application/json",
		},
	}
}

func mcpOverviewText() string {
	return `# dbrain MCP

The local dbrain MCP server is read-only.

## Tools

- ` + "`dbrain_search`" + `: search the local corpus, including exact user-tag aliases for multi-word entity queries
- ` + "`dbrain_get`" + `: load DB-backed item/source metadata, capped content sections, and limited linked/quoted context; use ` + "`content_mode=rendered`" + ` only when rendered Markdown is needed
- ` + "`dbrain_entity_map`" + `: browse derived entities across the local brain
- ` + "`dbrain_topic_map`" + `: build a compact topic graph around a concept
- ` + "`dbrain_topic_brief`" + `: build a richer topic brief with grouped pivots and markdown preview
- ` + "`dbrain_research_pack`" + `: bundle retrieve-only evidence, query/tag hints, exact tag and corpus coverage counts, suggested follow-ups, and an optional topic brief
- ` + "`dbrain_related`" + `: follow item-to-source links or source backlinks
- ` + "`dbrain_whats_new`" + `: review recent local imports, enrichments, failures, and blocked work from a timestamp or cursor
- ` + "`dbrain_stats_items`" + `: count item signals
- ` + "`dbrain_stats_sources`" + `: count sources by filters or status
- ` + "`dbrain_stats_activity`" + `: inspect recent pipeline activity
- ` + "`dbrain_stats_backlog`" + `: inspect remaining queued work

## Resources

- ` + "`dbrain://mcp/overview`" + `
- ` + "`dbrain://stats/activity`" + `
- ` + "`dbrain://stats/backlog`" + `
- ` + "`dbrain://stats/items`" + `
- ` + "`dbrain://stats/sources`" + `
- ` + "`dbrain://item/{lookup}`" + `
- ` + "`dbrain://source/{lookup}`" + `
- ` + "`dbrain://search/{query}`" + `
- ` + "`dbrain://entity/{query}`" + `
- ` + "`dbrain://topic/{query}`" + `
- ` + "`dbrain://topic-note/{query}`" + `
- ` + "`dbrain://research/{query}`" + `

## Prompts

- ` + "`brain_research`" + `: research a question from the brain
- ` + "`brain_browse`" + `: browse outward from a known note
- ` + "`brain_entity_browse`" + `: browse derived entities from stable local metadata
- ` + "`brain_topic_map`" + `: assemble a topic map from a keyword or concept
- ` + "`brain_topic_brief`" + `: assemble a browsable topic brief and note preview
- ` + "`brain_status`" + `: inspect pipeline activity and backlog

## Suggested workflows

1. Research: call ` + "`dbrain_research_pack`" + ` first, check ` + "`coverage.recall_note`" + ` and exact tag counts, then inspect the strongest hits with ` + "`dbrain_get`" + ` using ` + "`content_mode=evidence`" + ` or expand with ` + "`dbrain_related`" + `. Answer from the collector's intentionally selective saved corpus; prioritize accuracy over appearing objective, and do not add outside balance unless asked.
2. Browse: call ` + "`dbrain_get`" + ` on a known item or source, then expand with ` + "`dbrain_related`" + `. Prefer DB-backed modes (` + "`brief`" + `, ` + "`evidence`" + `, ` + "`raw`" + `); media refs appear on media-backed item search/evidence/get payloads, while the claim-bearing text appears in ` + "`x_media_transcript`" + ` and ` + "`ocr_text`" + ` sections. Sources can have their own ` + "`user_tags`" + `, and source ` + "`backlinks`" + ` include the referencing saved item's ` + "`user_tags`" + ` when that context differs. Use ` + "`rendered`" + ` for note shape only.
3. Entity browse: call ` + "`dbrain_entity_map`" + ` or read ` + "`dbrain://entity/{query}`" + ` to find people, repos, orgs, and sites connected to the corpus.
4. Topic map: call ` + "`dbrain_topic_map`" + ` or read ` + "`dbrain://topic/{query}`" + ` for a compact graph around a concept.
5. Topic brief: call ` + "`dbrain_topic_brief`" + ` or read ` + "`dbrain://topic-note/{query}`" + ` for grouped pivots and a rendered note preview.
6. Monitor and review: call ` + "`dbrain_whats_new`" + ` for a cursor-paged review feed of recent local evidence changes, call ` + "`dbrain_stats_activity`" + ` and ` + "`dbrain_stats_backlog`" + ` for aggregate health, then use ` + "`dbrain_stats_sources`" + ` for deeper breakdowns.`
}
