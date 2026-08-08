package app

import "testing"

func TestImportBlueskyBookmarksCommandExposesProfileImportFlags(t *testing.T) {
	cmd := newImportBlueskyBookmarksCommand(&rootOptions{})
	for _, name := range []string{"browser", "profile", "limit", "page-size", "max-pages", "timeout", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag", name)
		}
	}
	if got, want := cmd.Use, "bookmarks"; got != want {
		t.Fatalf("Use = %q, want %q", got, want)
	}
}

func TestImportBlueskyCommandContainsBookmarksSubcommand(t *testing.T) {
	cmd := newImportBlueskyCommand(&rootOptions{})
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "bookmarks" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected bluesky bookmarks subcommand")
	}
}
