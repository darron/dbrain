package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func (s *Server) readMCPResource(uri string, parsed *url.URL) ([]map[string]string, error) {
	switch strings.Trim(parsed.Path, "/") {
	case "overview":
		return []map[string]string{{
			"uri":      uri,
			"mimeType": "text/markdown",
			"text":     strings.TrimSpace(mcpOverviewText()),
		}}, nil
	default:
		return nil, fmt.Errorf("unknown mcp resource %q", parsed.Path)
	}
}

func (s *Server) readItemResource(ctx context.Context, uri string, lookup string) ([]map[string]string, error) {
	item, err := s.st.GetItem(ctx, lookup)
	if err != nil {
		return nil, err
	}

	noteBody, err := readVaultNote(s.cfg.VaultDir, item.NotePath)
	if err != nil {
		noteBody = fmt.Sprintf("_Note unreadable: %v_", err)
	}
	text := strings.TrimSpace(fmt.Sprintf(`# %s

- Kind: item
- Source key: %s
- Source type: %s
- URL: %s
- Note: %s

## Note

%s
`, firstNonEmpty(item.Title, item.SourceKey), item.SourceKey, item.SourceType, item.CanonicalURL, item.NotePath, noteBody))

	return []map[string]string{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     text,
	}}, nil
}

func (s *Server) readSourceResource(ctx context.Context, uri string, lookup string) ([]map[string]string, error) {
	source, err := s.st.GetSource(ctx, lookup)
	if err != nil {
		return nil, err
	}

	noteBody, err := readVaultNote(s.cfg.VaultDir, source.NotePath)
	if err != nil {
		noteBody = fmt.Sprintf("_Note unreadable: %v_", err)
	}
	text := strings.TrimSpace(fmt.Sprintf(`# %s

- Kind: source
- Source key: %s
- Source type: %s
- URL: %s
- Note: %s

## Note

%s
`, firstNonEmpty(source.Title, source.SourceKey, source.CanonicalURL), source.SourceKey, source.SourceType, source.CanonicalURL, source.NotePath, noteBody))

	return []map[string]string{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     text,
	}}, nil
}

func (s *Server) readSearchResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	limit := defaultInt(intFromQuery(parsed.Query(), "limit"), 10)
	results, err := s.st.Search(ctx, strings.TrimSpace(query), limit)
	if err != nil {
		return nil, err
	}
	results = filterSearchResults(ctx, s.st, results, listFromQuery(parsed.Query(), "source_type"))

	payload := map[string]interface{}{
		"query":   query,
		"count":   len(results),
		"results": results,
	}
	return jsonResourceContents(uri, payload)
}

func (s *Server) readEntityResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	results, err := entities.Search(ctx, s.st, query, entities.SearchOptions{
		Kind:  strings.TrimSpace(parsed.Query().Get("kind")),
		Limit: intFromQuery(parsed.Query(), "limit"),
	})
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"query":    query,
		"kind":     strings.TrimSpace(parsed.Query().Get("kind")),
		"count":    len(results),
		"entities": results,
	}
	return jsonResourceContents(uri, payload)
}

func (s *Server) readTopicResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	graph, err := topics.Build(ctx, s.st, query, topics.Options{
		SourceTypes:  listFromQuery(parsed.Query(), "source_type"),
		SeedLimit:    intFromQuery(parsed.Query(), "seed_limit"),
		RelatedLimit: intFromQuery(parsed.Query(), "related_limit"),
	})
	if err != nil {
		return nil, err
	}
	return jsonResourceContents(uri, graph)
}

func (s *Server) readTopicNoteResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	graph, err := topics.Build(ctx, s.st, query, topics.Options{
		SourceTypes:  listFromQuery(parsed.Query(), "source_type"),
		SeedLimit:    intFromQuery(parsed.Query(), "seed_limit"),
		RelatedLimit: intFromQuery(parsed.Query(), "related_limit"),
	})
	if err != nil {
		return nil, err
	}
	return []map[string]string{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     vault.RenderTopic(graph),
	}}, nil
}

func (s *Server) readResearchResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	pack, err := s.buildResearchPack(ctx, ResearchPackOptions{
		Question:       query,
		Topic:          firstQueryValue(parsed.Query(), "topic"),
		Limit:          intFromQuery(parsed.Query(), "limit"),
		SourceTypes:    listFromQuery(parsed.Query(), "source_type"),
		IncludeRelated: boolFromQuery(parsed.Query(), "include_related"),
		RelatedLimit:   intFromQuery(parsed.Query(), "related_limit"),
		SeedLimit:      intFromQuery(parsed.Query(), "seed_limit"),
		IncludeTopic:   boolPtrFromQuery(parsed.Query(), "include_topic_brief"),
		MaxCharsPerDoc: intFromQuery(parsed.Query(), "max_chars_per_doc"),
	})
	if err != nil {
		return nil, err
	}
	return jsonResourceContents(uri, pack)
}
