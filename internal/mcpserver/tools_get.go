package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Server) toolGet(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookup             string `json:"lookup"`
		Query              string `json:"query"`
		ContentMode        string `json:"content_mode"`
		MaxCharsPerSection int    `json:"max_chars_per_section"`
		IncludeContent     *bool  `json:"include_content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode get args: %w", err)
	}
	contentMode, err := resolveGetContentMode(args.ContentMode, args.IncludeContent)
	if err != nil {
		return nil, err
	}
	maxChars := maxGetSectionChars(args.MaxCharsPerSection)

	payload, text, err := s.getPayloadForLookup(ctx, args.Lookup, contentMode, maxChars, args.Query)
	if err != nil {
		return nil, err
	}
	return toolOKResult(text, payload), nil
}

func (s *Server) toolGetMany(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookups            []string `json:"lookups"`
		Query              string   `json:"query"`
		ContentMode        string   `json:"content_mode"`
		MaxCharsPerSection int      `json:"max_chars_per_section"`
		IncludeContent     *bool    `json:"include_content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode get_many args: %w", err)
	}
	contentMode, err := resolveGetContentMode(args.ContentMode, args.IncludeContent)
	if err != nil {
		return nil, err
	}
	maxChars := maxGetSectionChars(args.MaxCharsPerSection)

	lookups := uniqueGetLookups(args.Lookups)
	if len(lookups) == 0 {
		return nil, fmt.Errorf("lookups is required")
	}
	if len(lookups) > maxGetManyLookups {
		return nil, fmt.Errorf("too many lookups: got %d, maximum is %d", len(lookups), maxGetManyLookups)
	}

	results := make([]map[string]interface{}, 0, len(lookups))
	errors := make([]getManyError, 0)
	texts := make([]string, 0, len(lookups))
	for _, lookup := range lookups {
		payload, text, err := s.getPayloadForLookup(ctx, lookup, contentMode, maxChars, args.Query)
		if err != nil {
			errors = append(errors, getManyError{Lookup: lookup, Error: err.Error()})
			continue
		}
		payload["lookup"] = lookup
		results = append(results, payload)
		texts = append(texts, text)
	}

	payload := map[string]interface{}{
		"lookups":               lookups,
		"content_mode":          contentMode,
		"max_chars_per_section": maxChars,
		"count":                 len(results),
		"results":               results,
		"errors":                errors,
	}
	if query := strings.TrimSpace(args.Query); query != "" {
		payload["query"] = query
	}
	return toolOKResult(formatGetManyPayload(payload, texts), payload), nil
}
