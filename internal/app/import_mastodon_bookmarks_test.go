package app

import (
	"strings"
	"testing"
)

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

func TestMastodonForceHelpDocumentsTerminalMediaRecovery(t *testing.T) {
	for _, test := range []struct {
		name  string
		usage string
	}{
		{name: "direct import", usage: newImportMastodonBookmarksCommand(&rootOptions{}).Flag("force").Usage},
		{name: "sync all", usage: newSyncAllCommand(&rootOptions{}).Flag("force").Usage},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(test.usage, "terminal blocked Mastodon media") {
				t.Fatalf("--force help = %q, want terminal blocked Mastodon media recovery", test.usage)
			}
		})
	}
}
