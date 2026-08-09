package app

import "testing"

func TestImportMastodonBookmarksCommandExposesAccountImportFlags(t *testing.T) {
	cmd := newImportMastodonBookmarksCommand(&rootOptions{})
	for _, name := range []string{"account", "limit", "timeout", "force", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag", name)
		}
	}
}

func TestImportMastodonCommandContainsBookmarksSubcommand(t *testing.T) {
	cmd := newImportMastodonCommand(&rootOptions{})
	if _, _, err := cmd.Find([]string{"bookmarks"}); err != nil {
		t.Fatalf("find bookmarks command: %v", err)
	}
}
