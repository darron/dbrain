package githubimport

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
	"github.com/darron/dbrain/internal/version"
)

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
		token, err := runtimeenv.FirstNonEmptySecret(ctx, cfg.RootDir, "GITHUB_TOKEN")
		if err != nil {
			return Stats{}, err
		}
		opts.Token = token
	}
	if strings.TrimSpace(opts.Token) == "" {
		return Stats{}, fmt.Errorf("GITHUB_TOKEN is required")
	}
	if strings.TrimSpace(opts.UserAgent) == "" {
		opts.UserAgent = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_USER_AGENT")
	}

	c := &client{
		baseURL:   strings.TrimRight(opts.APIBase, "/"),
		token:     opts.Token,
		userAgent: version.UserAgent(opts.UserAgent),
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
	renderer := projection.NewRenderer(cfg, st)
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
				if _, err := renderer.RefreshItem(ctx, item.SourceKey); err != nil {
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
