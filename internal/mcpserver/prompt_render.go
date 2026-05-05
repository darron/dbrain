package mcpserver

import (
	"fmt"
	"strings"
)

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
8. Answer from the collector's saved corpus, which is intentionally selective around what the collector cared about or found noteworthy. Do not criticize it for not being unbiased, and do not add outside balance or model-background viewpoints unless the user asks.
9. Prioritize accuracy over appearing objective: separate supported facts, source claims, opinions, and uncertainty; flag weak or conflicting evidence plainly.
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
