package mcpserver

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (s *Server) handlePromptsList() map[string]interface{} {
	return map[string]interface{}{
		"prompts": promptDefinitions(),
	}
}

func (s *Server) handlePromptGet(raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode prompts/get args: %w", err)
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, fmt.Errorf("prompts/get requires a name")
	}

	description, text, err := renderPrompt(name, args.Arguments)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"description": description,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": map[string]interface{}{
					"type": "text",
					"text": text,
				},
			},
		},
	}, nil
}

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

func renderPrompt(name string, args map[string]interface{}) (string, string, error) {
	switch name {
	case "brain_research":
		question := strings.TrimSpace(argumentString(args, "question"))
		if question == "" {
			return "", "", fmt.Errorf("brain_research requires a question argument")
		}
		sourceTypes := strings.TrimSpace(argumentString(args, "source_types"))
		includeRelated := argumentBool(args, "include_related")
		return "Research a question against the local brain.", strings.TrimSpace(fmt.Sprintf(`Answer the following question from the local dbrain corpus:

Question: %s

Recommended workflow:
1. Call dbrain_research_pack first with limit=8 and include_related=%t.
2. If source_types are provided, pass them through as source_types: [%s].
3. Use the returned query_plan, coverage.recall_note, exact_tag_matches, top tags, and next_steps to decide whether follow-up calls are needed.
4. If the returned pack used_topic_brief=true, use the topic brief pivots and summary as the primary overview surface.
5. Use dbrain_related on the most relevant item or source when you need to follow supporting links or backlinks.
6. Review the strongest evidence with dbrain_get using content_mode=evidence before making detailed claims. Use content_mode=raw only when the raw extract/transcript/OCR is needed, and content_mode=rendered only when the rendered Markdown shape is useful.
7. Return a concise answer with citations to source keys and note paths.
8. Answer from the collector's saved corpus; do not add outside balance or model-background viewpoints unless the user asks.
`, question, includeRelated, quotedCSV(sourceTypes))), nil
	case "brain_browse":
		lookup := strings.TrimSpace(argumentString(args, "lookup"))
		if lookup == "" {
			return "", "", fmt.Errorf("brain_browse requires a lookup argument")
		}
		return "Browse the local item/source graph from a known starting point.", strings.TrimSpace(fmt.Sprintf(`Follow the local dbrain graph starting from:

Lookup: %s

Recommended workflow:
1. Call dbrain_get for the starting lookup with content_mode=evidence.
2. Call dbrain_related for the same lookup.
3. Open the most relevant linked notes with dbrain_get content_mode=evidence, or content_mode=rendered if the rendered note shape matters.
4. Summarize what the starting note connects to and why those links matter.
`, lookup)), nil
	case "brain_entity_browse":
		query := strings.TrimSpace(argumentString(args, "query"))
		if query == "" {
			return "", "", fmt.Errorf("brain_entity_browse requires a query argument")
		}
		kind := strings.TrimSpace(argumentString(args, "kind"))
		return "Browse derived entities from the local brain.", strings.TrimSpace(fmt.Sprintf(`Browse the derived entities for this query from the local dbrain corpus:

Query: %s

Recommended workflow:
1. Call dbrain_entity_map with the query, optional kind=%q, and limit=10.
2. Inspect the most relevant entity notes with dbrain_get content_mode=evidence when you need more detail.
3. Summarize the strongest matching entities, why they matter, and which notes reference them.
4. Cite entity keys, note paths, and any especially useful supporting item/source notes.
`, query, kind)), nil
	case "brain_topic_map":
		topic := strings.TrimSpace(argumentString(args, "topic"))
		if topic == "" {
			return "", "", fmt.Errorf("brain_topic_map requires a topic argument")
		}
		sourceTypes := strings.TrimSpace(argumentString(args, "source_types"))
		maxNodes := argumentInt(args, "max_nodes", 6)
		return "Build a topic map from the local brain.", strings.TrimSpace(fmt.Sprintf(`Build a compact topic map for this concept from the local dbrain corpus:

Topic: %s

Recommended workflow:
1. Call dbrain_topic_map with the topic, optional source_types [%s], seed_limit=%d, and related_limit=2.
2. Inspect the most important nodes with dbrain_get content_mode=evidence when you need more detail.
3. Return a compact topic map with:
   - key nodes
   - what each node contributes
   - important relationships between nodes
   - suggested follow-up notes worth reading next
4. Cite each node with source keys and note paths.
`, topic, quotedCSV(sourceTypes), maxNodes)), nil
	case "brain_topic_brief":
		topic := strings.TrimSpace(argumentString(args, "topic"))
		if topic == "" {
			return "", "", fmt.Errorf("brain_topic_brief requires a topic argument")
		}
		sourceTypes := strings.TrimSpace(argumentString(args, "source_types"))
		maxNodes := argumentInt(args, "max_nodes", 6)
		return "Build a richer topic brief from the local brain.", strings.TrimSpace(fmt.Sprintf(`Build a browsable topic brief for this concept from the local dbrain corpus:

Topic: %s

Recommended workflow:
1. Call dbrain_topic_brief with the topic, optional source_types [%s], seed_limit=%d, and related_limit=2.
2. Inspect the grouped pivots for projects, orgs, sites, and people.
3. Use dbrain_get content_mode=evidence on the most relevant seed notes or pivot note paths when you need supporting detail.
4. Return:
   - a short synthesis of what the topic is about in this corpus
   - the most useful entity pivots
   - the best starting notes
   - the most important related notes
5. Prefer citing source keys and note paths from the structured topic brief. Use the markdown preview when a rendered note shape is helpful.
`, topic, quotedCSV(sourceTypes), maxNodes)), nil
	case "brain_status":
		windowMinutes := argumentInt(args, "window_minutes", 15)
		windowSeconds := windowMinutes * 60
		return "Inspect the local brain pipeline state.", strings.TrimSpace(fmt.Sprintf(`Inspect the local dbrain pipeline and summarize its current state.

Recommended workflow:
1. Call dbrain_stats_activity with window_seconds=%d.
2. Call dbrain_stats_backlog.
3. If you need more detail, call dbrain_stats_sources grouped by source-type or summary-status.
4. Decide whether the pipeline is active, stalled, or drained.
5. Call out the largest remaining backlog buckets and the most recent write timestamps.
`, windowSeconds)), nil
	default:
		return "", "", fmt.Errorf("unknown prompt %q", name)
	}
}

func argumentString(args map[string]interface{}, key string) string {
	if len(args) == 0 {
		return ""
	}
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func argumentBool(args map[string]interface{}, key string) bool {
	if len(args) == 0 {
		return false
	}
	value, ok := args[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	default:
		return false
	}
}

func argumentInt(args map[string]interface{}, key string, fallback int) int {
	if len(args) == 0 {
		return fallback
	}
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case int:
		if typed > 0 {
			return typed
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func quotedCSV(raw string) string {
	parts := strings.Split(raw, ",")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			quoted = append(quoted, fmt.Sprintf("%q", part))
		}
	}
	return strings.Join(quoted, ", ")
}
