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

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
	"github.com/darron/dbrain/internal/version"
)

const defaultAppleNotesDBRelPath = "Library/Group Containers/group.com.apple.notes/NoteStore.sqlite"

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
			Name:    "dbrain",
			HasFTS:  s.store.HasFTS(),
			Version: webVersionInfo(),
		},
		Auth:           s.authInfo(r.Context()),
		Backlog:        backlog,
		Activity:       activity,
		SourceActivity: sourceActivity,
	})
}

func (s *server) handleDoctorFullDiskAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	path, err := fullDiskAccessProbePathWithOverride(s.cfg, s.fullDiskPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	executable, _ := os.Executable()
	response := FullDiskAccessResponse{
		Path:       path,
		Executable: executable,
		PID:        os.Getpid(),
	}
	file, err := os.Open(path)
	if err != nil {
		response.Error = err.Error()
		writeJSON(w, http.StatusOK, response)
		return
	}
	_ = file.Close()
	response.OK = true
	response.Readable = true
	writeJSON(w, http.StatusOK, response)
}

func fullDiskAccessProbePathWithOverride(cfg config.Config, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Abs(strings.TrimSpace(override))
	}
	if value := runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_APPLE_NOTES_DB_PATH"); strings.TrimSpace(value) != "" {
		return filepath.Abs(strings.TrimSpace(value))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home directory: empty HOME")
	}
	return filepath.Join(home, defaultAppleNotesDBRelPath), nil
}

func webVersionInfo() WebVersionInfo {
	current := version.Current()
	return WebVersionInfo{
		Commit:         current.Commit,
		Short:          current.Short,
		GitStatus:      current.GitStatus,
		ReleaseVersion: current.ReleaseVersion,
		ModuleVersion:  current.ModuleVersion,
	}
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := clampLimit(parseIntDefault(r.URL.Query().Get("limit"), defaultSearchLimit), 1, maxSearchLimit)
	if query == "" {
		writeJSON(w, http.StatusOK, SearchResponse{Query: "", Limit: limit, Results: []SearchResultResponse{}})
		return
	}

	results, err := s.store.Search(r.Context(), query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	responseResults, err := s.searchResultsWebResponse(r.Context(), results)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, SearchResponse{
		Query:   query,
		Limit:   limit,
		Results: responseResults,
	})
}

func (s *server) searchResultsWebResponse(ctx context.Context, results []model.SearchResult) ([]SearchResultResponse, error) {
	sourceKeys := make([]string, 0, len(results))
	for _, result := range results {
		sourceKeys = append(sourceKeys, result.SourceKey)
	}
	mediaBySourceKey, err := s.store.ListItemMediaRefsForSourceKeys(ctx, sourceKeys)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResultResponse, 0, len(results))
	for _, result := range results {
		out = append(out, SearchResultResponse{
			SourceKey:     result.SourceKey,
			SourceType:    result.SourceType,
			ExternalID:    result.ExternalID,
			Title:         result.Title,
			AuthorHandle:  result.AuthorHandle,
			AuthorName:    result.AuthorName,
			CanonicalURL:  result.CanonicalURL,
			PrimaryDomain: result.PrimaryDomain,
			NotePath:      result.NotePath,
			UserTags:      result.UserTags,
			Snippet:       result.Snippet,
			Media:         mediaWebResponse(mediaBySourceKey[result.SourceKey]),
		})
	}
	return out, nil
}

func (s *server) handleSchedulerSyncAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if s.schedulerStatus == nil {
		writeJSON(w, http.StatusOK, SchedulerSyncAllResponse{})
		return
	}
	writeJSON(w, http.StatusOK, SchedulerSyncAllResponse{
		SyncAll: s.schedulerStatus(),
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

	sourceResponse := sourceWebResponse(source)
	writeJSON(w, http.StatusOK, GetResponse{
		Lookup:      lookup,
		Kind:        "source",
		Source:      &sourceResponse,
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
		ID:                     item.ID,
		SourceKey:              item.SourceKey,
		SourceType:             item.SourceType,
		ExternalID:             item.ExternalID,
		CanonicalURL:           item.CanonicalURL,
		Title:                  item.Title,
		AuthorHandle:           item.AuthorHandle,
		AuthorName:             item.AuthorName,
		PublishedAt:            item.PublishedAt,
		SavedAt:                item.SavedAt,
		SyncedAt:               item.SyncedAt,
		Language:               item.Language,
		Text:                   item.Text,
		ArticleTitle:           item.ArticleTitle,
		ArticleText:            item.ArticleText,
		PrimaryCategory:        item.PrimaryCategory,
		PrimaryDomain:          item.PrimaryDomain,
		LinksJSON:              item.LinksJSON,
		Categories:             item.Categories,
		Domains:                item.Domains,
		GitHubURLs:             item.GitHubURLs,
		FolderNames:            item.FolderNames,
		LikeCount:              item.LikeCount,
		RepostCount:            item.RepostCount,
		ReplyCount:             item.ReplyCount,
		QuoteCount:             item.QuoteCount,
		BookmarkCount:          item.BookmarkCount,
		NotePath:               item.NotePath,
		ImportedAt:             item.ImportedAt,
		UpdatedAt:              item.UpdatedAt,
		LastSeenAt:             item.LastSeenAt,
		XPostText:              item.XPostText,
		XPostLang:              item.XPostLang,
		XPostFetchedAt:         item.XPostFetchedAt,
		XPostStatus:            item.XPostStatus,
		LinkExtractSyncedAt:    item.LinkExtractSyncedAt,
		SummaryText:            item.SummaryText,
		SummaryStatus:          item.SummaryStatus,
		SummaryModel:           item.SummaryModel,
		SummaryTool:            item.SummaryTool,
		SummarizedAt:           item.SummarizedAt,
		OCRText:                item.OCRText,
		OCRStatus:              item.OCRStatus,
		OCRModel:               item.OCRModel,
		OCRTool:                item.OCRTool,
		OCRAt:                  item.OCRAt,
		XMediaTranscriptStatus: item.XMediaTranscriptStatus,
		XMediaTranscriptAt:     item.XMediaTranscriptAt,
		UserTags:               item.UserTags,
		Media:                  mediaWebResponse(item.Media),
	}
}

func sourceWebResponse(source model.SourceDocument) SourceResponse {
	return SourceResponse{
		ID:            source.ID,
		SourceKey:     source.SourceKey,
		CanonicalURL:  source.CanonicalURL,
		NormalizedURL: source.NormalizedURL,
		SourceType:    source.SourceType,
		Domain:        source.Domain,
		Title:         source.Title,
		Description:   source.Description,
		SiteName:      source.SiteName,
		ExtractedText: source.ExtractedText,
		ExtractStatus: source.ExtractStatus,
		ExtractedAt:   source.ExtractedAt,
		ExtractTool:   source.ExtractTool,
		SummaryText:   source.SummaryText,
		SummaryStatus: source.SummaryStatus,
		SummaryModel:  source.SummaryModel,
		SummaryTool:   source.SummaryTool,
		SummarizedAt:  source.SummarizedAt,
		NotePath:      source.NotePath,
		UserTags:      source.UserTags,
		CreatedAt:     source.CreatedAt,
		UpdatedAt:     source.UpdatedAt,
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
	if strings.TrimSpace(notePath) == "" {
		return "", ""
	}
	root, err := vaultfs.Open(s.cfg.VaultDir)
	if err != nil {
		return "", err.Error()
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile(notePath)
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
