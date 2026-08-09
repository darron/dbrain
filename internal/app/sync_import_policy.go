package app

import "github.com/darron/dbrain/internal/runtimeenv"

const (
	syncImportXBookmarksKey        = "DBRAIN_SYNC_ALL_IMPORT_X_BOOKMARKS"
	syncImportGitHubStarsKey       = "DBRAIN_SYNC_ALL_IMPORT_GITHUB_STARS"
	syncImportYouTubeWatchLaterKey = "DBRAIN_SYNC_ALL_IMPORT_YOUTUBE_WATCH_LATER"
	syncImportYouTubeLikedKey      = "DBRAIN_SYNC_ALL_IMPORT_YOUTUBE_LIKED"
	syncImportFeedsKey             = "DBRAIN_SYNC_ALL_IMPORT_FEEDS"
	syncImportAppleNotesKey        = "DBRAIN_SYNC_ALL_IMPORT_APPLE_NOTES"
	syncImportSafariTabsKey        = "DBRAIN_SYNC_ALL_IMPORT_SAFARI_TABS"
	syncImportBlueskyBookmarksKey  = "DBRAIN_SYNC_ALL_IMPORT_BLUESKY_BOOKMARKS"
	syncImportMastodonBookmarksKey = "DBRAIN_SYNC_ALL_IMPORT_MASTODON_BOOKMARKS"
)

type syncAllImportPolicy struct {
	XBookmarks        bool
	GitHubStars       bool
	YouTubeWatchLater bool
	YouTubeLiked      bool
	Feeds             bool
	AppleNotes        bool
	SafariTabs        bool
	BlueskyBookmarks  bool
	MastodonBookmarks bool
}

type syncAllFlagOverrides struct {
	skipXBookmarks    bool
	skipX             bool
	skipXMedia        bool
	skipXPhotoOCR     bool
	skipGitHub        bool
	skipYouTube       bool
	skipFeeds         bool
	skipCategorize    bool
	watchLater        bool
	liked             bool
	appleNotes        bool
	safariTabs        bool
	blueskyBookmarks  bool
	mastodonBookmarks bool
	browser           bool
	profile           bool
}

func defaultSyncAllImportPolicy(rootDir string) syncAllImportPolicy {
	return syncAllImportPolicy{
		XBookmarks:        true,
		GitHubStars:       true,
		YouTubeWatchLater: true,
		YouTubeLiked:      true,
		Feeds:             true,
		AppleNotes:        runtimeenv.FirstBool(rootDir, "DBRAIN_APPLE_NOTES_ENABLED"),
		SafariTabs:        runtimeenv.FirstBool(rootDir, "DBRAIN_SAFARI_TABS_ENABLED"),
		BlueskyBookmarks:  false,
		MastodonBookmarks: false,
	}
}

func syncAllImportPolicyFromRuntime(rootDir string) syncAllImportPolicy {
	policy := defaultSyncAllImportPolicy(rootDir)
	applySyncImportBool(rootDir, syncImportXBookmarksKey, &policy.XBookmarks)
	applySyncImportBool(rootDir, syncImportGitHubStarsKey, &policy.GitHubStars)
	applySyncImportBool(rootDir, syncImportYouTubeWatchLaterKey, &policy.YouTubeWatchLater)
	applySyncImportBool(rootDir, syncImportYouTubeLikedKey, &policy.YouTubeLiked)
	applySyncImportBool(rootDir, syncImportFeedsKey, &policy.Feeds)
	applySyncImportBool(rootDir, syncImportAppleNotesKey, &policy.AppleNotes)
	applySyncImportBool(rootDir, syncImportSafariTabsKey, &policy.SafariTabs)
	applySyncImportBool(rootDir, syncImportBlueskyBookmarksKey, &policy.BlueskyBookmarks)
	applySyncImportBool(rootDir, syncImportMastodonBookmarksKey, &policy.MastodonBookmarks)
	return policy
}

func applySyncImportBool(rootDir string, key string, target *bool) {
	if value, ok := runtimeenv.LookupBool(rootDir, key); ok {
		*target = value
	}
}

func applySyncAllImportPolicy(flags syncAllFlags, policy syncAllImportPolicy, overrides syncAllFlagOverrides) syncAllFlags {
	if !overrides.skipXBookmarks {
		flags.skipXBookmarks = flags.skipXBookmarks || !policy.XBookmarks
	}
	if !overrides.skipX {
		flags.skipX = flags.skipX || !policy.XBookmarks
	}
	if !overrides.skipGitHub {
		flags.skipGitHub = flags.skipGitHub || !policy.GitHubStars
	}
	if !overrides.watchLater {
		flags.watchLater = policy.YouTubeWatchLater
	}
	if !overrides.liked {
		flags.liked = policy.YouTubeLiked
	}
	if !overrides.skipYouTube {
		flags.skipYouTube = flags.skipYouTube || (!flags.watchLater && !flags.liked)
	}
	if !overrides.skipFeeds {
		flags.skipFeeds = flags.skipFeeds || !policy.Feeds
	}
	if !overrides.appleNotes {
		flags.appleNotes = flags.appleNotes || policy.AppleNotes
	}
	if !overrides.safariTabs {
		flags.safariTabs = flags.safariTabs || policy.SafariTabs
	}
	if !overrides.blueskyBookmarks {
		flags.blueskyBookmarks = flags.blueskyBookmarks || policy.BlueskyBookmarks
	}
	if !overrides.mastodonBookmarks {
		flags.mastodonBookmarks = flags.mastodonBookmarks || policy.MastodonBookmarks
	}
	// Compute shared enrichment from the final effective source selection.
	// This includes explicit CLI flags and scheduler-provided Mastodon/Bluesky
	// selections, which were not represented by policy alone before A2.
	sharedBookmarkMedia := policy.XBookmarks || flags.blueskyBookmarks || flags.mastodonBookmarks
	if !overrides.skipXMedia {
		flags.skipXMedia = flags.skipXMedia || !sharedBookmarkMedia
	}
	if !overrides.skipXPhotoOCR {
		flags.skipXPhotoOCR = flags.skipXPhotoOCR || !sharedBookmarkMedia
	}
	return flags
}
