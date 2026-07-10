package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
)

func TestResolveSyncAllFlagsUsesSharedImportPolicy(t *testing.T) {
	clearSyncImportPolicyEnv(t)
	root := t.TempDir()
	writeSyncImportPolicyConfig(t, root, `
sync_all:
  browser: firefox
  profile: research
  imports:
    x_bookmarks: false
    github_stars: false
    youtube_watch_later: true
    youtube_liked: false
    feeds: false
    apple_notes: true
    safari_tabs: false
`)

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{
		watchLater: true,
		liked:      true,
	})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if !resolved.skipXBookmarks || !resolved.skipX || !resolved.skipXMedia || !resolved.skipXPhotoOCR {
		t.Fatalf("disabled X selection should skip the full X family: %+v", resolved)
	}
	if !resolved.skipGitHub || !resolved.skipFeeds {
		t.Fatalf("disabled GitHub/feed selections were not applied: %+v", resolved)
	}
	if resolved.skipYouTube || !resolved.watchLater || resolved.liked {
		t.Fatalf("YouTube sub-selections were not applied: %+v", resolved)
	}
	if !resolved.appleNotes || resolved.safariTabs {
		t.Fatalf("Apple Notes/Safari selections were not applied: %+v", resolved)
	}
	if resolved.browser != "firefox" || resolved.profile != "research" {
		t.Fatalf("shared browser/profile were not applied: %+v", resolved)
	}
}

func TestResolveSyncAllFlagsKeepsLegacyDefaultsWithoutSharedPolicy(t *testing.T) {
	clearSyncImportPolicyEnv(t)
	root := t.TempDir()
	writeSyncImportPolicyConfig(t, root, `
apple_notes:
  enabled: true
safari_tabs:
  enabled: false
`)

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if resolved.skipXBookmarks || resolved.skipGitHub || resolved.skipYouTube || resolved.skipFeeds {
		t.Fatalf("legacy default-on imports should remain enabled: %+v", resolved)
	}
	if !resolved.watchLater || !resolved.liked || !resolved.appleNotes || resolved.safariTabs {
		t.Fatalf("legacy YouTube/local import defaults changed: %+v", resolved)
	}
}

func TestResolveSyncAllFlagsHonorsExplicitCLIOverrides(t *testing.T) {
	clearSyncImportPolicyEnv(t)
	root := t.TempDir()
	writeSyncImportPolicyConfig(t, root, `
sync_all:
  browser: firefox
  profile: configured
  imports:
    x_bookmarks: false
    github_stars: false
    youtube_watch_later: false
    youtube_liked: false
    feeds: false
    apple_notes: false
    safari_tabs: false
`)

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{
		watchLater: true,
		liked:      false,
		appleNotes: true,
		browser:    "safari",
		profile:    "explicit",
	}, syncAllFlagOverrides{
		skipXBookmarks: true,
		skipX:          true,
		skipXMedia:     true,
		skipXPhotoOCR:  true,
		skipGitHub:     true,
		skipYouTube:    true,
		skipFeeds:      true,
		watchLater:     true,
		liked:          true,
		appleNotes:     true,
		browser:        true,
		profile:        true,
	})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if resolved.skipXBookmarks || resolved.skipX || resolved.skipXMedia || resolved.skipXPhotoOCR || resolved.skipGitHub || resolved.skipYouTube || resolved.skipFeeds {
		t.Fatalf("explicit false skip flags should override disabled config: %+v", resolved)
	}
	if !resolved.watchLater || resolved.liked || !resolved.appleNotes {
		t.Fatalf("explicit source flags should override shared config: %+v", resolved)
	}
	if resolved.browser != "safari" || resolved.profile != "explicit" {
		t.Fatalf("explicit browser/profile should override shared config: %+v", resolved)
	}
}

func TestSyncAllImportEnvironmentOverridesConfig(t *testing.T) {
	clearSyncImportPolicyEnv(t)
	root := t.TempDir()
	writeSyncImportPolicyConfig(t, root, `
sync_all:
  imports:
    github_stars: false
`)
	t.Setenv(syncImportGitHubStarsKey, "true")

	policy := syncAllImportPolicyFromRuntime(root)
	if !policy.GitHubStars {
		t.Fatal("environment should override config for GitHub stars")
	}
}

func TestSyncAllLocalImportSkipFlagsOverrideSharedPolicy(t *testing.T) {
	clearSyncImportPolicyEnv(t)
	root := t.TempDir()
	writeSyncImportPolicyConfig(t, root, `
sync_all:
  imports:
    apple_notes: true
    safari_tabs: true
`)

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{
		skipAppleNotes: true,
		skipSafariTabs: true,
		skipGitHub:     true,
		skipXPhotoOCR:  true,
		skipCategorize: true,
		skipSources:    true,
	})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if !resolved.appleNotes || !resolved.safariTabs || !resolved.skipAppleNotes || !resolved.skipSafariTabs {
		t.Fatalf("shared policy and explicit skip flags were not both preserved: %+v", resolved)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	opts, err := syncOptionsFromFlags(context.Background(), cfg, resolved, nil, io.Discard)
	if err != nil {
		t.Fatalf("syncOptionsFromFlags: %v", err)
	}
	if opts.AppleNotesEnabled || opts.SafariTabsEnabled {
		t.Fatalf("explicit skip flags should disable policy-enabled local imports: %+v", opts)
	}
}

func clearSyncImportPolicyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		syncImportXBookmarksKey,
		syncImportGitHubStarsKey,
		syncImportYouTubeWatchLaterKey,
		syncImportYouTubeLikedKey,
		syncImportFeedsKey,
		syncImportAppleNotesKey,
		syncImportSafariTabsKey,
		"DBRAIN_APPLE_NOTES_ENABLED",
		"DBRAIN_SAFARI_TABS_ENABLED",
		"DBRAIN_SYNC_ALL_BROWSER",
		"DBRAIN_SYNC_ALL_PROFILE",
	} {
		t.Setenv(key, "")
	}
}

func writeSyncImportPolicyConfig(t *testing.T, root string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
