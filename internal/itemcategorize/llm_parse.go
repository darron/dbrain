package itemcategorize

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/categoryvocab"
)

// parseCategorizationJSON strips optional markdown fences, parses the JSON,
// and applies canonical vocab rules if provided.
func parseCategorizationJSON(content string, modelName string, vocab categoryvocab.Vocab) (Result, error) {
	text := strings.TrimSpace(content)

	// Strip markdown code fences if the model wrapped the output.
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		end := strings.LastIndex(text, "```")
		if end > start {
			text = strings.TrimSpace(text[start:end])
		}
	}

	// Find the JSON object boundaries in case there is leading or trailing prose.
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if j := strings.LastIndex(text, "}"); j >= 0 && j < len(text)-1 {
		text = text[:j+1]
	}

	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return Result{}, fmt.Errorf("parse categorization JSON: %w (content: %q)", err, truncate(content, 300))
	}

	// Normalize and apply vocab to tags, categories, and primary_category.
	// ApplyToTokens always normalizes to lowercase-hyphenated form, then
	// applies alias resolution and drops.
	result.Tags = vocab.ApplyToTokens(result.Tags)
	result.Categories = vocab.ApplyToTokens(result.Categories)
	if pc := vocab.ApplyToTokens([]string{result.PrimaryCategory}); len(pc) > 0 {
		result.PrimaryCategory = pc[0]
	} else {
		result.PrimaryCategory = ""
	}

	result.Model = modelName
	result.RawResponse = content
	return result, nil
}
