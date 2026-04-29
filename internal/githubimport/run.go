package githubimport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	apiVersion        = "2022-11-28"
	githubSiteName    = "GitHub"
)

type Options struct {
	Limit     int
	Force     bool
	Summarize bool
	Model     string
	CLI       string
	Length    string
	Timeout   time.Duration
	Logger    *slog.Logger
	Token     string
	APIBase   string
	Binary    string
}

type Stats struct {
	Viewer             string `json:"viewer"`
	PagesFetched       int    `json:"pages_fetched"`
	StarsProcessed     int    `json:"stars_processed"`
	ItemsCreated       int    `json:"items_created"`
	ItemsUpdated       int    `json:"items_updated"`
	ItemsUnchanged     int    `json:"items_unchanged"`
	ItemsRendered      int    `json:"items_rendered"`
	SourcesCreated     int    `json:"sources_created"`
	LinksCreated       int    `json:"links_created"`
	SourcesQueued      int    `json:"sources_queued"`
	SourcesExtracted   int    `json:"sources_extracted"`
	SourcesSummarized  int    `json:"sources_summarized"`
	SourcesRendered    int    `json:"sources_rendered"`
	SourcesUnchanged   int    `json:"sources_unchanged"`
	HomepageDiscovered int    `json:"homepage_discovered"`
	Errors             int    `json:"errors"`
}

type client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type viewer struct {
	Login string `json:"login"`
}

type starRecord struct {
	StarredAt string     `json:"starred_at"`
	Repo      repository `json:"repo"`
}

type repository struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	FullName      string      `json:"full_name"`
	HTMLURL       string      `json:"html_url"`
	Description   string      `json:"description"`
	Homepage      string      `json:"homepage"`
	Language      string      `json:"language"`
	Topics        []string    `json:"topics"`
	DefaultBranch string      `json:"default_branch"`
	Private       bool        `json:"private"`
	Archived      bool        `json:"archived"`
	Disabled      bool        `json:"disabled"`
	Fork          bool        `json:"fork"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
	PushedAt      string      `json:"pushed_at"`
	Owner         owner       `json:"owner"`
	License       *licenseRef `json:"license"`
}

type owner struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type licenseRef struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	SPDXID string `json:"spdx_id"`
}

type readmePayload struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	HTMLURL  string `json:"html_url"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}
	if opts.APIBase == "" {
		opts.APIBase = defaultAPIBaseURL
	}
	if strings.TrimSpace(opts.Token) == "" {
		opts.Token = runtimeenv.FirstNonEmpty(cfg.RootDir, "GITHUB_TOKEN")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return Stats{}, fmt.Errorf("GITHUB_TOKEN is required")
	}

	c := &client{
		baseURL: strings.TrimRight(opts.APIBase, "/"),
		token:   opts.Token,
		httpClient: &http.Client{
			Timeout: opts.Timeout,
		},
	}

	viewer, err := c.viewer(ctx)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{Viewer: viewer.Login}
	now := time.Now().UTC()
	githubSourceIDs := map[int64]struct{}{}
	homepageSourceIDs := map[int64]struct{}{}

	stop := false
	for page := 1; !stop; page++ {
		records, err := c.starredRepos(ctx, page)
		if err != nil {
			return stats, err
		}
		if len(records) == 0 {
			break
		}
		stats.PagesFetched++

		for _, record := range records {
			item, err := toItem(viewer.Login, record, now)
			if err != nil {
				return stats, err
			}

			result, err := st.UpsertItem(ctx, item)
			if err != nil {
				return stats, err
			}
			stats.StarsProcessed++
			switch result.Status {
			case model.UpsertCreated:
				stats.ItemsCreated++
			case model.UpsertUpdated:
				stats.ItemsUpdated++
			case model.UpsertUnchanged:
				stats.ItemsUnchanged++
			}

			shouldRender := result.Status != model.UpsertUnchanged
			if !shouldRender {
				if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
					shouldRender = true
				}
			}
			if shouldRender {
				if err := vault.WriteItem(cfg, item); err != nil {
					return stats, fmt.Errorf("render github star note %s: %w", item.SourceKey, err)
				}
				stats.ItemsRendered++
			}

			repoCandidate := repoSourceCandidate(record.Repo)
			repoLink, err := st.UpsertSourceLink(ctx, result.ItemID, repoCandidate)
			if err != nil {
				return stats, err
			}
			if repoLink.SourceCreated {
				stats.SourcesCreated++
			}
			if repoLink.LinkCreated {
				stats.LinksCreated++
			}
			githubSourceIDs[repoLink.SourceID] = struct{}{}

			source, err := st.GetSourceByID(ctx, repoLink.SourceID)
			if err != nil {
				return stats, err
			}
			if shouldRefreshGitHubSource(source, opts) {
				extract, contentHash, err := c.repoExtract(ctx, record.Repo)
				if err != nil {
					stats.Errors++
					debugLog(opts.Logger, "github repo extract failed", "repo", record.Repo.FullName, "error", err.Error())
					if _, saveErr := st.SaveSourceExtraction(ctx, repoLink.SourceID, model.ExtractResult{
						Status:      "error",
						Error:       err.Error(),
						Tool:        "github-api",
						ToolVersion: apiVersion,
					}, source.ContentHash); saveErr != nil {
						return stats, saveErr
					}
				} else if changed, err := st.SaveSourceExtraction(ctx, repoLink.SourceID, extract, contentHash); err != nil {
					return stats, err
				} else if changed {
					stats.SourcesExtracted++
				} else {
					stats.SourcesUnchanged++
				}
			}

			if homepageCandidate, ok := homepageSourceCandidate(record.Repo); ok {
				stats.HomepageDiscovered++
				homeLink, err := st.UpsertSourceLink(ctx, result.ItemID, homepageCandidate)
				if err != nil {
					return stats, err
				}
				if homeLink.SourceCreated {
					stats.SourcesCreated++
				}
				if homeLink.LinkCreated {
					stats.LinksCreated++
				}
				homepageSourceIDs[homeLink.SourceID] = struct{}{}
			}

			if opts.Limit > 0 && stats.StarsProcessed >= opts.Limit {
				stop = true
				break
			}
			if !opts.Force && result.Status != model.UpsertCreated {
				stop = true
				break
			}
		}
	}

	githubStats, err := summarizeGitHubSources(ctx, cfg, st, mapKeys(githubSourceIDs), opts)
	if err != nil {
		return stats, err
	}
	stats.SourcesQueued += githubStats.SourcesQueued
	stats.SourcesExtracted += githubStats.SourcesExtracted
	stats.SourcesSummarized += githubStats.SourcesSummarized
	stats.SourcesRendered += githubStats.SourcesRendered
	stats.SourcesUnchanged += githubStats.SourcesUnchanged
	stats.Errors += githubStats.Errors
	if !opts.Summarize {
		rendered, err := renderSourceNotes(ctx, cfg, st, mapKeys(githubSourceIDs))
		if err != nil {
			return stats, err
		}
		stats.SourcesRendered += rendered
	}

	homepageStats, err := summarizeHomepageSources(ctx, cfg, st, mapKeys(homepageSourceIDs), opts)
	if err != nil {
		return stats, err
	}
	stats.SourcesQueued += homepageStats.SourcesQueued
	stats.SourcesExtracted += homepageStats.SourcesExtracted
	stats.SourcesSummarized += homepageStats.SourcesSummarized
	stats.SourcesRendered += homepageStats.SourcesRendered
	stats.SourcesUnchanged += homepageStats.SourcesUnchanged
	stats.Errors += homepageStats.Errors

	return stats, nil
}

func summarizeGitHubSources(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (sourceenrich.Stats, error) {
	if len(sourceIDs) == 0 {
		return sourceenrich.Stats{}, nil
	}
	stats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, sourceIDs, sourceenrich.Options{
		Limit:                len(sourceIDs),
		Force:                false,
		AcceptCurrentSummary: true,
		Summarize:            opts.Summarize,
		Model:                opts.Model,
		CLI:                  opts.CLI,
		Length:               opts.Length,
		Timeout:              opts.Timeout,
		Logger:               opts.Logger,
		Binary:               opts.Binary,
	})
	return stats, err
}

func summarizeHomepageSources(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (sourceenrich.Stats, error) {
	if len(sourceIDs) == 0 {
		return sourceenrich.Stats{}, nil
	}
	stats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, sourceIDs, sourceenrich.Options{
		Limit:                len(sourceIDs),
		Force:                opts.Force,
		AcceptCurrentSummary: true,
		Summarize:            opts.Summarize,
		Model:                opts.Model,
		CLI:                  opts.CLI,
		Length:               opts.Length,
		Timeout:              opts.Timeout,
		Logger:               opts.Logger,
		Binary:               opts.Binary,
	})
	return stats, err
}

func renderSourceNotes(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64) (int, error) {
	rendered := 0
	for _, sourceID := range uniqueSorted(sourceIDs) {
		source, err := st.GetSourceByID(ctx, sourceID)
		if err != nil {
			return rendered, err
		}
		backlinks, err := st.ListBacklinksForSource(ctx, sourceID)
		if err != nil {
			return rendered, err
		}
		if err := vault.WriteSource(cfg, source, backlinks); err != nil {
			return rendered, err
		}
		rendered++
	}
	return rendered, nil
}

func shouldRefreshGitHubSource(source model.SourceDocument, opts Options) bool {
	if opts.Force {
		return true
	}
	if source.SourceType != "github" {
		return true
	}
	if source.ExtractStatus == "" || source.ExtractStatus == "error" {
		return true
	}
	if strings.TrimSpace(source.ExtractedText) == "" {
		return true
	}
	if source.ExtractTool != "github-api" {
		return true
	}
	return false
}

func toItem(viewerLogin string, record starRecord, now time.Time) (model.Item, error) {
	starredAt := normalizeTimestamp(record.StarredAt)
	externalID := record.Repo.FullName
	noteID := itemNoteID(record.Repo.FullName)
	links := make([]string, 0, 1)
	if value := strings.TrimSpace(record.Repo.Homepage); value != "" {
		links = append(links, value)
	}
	linksJSON, err := json.Marshal(links)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal github links: %w", err)
	}
	rawJSONBytes, err := json.Marshal(record)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal github star: %w", err)
	}

	item := model.Item{
		SourceKey:       "gh-star:" + viewerLogin + ":" + strings.ToLower(record.Repo.FullName),
		SourceType:      "github_star",
		ExternalID:      externalID,
		CanonicalURL:    strings.TrimSpace(record.Repo.HTMLURL),
		Title:           strings.TrimSpace(record.Repo.FullName),
		AuthorHandle:    strings.TrimSpace(record.Repo.Owner.Login),
		PublishedAt:     normalizeTimestamp(record.Repo.CreatedAt),
		SavedAt:         starredAt,
		SyncedAt:        starredAt,
		Language:        strings.TrimSpace(record.Repo.Language),
		Text:            strings.TrimSpace(record.Repo.Description),
		PrimaryCategory: "star",
		PrimaryDomain:   "github.com",
		LinksJSON:       string(linksJSON),
		Categories:      strings.Join(record.Repo.Topics, ", "),
		Domains:         "github.com",
		GitHubURLs:      strings.TrimSpace(record.Repo.HTMLURL),
		NotePath:        vault.NoteRelativePath("github", chooseYear(starredAt, normalizeTimestamp(record.Repo.CreatedAt), now.Format(time.RFC3339)), noteID),
		RawJSON:         string(rawJSONBytes),
		ImportedAt:      now,
		UpdatedAt:       now,
		LastSeenAt:      now,
	}
	if item.SyncedAt == "" {
		item.SyncedAt = now.Format(time.RFC3339)
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func (c *client) viewer(ctx context.Context) (viewer, error) {
	var out viewer
	if err := c.getJSON(ctx, "/user", "application/vnd.github+json", &out); err != nil {
		return viewer{}, fmt.Errorf("load github viewer: %w", err)
	}
	if strings.TrimSpace(out.Login) == "" {
		return viewer{}, fmt.Errorf("github viewer login missing")
	}
	return out, nil
}

func (c *client) starredRepos(ctx context.Context, page int) ([]starRecord, error) {
	query := url.Values{}
	query.Set("sort", "created")
	query.Set("direction", "desc")
	query.Set("per_page", "100")
	query.Set("page", fmt.Sprintf("%d", page))
	var out []starRecord
	if err := c.getJSON(ctx, "/user/starred?"+query.Encode(), "application/vnd.github.star+json", &out); err != nil {
		return nil, fmt.Errorf("load github starred repos page %d: %w", page, err)
	}
	return out, nil
}

func (c *client) repoReadme(ctx context.Context, fullName string) (readmePayload, bool, error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return readmePayload{}, false, fmt.Errorf("invalid repo full name %q", fullName)
	}
	req, err := c.request(ctx, http.MethodGet, "/repos/"+parts[0]+"/"+parts[1]+"/readme", "application/vnd.github+json")
	if err != nil {
		return readmePayload{}, false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return readmePayload{}, false, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return readmePayload{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return readmePayload{}, false, fmt.Errorf("github readme request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out readmePayload
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return readmePayload{}, false, fmt.Errorf("decode github readme: %w", err)
	}
	return out, true, nil
}

func (c *client) repoExtract(ctx context.Context, repo repository) (model.ExtractResult, string, error) {
	readme, hasReadme, err := c.repoReadme(ctx, repo.FullName)
	if err != nil {
		return model.ExtractResult{}, "", err
	}

	readmeText := ""
	if hasReadme {
		readmeText, err = decodeReadme(readme)
		if err != nil {
			return model.ExtractResult{}, "", err
		}
	}

	content := buildRepoExtractContent(repo, readmeText)
	rawJSONBytes, err := json.Marshal(map[string]any{
		"repo":   repo,
		"readme": map[string]any{"present": hasReadme, "path": readme.Path, "html_url": readme.HTMLURL},
	})
	if err != nil {
		return model.ExtractResult{}, "", fmt.Errorf("marshal github extract: %w", err)
	}

	result := model.ExtractResult{
		CanonicalURL: strings.TrimSpace(repo.HTMLURL),
		FinalURL:     strings.TrimSpace(repo.HTMLURL),
		Title:        strings.TrimSpace(repo.FullName),
		Description:  strings.TrimSpace(repo.Description),
		SiteName:     githubSiteName,
		Content:      content,
		RawJSON:      string(rawJSONBytes),
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "github-api",
		ToolVersion:  apiVersion,
	}
	return result, hashText(content), nil
}

func buildRepoExtractContent(repo repository, readme string) string {
	lines := []string{
		"Repository: " + strings.TrimSpace(repo.FullName),
	}
	if value := strings.TrimSpace(repo.Description); value != "" {
		lines = append(lines, "Description: "+value)
	}
	if value := strings.TrimSpace(repo.HTMLURL); value != "" {
		lines = append(lines, "GitHub URL: "+value)
	}
	if value := strings.TrimSpace(repo.Homepage); value != "" {
		lines = append(lines, "Homepage: "+value)
	}
	if value := strings.TrimSpace(repo.Language); value != "" {
		lines = append(lines, "Primary language: "+value)
	}
	if len(repo.Topics) > 0 {
		topics := append([]string(nil), repo.Topics...)
		sort.Strings(topics)
		lines = append(lines, "Topics: "+strings.Join(topics, ", "))
	}
	if value := strings.TrimSpace(repo.DefaultBranch); value != "" {
		lines = append(lines, "Default branch: "+value)
	}
	if repo.License != nil && strings.TrimSpace(repo.License.Name) != "" {
		lines = append(lines, "License: "+strings.TrimSpace(repo.License.Name))
	}
	lines = append(lines, "Private: "+boolLabel(repo.Private))
	lines = append(lines, "Archived: "+boolLabel(repo.Archived))
	lines = append(lines, "Fork: "+boolLabel(repo.Fork))

	parts := []string{strings.Join(lines, "\n")}
	if strings.TrimSpace(readme) != "" {
		parts = append(parts, "README:\n"+strings.TrimSpace(readme))
	}
	return strings.Join(parts, "\n\n")
}

func homepageSourceCandidate(repo repository) (model.SourceCandidate, bool) {
	homepage := strings.TrimSpace(repo.Homepage)
	if homepage == "" {
		return model.SourceCandidate{}, false
	}
	return linkextract.NormalizeCandidate(homepage)
}

func repoSourceCandidate(repo repository) model.SourceCandidate {
	canonical := strings.TrimSpace(repo.HTMLURL)
	return model.SourceCandidate{
		OriginalURL:   canonical,
		CanonicalURL:  canonical,
		NormalizedURL: canonical,
		SourceType:    "github",
		Domain:        "github.com",
		SourceKey:     "src:" + shortHash(canonical),
		NotePath:      vault.SourceNoteRelativePath("github", repoNoteSlug(repo.FullName)),
	}
}

func decodeReadme(readme readmePayload) (string, error) {
	if strings.TrimSpace(readme.Content) == "" {
		return "", nil
	}
	if strings.TrimSpace(strings.ToLower(readme.Encoding)) != "base64" {
		return "", fmt.Errorf("unsupported github readme encoding %q", readme.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(readme.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decode github readme: %w", err)
	}
	return strings.TrimSpace(string(decoded)), nil
}

func (c *client) getJSON(ctx context.Context, path string, accept string, target any) error {
	req, err := c.request(ctx, http.MethodGet, path, accept)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}

func (c *client) request(ctx context.Context, method string, path string, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "dbrain")
	return req, nil
}

func normalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339)
}

func chooseYear(values ...string) string {
	for _, value := range values {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
		if err == nil {
			return fmt.Sprintf("%04d", parsed.UTC().Year())
		}
	}
	return "unknown"
}

func itemNoteID(fullName string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(fullName)), "/", "__")
}

func repoNoteSlug(fullName string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(fullName)), "/", "-")
}

func shortHash(value string) string {
	return hashText(value)[:12]
}

func hashText(value string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, strings.TrimSpace(value))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func mapKeys(values map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func uniqueSorted(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
