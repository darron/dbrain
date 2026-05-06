package mcpserver

import (
	"fmt"
	"strings"
)

func toolOKResult(text string, payload interface{}) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
		"structuredContent": payload,
		"isError":           false,
	}
}

func toolErrorResult(err error) map[string]interface{} {
	message := err.Error()
	return map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": message},
		},
		"structuredContent": map[string]interface{}{
			"error": map[string]interface{}{
				"message":     message,
				"suggestions": toolErrorSuggestions(message),
			},
		},
		"isError": true,
	}
}

func toolErrorSuggestions(message string) []string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unknown tool"):
		return []string{
			"Call tools/list and choose one of the advertised dbrain_* tools.",
			"Use dbrain_research_pack for broad research, dbrain_search for keyword lookup, dbrain_get for a known lookup, or dbrain_related for graph expansion.",
		}
	case strings.Contains(lower, "lookup is required"):
		return []string{
			"Pass a source key, external id, URL, or note path as lookup.",
			"If you do not know the lookup yet, call dbrain_research_pack or dbrain_search first.",
		}
	case strings.Contains(lower, "lookup not found"):
		return []string{
			"Verify the source key, external id, URL, or note path and retry.",
			"Use dbrain_search or dbrain_research_pack to find a current lookup before calling dbrain_get or dbrain_related.",
		}
	case strings.Contains(lower, "lookups is required"):
		return []string{
			"Pass one or more source keys, external ids, URLs, or note paths in lookups.",
			"Use dbrain_research_pack next_steps or dbrain_search results as inputs to dbrain_get_many.",
		}
	case strings.Contains(lower, "too many lookups"):
		return []string{
			fmt.Sprintf("Split the request into batches of %d or fewer lookups.", maxGetManyLookups),
		}
	case strings.Contains(lower, "unsupported content_mode"):
		return []string{
			"Use content_mode=brief, evidence, raw, or rendered.",
			"Prefer content_mode=evidence for agent research and content_mode=rendered only when note shape matters.",
		}
	default:
		return []string{
			"Inspect the error message, adjust the tool arguments, and retry.",
			"Call dbrain://mcp/overview or tools/list for the supported workflow and tool surface.",
		}
	}
}
