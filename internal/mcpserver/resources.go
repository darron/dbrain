package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (s *Server) handleResourcesList() map[string]interface{} {
	return map[string]interface{}{
		"resources": resourceDefinitions(),
	}
}

func (s *Server) handleResourceTemplatesList() map[string]interface{} {
	return map[string]interface{}{
		"resourceTemplates": resourceTemplateDefinitions(),
	}
}

func (s *Server) handleResourceRead(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode resources/read args: %w", err)
	}
	if strings.TrimSpace(args.URI) == "" {
		return nil, fmt.Errorf("resources/read requires a uri")
	}

	content, err := s.readResource(ctx, args.URI)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"contents": content,
	}, nil
}

func (s *Server) readResource(ctx context.Context, uri string) ([]map[string]string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse resource uri: %w", err)
	}
	if parsed.Scheme != "dbrain" {
		return nil, fmt.Errorf("unsupported resource scheme %q", parsed.Scheme)
	}

	switch parsed.Host {
	case "mcp":
		return s.readMCPResource(uri, parsed)
	case "stats":
		return s.readStatsResource(ctx, uri, parsed)
	case "item":
		lookup, err := resourceLookup(parsed, "lookup")
		if err != nil {
			return nil, err
		}
		return s.readItemResource(ctx, uri, lookup)
	case "source":
		lookup, err := resourceLookup(parsed, "lookup")
		if err != nil {
			return nil, err
		}
		return s.readSourceResource(ctx, uri, lookup)
	case "search":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readSearchResource(ctx, uri, parsed, query)
	case "entity":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readEntityResource(ctx, uri, parsed, query)
	case "topic":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readTopicResource(ctx, uri, parsed, query)
	case "topic-note":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readTopicNoteResource(ctx, uri, parsed, query)
	case "research":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readResearchResource(ctx, uri, parsed, query)
	default:
		return nil, fmt.Errorf("unsupported resource host %q", parsed.Host)
	}
}
