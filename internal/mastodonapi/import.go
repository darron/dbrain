package mastodonapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

type BookmarkOptions struct {
	AccountKey string
	Limit      int
	Timeout    time.Duration
	MaxPages   int
	// MediaRetryLimit bounds the post-import sweep that retries historical
	// Mastodon media failures. Zero uses the package default.
	MediaRetryLimit int
	Force           bool
	MediaHTTPPolicy *safehttp.Policy
	Now             func() time.Time
	// afterCheckpointHook is a deterministic concurrency-test seam. It runs
	// after a checkpoint has committed and before the importer requests the
	// next page; production callers leave it nil.
	afterCheckpointHook func(store.MastodonSyncState)
}

type BookmarkStats struct {
	AccountKey         string `json:"account_key"`
	Origin             string `json:"origin"`
	VerifiedAccountID  string `json:"verified_account_id"`
	Handle             string `json:"handle"`
	PagesFetched       int    `json:"pages_fetched"`
	Seen               int    `json:"seen"`
	Processed          int    `json:"processed"`
	Skipped            int    `json:"skipped"`
	SkippedUnsupported int    `json:"skipped_unsupported"`
	SkippedMalformed   int    `json:"skipped_malformed"`
	Created            int    `json:"created"`
	Updated            int    `json:"updated"`
	Unchanged          int    `json:"unchanged"`
	Rendered           int    `json:"rendered"`
	MediaDiscovered    int    `json:"media_discovered"`
	MediaLinked        int    `json:"media_linked"`
	MediaUnavailable   int    `json:"media_unavailable"`
	MediaDownloaded    int    `json:"media_downloaded"`
	MediaGone          int    `json:"media_gone"`
	MediaErrors        int    `json:"media_errors"`
	MediaBlocked       int    `json:"media_blocked"`
	APIErrors          int    `json:"api_errors"`
	RateLimits         int    `json:"rate_limits"`
	Retries            int    `json:"retries"`
	StoppedReason      string `json:"stopped_reason"`
}

const defaultMastodonMediaRetryLimit = 100

// StaleCursorError means that a previously persisted Link cursor is no longer
// accepted by the instance. The importer restarts from the endpoint head and
// keeps local items append-only.
type StaleCursorError struct {
	StatusCode int
	Reason     string
}

func (e *StaleCursorError) Error() string {
	if e.Reason != "" {
		if e.StatusCode != 0 {
			return fmt.Sprintf("Mastodon bookmarks cursor is stale (HTTP %d): %s", e.StatusCode, e.Reason)
		}
		return "Mastodon bookmarks cursor is invalid: " + e.Reason
	}
	if e.StatusCode == 0 {
		return "Mastodon bookmarks cursor is stale"
	}
	return fmt.Sprintf("Mastodon bookmarks cursor is stale (HTTP %d)", e.StatusCode)
}

// RunBookmarksWithClient imports one configured account. The caller resolves
// credentials and constructs the origin-scoped API client; this keeps token
// lookup out of the protocol package's persistence and test fixtures.
func RunBookmarksWithClient(ctx context.Context, cfg config.Config, st *store.Store, client *Client, opts BookmarkOptions) (BookmarkStats, error) {
	if client == nil {
		return BookmarkStats{}, fmt.Errorf("mastodon client is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	client = clientWithTimeout(client, opts.Timeout)
	if opts.MaxPages <= 0 {
		opts.MaxPages = 1000
	}
	if opts.MediaRetryLimit <= 0 {
		opts.MediaRetryLimit = defaultMastodonMediaRetryLimit
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, opts.Timeout)
	verified, err := client.VerifyCredentials(verifyCtx)
	verifyCancel()
	if err != nil {
		return BookmarkStats{}, fmt.Errorf("verify Mastodon credentials: %w", err)
	}
	accountKey := strings.TrimSpace(opts.AccountKey)
	if accountKey == "" {
		accountKey = strings.TrimSpace(verified.Acct)
		if accountKey == "" {
			accountKey = strings.TrimSpace(verified.Username)
		}
	}
	stats := BookmarkStats{AccountKey: accountKey, Origin: client.Origin, VerifiedAccountID: verified.ID, Handle: firstNonEmpty(verified.Acct, verified.Username)}
	state, err := st.GetMastodonSyncState(ctx, accountKey, client.Origin)
	if err != nil {
		return stats, err
	}
	if state != nil && state.VerifiedAccountID != "" && state.VerifiedAccountID != verified.ID {
		return stats, fmt.Errorf("mastodon account %q identity changed from %q to %q; refusing to reuse its bookmark state", accountKey, state.VerifiedAccountID, verified.ID)
	}
	otherState, err := st.GetMastodonSyncStateByVerifiedAccount(ctx, client.Origin, verified.ID)
	if err != nil {
		return stats, err
	}
	if otherState != nil && otherState.AccountKey != accountKey {
		return stats, fmt.Errorf("mastodon verified account %q on %s is already configured as %q; refusing account-key alias %q", verified.ID, client.Origin, otherState.AccountKey, accountKey)
	}
	if opts.Force && state != nil {
		state, err = st.ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx, state)
		if err != nil {
			return stats, err
		}
	}
	cursor := ""
	incrementalMode := false
	backfillComplete := false
	if state != nil {
		incrementalMode = state.BackfillComplete || state.BackfillIncremental
		if state.BackfillIncremental {
			// A limited incremental run owns the exact page and offset it
			// stopped on. Re-fetch that page so consecutive limited runs do
			// not replay its first status forever.
			cursor = firstNonEmpty(state.BackfillPageURL, state.BackfillNextURL)
		} else if state.BackfillComplete && state.BackfillNextURL != "" {
			// A full incremental page is checkpointed as complete so normal
			// runs can stop at the overlap boundary. If the process stops
			// before requesting the next page, resume that cursor instead of
			// replaying the head page and stopping at the overlap boundary.
			cursor = state.BackfillNextURL
		} else if !state.BackfillComplete {
			cursor = state.BackfillNextURL
		}
	}
	seenCursors := map[string]struct{}{}
	staleCursorRecoveryUsed := false
	renderer := projection.NewRenderer(cfg, st)
	for {
		if stats.PagesFetched >= opts.MaxPages {
			return stats, recordMastodonImportError(ctx, st, state, accountKey, client.Origin, verified, now(), fmt.Errorf("mastodon bookmark page limit %d reached", opts.MaxPages))
		}
		if cursor != "" {
			if _, ok := seenCursors[cursor]; ok {
				return stats, recordMastodonImportError(ctx, st, state, accountKey, client.Origin, verified, now(), fmt.Errorf("repeated Mastodon bookmarks cursor %q", cursor))
			}
			seenCursors[cursor] = struct{}{}
		}
		page, fetchStats, err := fetchMastodonBookmarksPageWithStats(ctx, client, cursor, 40, opts.Timeout)
		stats.APIErrors += fetchStats.APIErrors
		stats.RateLimits += fetchStats.RateLimits
		stats.Retries += fetchStats.Retries
		if err != nil {
			var stale *StaleCursorError
			if cursor != "" && errors.As(err, &stale) {
				if staleCursorRecoveryUsed {
					resetState, resetErr := st.ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx, state)
					if resetErr != nil {
						return stats, fmt.Errorf("clear rejected Mastodon recovery cursor identity/state: %w", resetErr)
					}
					return stats, recordMastodonImportError(ctx, st, resetState, accountKey, client.Origin, verified, now(), fmt.Errorf("mastodon bookmarks cursor remained stale after recovery: %w", err))
				}
				staleCursorRecoveryUsed = true
				resetState, resetErr := st.ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx, state)
				if resetErr != nil {
					return stats, fmt.Errorf("reset stale Mastodon cursor identity/state: %w", resetErr)
				}
				state = resetState
				cursor = ""
				incrementalMode = false
				seenCursors = map[string]struct{}{}
				continue
			}
			return stats, recordMastodonImportError(ctx, st, state, accountKey, client.Origin, verified, now(), err)
		}
		stats.PagesFetched++
		if page.NextURL != "" && (page.NextURL == cursor || hasSeenCursor(seenCursors, page.NextURL)) {
			return stats, recordMastodonImportError(ctx, st, state, accountKey, client.Origin, verified, now(), fmt.Errorf("repeated Mastodon bookmarks cursor %q", page.NextURL))
		}
		if len(page.Statuses) == 0 {
			preserveIncremental := incrementalMode
			backfillComplete = preserveIncremental || page.NextURL == ""
			incrementalMode = preserveIncremental && page.NextURL != ""
			state, err = checkpointMastodonState(ctx, st, state, accountKey, client.Origin, verified, page, backfillComplete, false, page.NextURL, 0, now())
			if err != nil {
				return stats, err
			}
			if opts.afterCheckpointHook != nil {
				opts.afterCheckpointHook(*state)
			}
			if page.NextURL == "" {
				stats.StoppedReason = "empty page"
				break
			}
			cursor = page.NextURL
			continue
		}
		pageComplete := true
		allPreviouslyPresent := incrementalMode
		newOnPage := false
		resumeOffset := 0
		if state != nil && state.BackfillPageOffset > 0 && state.BackfillPageURL == page.URL && state.BackfillPageDigest == page.PageDigest {
			resumeOffset = state.BackfillPageOffset
		}
		pageOffset := resumeOffset
		for index, remoteStatus := range page.Statuses {
			if index < resumeOffset {
				continue
			}
			if opts.Limit > 0 && stats.Seen >= opts.Limit {
				pageComplete = false
				stats.StoppedReason = "limit reached"
				break
			}
			pageOffset = index + 1
			stats.Seen++
			projection, normalizeErr := NormalizeStatusForAccount(remoteStatus, client.Origin, verified.ID, now())
			if normalizeErr != nil {
				if errors.Is(normalizeErr, ErrUnsupportedStatus) {
					// A skipped status is not evidence that this page is wholly
					// covered by the local overlap boundary. Continue beyond it.
					allPreviouslyPresent = false
					stats.Skipped++
					stats.SkippedUnsupported++
					continue
				}
				if errors.Is(normalizeErr, ErrMalformedStatus) {
					allPreviouslyPresent = false
					stats.Skipped++
					stats.SkippedMalformed++
					continue
				}
				return stats, fmt.Errorf("normalize Mastodon status: %w", normalizeErr)
			}
			if _, getErr := st.GetItem(ctx, projection.Item.SourceKey); getErr != nil {
				if errors.Is(getErr, store.ErrItemNotFound) {
					allPreviouslyPresent = false
					newOnPage = true
				} else {
					return stats, getErr
				}
			}
			result, upsertErr := st.UpsertItem(ctx, projection.Item)
			if upsertErr != nil {
				return stats, fmt.Errorf("upsert Mastodon status %s: %w", projection.Item.ExternalID, upsertErr)
			}
			stats.Processed++
			switch result.Status {
			case model.UpsertCreated:
				stats.Created++
			case model.UpsertUpdated:
				stats.Updated++
			case model.UpsertUnchanged:
				stats.Unchanged++
			}
			stats.MediaDiscovered += len(projection.MediaCandidates)
			if projection.MediaUnavailable {
				stats.MediaUnavailable++
			}
			mediaChanged := false
			if projection.MediaComplete {
				mediaChanged, err = st.SaveItemMediaCandidates(ctx, result.ItemID, projection.MediaCandidates)
				if err != nil {
					return stats, fmt.Errorf("save Mastodon media %s: %w", projection.Item.ExternalID, err)
				}
			} else if len(projection.MediaCandidates) > 0 {
				mediaChanged, err = st.MergeItemMediaCandidates(ctx, result.ItemID, projection.MediaCandidates)
				if err != nil {
					return stats, fmt.Errorf("merge Mastodon media %s: %w", projection.Item.ExternalID, err)
				}
			}
			if mediaChanged {
				stats.MediaLinked++
			}
			downloadStats, downloadErr := downloadMastodonItemMedia(ctx, cfg, st, result.ItemID, projection.Item.SourceType, projection.MediaCandidates, opts.MediaHTTPPolicy)
			if downloadErr != nil {
				return stats, downloadErr
			}
			stats.MediaDownloaded += downloadStats.Downloaded
			stats.MediaGone += downloadStats.Gone
			stats.MediaErrors += downloadStats.Errors
			stats.MediaBlocked += downloadStats.Blocked
			shouldRender := result.Status != model.UpsertUnchanged || mediaChanged || downloadStats.Changed > 0
			if !shouldRender {
				if _, statErr := vault.StatNote(cfg, projection.Item.NotePath); statErr != nil {
					shouldRender = true
				}
			}
			if shouldRender {
				if _, renderErr := renderer.RefreshItem(ctx, projection.Item.SourceKey); renderErr != nil {
					return stats, fmt.Errorf("render Mastodon status %s: %w", projection.Item.ExternalID, renderErr)
				}
				stats.Rendered++
			}
		}
		if !pageComplete {
			state, err = checkpointMastodonState(ctx, st, state, accountKey, client.Origin, verified, page, false, incrementalMode, cursor, pageOffset, now())
			if err != nil {
				return stats, err
			}
			if opts.afterCheckpointHook != nil {
				opts.afterCheckpointHook(*state)
			}
			break
		}
		// A resumed page only proves overlap for the suffix processed during
		// this invocation. Its skipped prefix may contain a newly inserted
		// bookmark, so conservatively request the next page rather than
		// terminating at this page's apparent overlap.
		if incrementalMode && resumeOffset == 0 && !newOnPage && allPreviouslyPresent {
			stats.StoppedReason = "overlap page reached"
			state, err = checkpointMastodonState(ctx, st, state, accountKey, client.Origin, verified, page, true, false, "", 0, now())
			if err != nil {
				return stats, err
			}
			if opts.afterCheckpointHook != nil {
				opts.afterCheckpointHook(*state)
			}
			break
		}
		cursor = page.NextURL
		if incrementalMode {
			backfillComplete = true
		} else {
			backfillComplete = cursor == ""
		}
		state, err = checkpointMastodonState(ctx, st, state, accountKey, client.Origin, verified, page, backfillComplete, false, page.NextURL, 0, now())
		if err != nil {
			return stats, err
		}
		if opts.afterCheckpointHook != nil {
			opts.afterCheckpointHook(*state)
		}
		if cursor == "" {
			stats.StoppedReason = "backfill complete"
			break
		}
	}
	if stats.StoppedReason == "limit reached" {
		return stats, nil
	}
	mediaStats, mediaErr := retryMastodonMedia(ctx, cfg, st, opts.MediaRetryLimit, opts.MediaHTTPPolicy)
	if mediaErr != nil {
		return stats, mediaErr
	}
	stats.MediaDownloaded += mediaStats.Downloaded
	stats.MediaGone += mediaStats.Gone
	stats.MediaErrors += mediaStats.Errors
	stats.MediaBlocked += mediaStats.Blocked
	return stats, nil
}

func checkpointMastodonState(ctx context.Context, st *store.Store, previous *store.MastodonSyncState, accountKey, origin string, verified VerifiedAccount, page BookmarksPage, complete, incremental bool, nextURL string, pageOffset int, now time.Time) (*store.MastodonSyncState, error) {
	state := store.MastodonSyncState{AccountKey: accountKey, CanonicalOrigin: origin, VerifiedAccountID: verified.ID, Handle: firstNonEmpty(verified.Acct, verified.Username), BackfillNextURL: nextURL, BackfillComplete: complete, BackfillIncremental: incremental && !complete, BackfillPageOffset: pageOffset, LastSuccessAt: now}
	if previous != nil {
		state.CapabilitiesJSON = previous.CapabilitiesJSON
		state.LastPageURL = previous.LastPageURL
		state.LastPageDigest = previous.LastPageDigest
		if !complete {
			state.LastSuccessAt = previous.LastSuccessAt
		}
	}
	if !complete {
		state.BackfillPageURL = page.URL
		state.BackfillPageDigest = page.PageDigest
	} else {
		state.LastPageURL = page.URL
		state.LastPageDigest = page.PageDigest
	}
	if err := st.UpsertMastodonSyncStateIfCurrent(ctx, state, previous); err != nil {
		return nil, err
	}
	return &state, nil
}

func recordMastodonImportError(ctx context.Context, st *store.Store, previous *store.MastodonSyncState, accountKey, origin string, verified VerifiedAccount, now time.Time, cause error) error {
	redacted := redactMastodonError(cause)
	if err := st.RecordMastodonSyncErrorIfCurrent(ctx, accountKey, origin, verified.ID, now.UTC(), redacted, previous); err != nil {
		return fmt.Errorf("mastodon import failed (%s), and saving state failed: %w", redacted, err)
	}
	return cause
}

type mastodonPageFetchStats struct {
	APIErrors  int
	RateLimits int
	Retries    int
}

func fetchMastodonBookmarksPageWithStats(ctx context.Context, client *Client, cursor string, limit int, timeout time.Duration) (BookmarksPage, mastodonPageFetchStats, error) {
	var stats mastodonPageFetchStats
	for attempt := 0; attempt < 2; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		page, err := client.FetchBookmarksPage(requestCtx, cursor, limit)
		cancel()
		if err == nil {
			return page, stats, nil
		}
		stats.APIErrors++
		var retryable *RetryableError
		if errors.As(err, &retryable) && retryable.StatusCode == 429 {
			stats.RateLimits++
		}
		if !errors.As(err, &retryable) || retryable.RetryAfter <= 0 || attempt > 0 {
			return BookmarksPage{}, stats, err
		}
		stats.Retries++
		timer := time.NewTimer(retryable.RetryAfter)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return BookmarksPage{}, stats, ctx.Err()
		case <-timer.C:
		}
	}
	return BookmarksPage{}, stats, fmt.Errorf("mastodon bookmarks retry limit reached")
}

func hasSeenCursor(seen map[string]struct{}, cursor string) bool {
	_, ok := seen[cursor]
	return ok
}

func redactMastodonError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	value = redactMastodonCredentialText(value)
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}

func retryMastodonMedia(ctx context.Context, cfg config.Config, st *store.Store, limit int, basePolicy *safehttp.Policy) (mediadownload.Stats, error) {
	refs, err := st.ListMastodonMediaRefsForDownload(ctx, limit, false)
	if err != nil {
		return mediadownload.Stats{}, err
	}
	var total mediadownload.Stats
	seenAssets := make(map[int64]struct{}, len(refs))
	for _, ref := range refs {
		if _, seen := seenAssets[ref.MediaAssetID]; seen {
			continue
		}
		seenAssets[ref.MediaAssetID] = struct{}{}
		policy, ok := mastodonMediaHTTPPolicy(ref.RemoteURL, basePolicy)
		if !ok {
			continue
		}
		stats, runErr := mediadownload.RunForItem(ctx, cfg, st, ref.ItemID, mediadownload.Options{
			MediaNamespace:  mediadownload.MediaNamespaceForSourceType("mastodon_bookmark"),
			AllowedAssetIDs: []int64{ref.MediaAssetID},
			HTTPPolicy:      &policy,
		})
		if runErr != nil {
			return total, fmt.Errorf("retry Mastodon media %s: %w", ref.RemoteURL, runErr)
		}
		total.Candidates += stats.Candidates
		total.Requested += stats.Requested
		total.Downloaded += stats.Downloaded
		total.Gone += stats.Gone
		total.Errors += stats.Errors
		total.Blocked += stats.Blocked
		total.Changed += stats.Changed
	}
	return total, nil
}

func downloadMastodonItemMedia(ctx context.Context, cfg config.Config, st *store.Store, itemID int64, sourceType string, candidates []model.MediaCandidate, basePolicy *safehttp.Policy) (mediadownload.Stats, error) {
	var total mediadownload.Stats
	for _, candidate := range candidates {
		policy, ok := mastodonMediaHTTPPolicy(candidate.RemoteURL, basePolicy)
		if !ok {
			continue
		}
		refs, err := st.ListItemMediaRefs(ctx, itemID)
		if err != nil {
			return total, err
		}
		for _, ref := range refs {
			if ref.RemoteURL != candidate.RemoteURL {
				continue
			}
			stats, runErr := mediadownload.RunForItem(ctx, cfg, st, itemID, mediadownload.Options{MediaNamespace: mediadownload.MediaNamespaceForSourceType(sourceType), AllowedAssetIDs: []int64{ref.MediaAssetID}, HTTPPolicy: &policy})
			if runErr != nil {
				return total, fmt.Errorf("download Mastodon media %s: %w", candidate.RemoteURL, runErr)
			}
			total.Candidates += stats.Candidates
			total.Requested += stats.Requested
			total.Downloaded += stats.Downloaded
			total.Gone += stats.Gone
			total.Errors += stats.Errors
			total.Blocked += stats.Blocked
			total.Changed += stats.Changed
		}
	}
	return total, nil
}

func mastodonMediaHTTPPolicy(rawURL string, basePolicy *safehttp.Policy) (safehttp.Policy, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return safehttp.Policy{}, false
	}
	origin, err := safehttp.CanonicalOriginEndpoint(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return safehttp.Policy{}, false
	}
	policy := safehttp.Policy{AllowedOrigins: []string{origin}, RejectCredentialQueryOnRedirect: true}
	if basePolicy != nil {
		policy = *basePolicy
		policy.AllowedOrigins = []string{origin}
		policy.RejectCredentialQueryOnRedirect = true
	}
	return policy, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
