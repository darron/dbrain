package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	AuditIdentityMaxCount = 100_000
	auditIdentityDomainV1 = "dbrain.audit.identity.v1"
)

var auditIdentityHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AuditSource is a store-owned closed enum. Its values are the fixed local
// source_type populations used for parity matching; callers cannot provide a
// table, column, or predicate.
type AuditSource string

const (
	AuditSourceAppleNotes        AuditSource = "apple_note"
	AuditSourceSafariTabs        AuditSource = "safari_tab"
	AuditSourceXBookmarks        AuditSource = "x_bookmark"
	AuditSourceGitHubStars       AuditSource = "github_star"
	AuditSourceYouTubeLiked      AuditSource = "youtube_liked"
	AuditSourceYouTubeWatchLater AuditSource = "youtube_watch_later"
	AuditSourceFeeds             AuditSource = "feed_entry"
)

func (s AuditSource) valid() bool {
	switch s {
	case AuditSourceAppleNotes, AuditSourceSafariTabs, AuditSourceXBookmarks, AuditSourceGitHubStars,
		AuditSourceYouTubeLiked, AuditSourceYouTubeWatchLater, AuditSourceFeeds:
		return true
	default:
		return false
	}
}

func (s AuditSource) hashDomain() string {
	switch s {
	case AuditSourceAppleNotes:
		return "apple-notes"
	case AuditSourceSafariTabs:
		return "safari-tabs"
	case AuditSourceXBookmarks:
		return "x-bookmarks"
	case AuditSourceGitHubStars:
		return "github-stars"
	case AuditSourceYouTubeLiked:
		return "youtube-liked"
	case AuditSourceYouTubeWatchLater:
		return "youtube-watch-later"
	case AuditSourceFeeds:
		return "feeds"
	default:
		return ""
	}
}

// CountLocalIdentityMatches counts unique requested hashes represented by the
// query-only snapshot. Validation happens before selecting the fixed query, and
// local rows are streamed and hashed in Go; no 100,000-placeholder SQL is built.
func (s *AuditReadSnapshot) CountLocalIdentityMatches(ctx context.Context, source AuditSource, hashes []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !source.valid() {
		return 0, fmt.Errorf("unsupported audit source %q", source)
	}
	if len(hashes) > AuditIdentityMaxCount {
		return 0, fmt.Errorf("audit identity count exceeds %d", AuditIdentityMaxCount)
	}
	requested := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		if !auditIdentityHashPattern.MatchString(hash) {
			return 0, fmt.Errorf("invalid audit identity hash")
		}
		requested[hash] = struct{}{}
	}
	reader, err := s.query(ctx)
	if err != nil {
		return 0, err
	}
	if len(requested) == 0 {
		return 0, nil
	}
	matched := make(map[string]struct{}, len(requested))
	if source == AuditSourceFeeds {
		err = countFeedAuditIdentityMatches(ctx, reader, requested, matched)
	} else {
		err = countStaticAuditIdentityMatches(ctx, reader, source, requested, matched)
	}
	if err != nil {
		return 0, err
	}
	return len(matched), nil
}

func countStaticAuditIdentityMatches(ctx context.Context, reader sqlQueryer, source AuditSource, requested, matched map[string]struct{}) error {
	rows, err := reader.QueryContext(ctx, `SELECT source_key FROM items WHERE source_type = ? ORDER BY id ASC`, string(source))
	if err != nil {
		return fmt.Errorf("query local audit identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var identity string
		if err := rows.Scan(&identity); err != nil {
			return fmt.Errorf("scan local audit identity: %w", err)
		}
		identity = strings.TrimSpace(identity)
		if identity == "" {
			continue
		}
		markAuditIdentityMatch(hashAuditIdentity(source, identity), requested, matched)
		if len(matched) == len(requested) {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate local audit identities: %w", err)
	}
	return nil
}

func countFeedAuditIdentityMatches(ctx context.Context, reader sqlQueryer, requested, matched map[string]struct{}) error {
	rows, err := reader.QueryContext(ctx, `SELECT f.feed_key, e.identity_key, e.guid, e.normalized_link
		FROM feed_entries e JOIN feeds f ON f.id = e.feed_id ORDER BY e.id ASC`)
	if err != nil {
		return fmt.Errorf("query local feed audit identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var feedKey, identityKey, guid, normalizedLink string
		if err := rows.Scan(&feedKey, &identityKey, &guid, &normalizedLink); err != nil {
			return fmt.Errorf("scan local feed audit identity: %w", err)
		}
		feedKey = strings.TrimSpace(feedKey)
		if feedKey == "" || strings.ContainsRune(feedKey, '\x00') {
			continue
		}
		aliases := []string{strings.TrimSpace(identityKey)}
		if guid = strings.TrimSpace(guid); guid != "" {
			aliases = append(aliases, "guid:"+guid)
		}
		if normalizedLink = strings.TrimSpace(normalizedLink); normalizedLink != "" {
			aliases = append(aliases, "link:"+normalizedLink)
		}
		for _, alias := range aliases {
			if alias == "" || strings.ContainsRune(alias, '\x00') {
				continue
			}
			markAuditIdentityMatch(hashAuditFeedIdentity(feedKey, alias), requested, matched)
		}
		if len(matched) == len(requested) {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate local feed audit identities: %w", err)
	}
	return nil
}

func markAuditIdentityMatch(hash string, requested, matched map[string]struct{}) {
	if _, ok := requested[hash]; ok {
		matched[hash] = struct{}{}
	}
}

func hashAuditFeedIdentity(feedKey, identity string) string {
	return hashAuditIdentity(AuditSourceFeeds, strings.TrimSpace(feedKey)+"\x00"+strings.TrimSpace(identity))
}

func hashAuditIdentity(source AuditSource, identity string) string {
	digest := sha256.Sum256([]byte(auditIdentityDomainV1 + "\x00" + source.hashDomain() + "\x00" + strings.TrimSpace(identity)))
	return hex.EncodeToString(digest[:])
}
