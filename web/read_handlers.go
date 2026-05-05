package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func (s *server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	backlog, err := s.backlog(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	activity, err := s.activity(r.Context(), defaultActivityWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sourceActivity, err := s.sourceActivity(r.Context(), store.SourceActivityFilter{Limit: defaultEventLimit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, BootstrapResponse{
		App: AppInfo{
			Name:   "dbrain",
			HasFTS: s.store.HasFTS(),
		},
		Backlog:        backlog,
		Activity:       activity,
		SourceActivity: sourceActivity,
	})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := clampLimit(parseIntDefault(r.URL.Query().Get("limit"), defaultSearchLimit), 1, maxSearchLimit)
	if query == "" {
		writeJSON(w, http.StatusOK, SearchResponse{Query: "", Limit: limit, Results: []model.SearchResult{}})
		return
	}

	results, err := s.store.Search(r.Context(), query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, SearchResponse{
		Query:   query,
		Limit:   limit,
		Results: results,
	})
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	lookup := strings.TrimSpace(r.URL.Query().Get("lookup"))
	if lookup == "" {
		writeMessage(w, http.StatusBadRequest, "lookup is required")
		return
	}

	item, itemErr := s.store.GetItem(r.Context(), lookup)
	if itemErr == nil {
		noteContent, noteError := s.loadNote(item.NotePath)
		linkedSources, err := s.store.ListSourcesForItem(r.Context(), item.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		quotedPosts := s.loadQuotedPosts(r.Context(), item.ID)
		itemResponse := itemWebResponse(item)
		writeJSON(w, http.StatusOK, GetResponse{
			Lookup:        lookup,
			Kind:          "item",
			Item:          &itemResponse,
			LinkedSources: linkedSources,
			QuotedPosts:   quotedPosts,
			NoteContent:   noteContent,
			NoteError:     noteError,
		})
		return
	}

	source, sourceErr := s.store.GetSource(r.Context(), lookup)
	if sourceErr != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("lookup not found: %s", lookup))
		return
	}

	noteContent, noteError := s.loadNote(source.NotePath)
	backlinks, err := s.store.ListBacklinksForSource(r.Context(), source.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, GetResponse{
		Lookup:      lookup,
		Kind:        "source",
		Source:      &source,
		Backlinks:   backlinks,
		NoteContent: noteContent,
		NoteError:   noteError,
	})
}

func (s *server) loadQuotedPosts(ctx context.Context, itemID int64) []ItemResponse {
	childIDs, err := s.store.ListItemChildLinks(ctx, itemID, "quoted_post")
	if err != nil || len(childIDs) == 0 {
		return nil
	}
	posts := make([]ItemResponse, 0, len(childIDs))
	for _, id := range childIDs {
		child, err := s.store.GetItemByID(ctx, id)
		if err == nil {
			posts = append(posts, itemWebResponse(child))
		}
	}
	return posts
}

func itemWebResponse(item model.Item) ItemResponse {
	return ItemResponse{
		Item:  item,
		Media: mediaWebResponse(item.Media),
	}
}

func mediaWebResponse(refs []model.ItemMediaRef) []MediaRefResponse {
	if len(refs) == 0 {
		return nil
	}
	out := make([]MediaRefResponse, 0, len(refs))
	for _, ref := range refs {
		out = append(out, MediaRefResponse{
			MediaAssetID:   ref.MediaAssetID,
			Ordinal:        ref.Ordinal,
			ExpandedURL:    ref.ExpandedURL,
			RemoteURL:      ref.RemoteURL,
			MediaType:      ref.MediaType,
			DownloadStatus: ref.DownloadStatus,
			ArchiveURL:     ref.ArchiveURL,
			ArchiveStatus:  ref.ArchiveStatus,
			Width:          ref.Width,
			Height:         ref.Height,
		})
	}
	return out
}

func (s *server) loadNote(notePath string) (string, string) {
	fullPath, err := s.resolveNotePath(notePath)
	if err != nil {
		return "", err.Error()
	}
	if fullPath == "" {
		return "", ""
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", noteReadError(notePath, err)
	}
	return string(content), ""
}

func noteReadError(notePath string, err error) string {
	reason := "failed"
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		reason = pathErr.Err.Error()
	}
	return fmt.Sprintf("read note %s: %s", filepath.ToSlash(notePath), reason)
}

func (s *server) resolveNotePath(notePath string) (string, error) {
	notePath = strings.TrimSpace(notePath)
	if notePath == "" {
		return "", nil
	}

	fullPath := filepath.Clean(filepath.Join(s.cfg.VaultDir, filepath.FromSlash(notePath)))
	vaultRoot := filepath.Clean(s.cfg.VaultDir)
	if fullPath != vaultRoot && !strings.HasPrefix(fullPath, vaultRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("note path escapes vault: %s", notePath)
	}
	return fullPath, nil
}
