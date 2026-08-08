package bskyapi

import (
	"strings"
	"testing"
)

func TestParsePersistedSessionSelectsCurrentAccount(t *testing.T) {
	raw := []byte(`{
  "session": {
    "accounts": [
      {
        "service": "https://bsky.social",
        "did": "did:plc:other",
        "handle": "other.example",
        "accessJwt": "other-access",
        "refreshJwt": "other-refresh"
      },
      {
        "service": "https://bsky.social",
        "did": "did:plc:current",
        "handle": "darron.example",
        "pdsUrl": "https://pds.example",
        "accessJwt": "current-access",
        "refreshJwt": "current-refresh"
      }
    ],
    "currentAccount": {"did": "did:plc:current"}
  }
}`)

	got, err := parsePersistedSession(raw)
	if err != nil {
		t.Fatalf("parsePersistedSession: %v", err)
	}
	if got.DID != "did:plc:current" {
		t.Fatalf("DID = %q, want current account", got.DID)
	}
	if got.Service != "https://bsky.social" {
		t.Fatalf("service = %q, want https://bsky.social", got.Service)
	}
	if got.PDSURL != "https://pds.example" {
		t.Fatalf("PDSURL = %q, want persisted pdsUrl", got.PDSURL)
	}
	if got.AccessJWT != "current-access" || got.RefreshJWT != "current-refresh" {
		t.Fatalf("selected the wrong account tokens")
	}
}

func TestParsePersistedSessionRejectsAmbiguousAccounts(t *testing.T) {
	raw := []byte(`{
  "session": {
    "accounts": [
      {"service": "https://bsky.social", "did": "did:plc:one", "handle": "one.example"},
      {"service": "https://bsky.social", "did": "did:plc:two", "handle": "two.example"}
    ]
  }
}`)

	if _, err := parsePersistedSession(raw); err == nil {
		t.Fatal("expected ambiguous account error")
	}
}

func TestNoteIDForPostURIIsFilesystemSafeAndURIUnique(t *testing.T) {
	first, err := noteIDForPostURI("at://did:plc:one/app.bsky.feed.post/3lq7example")
	if err != nil {
		t.Fatalf("noteIDForPostURI: %v", err)
	}
	if first == "" || strings.ContainsAny(first, `/\\:`) {
		t.Fatalf("note ID %q is not filesystem-safe", first)
	}
	if !strings.Contains(first, "3lq7example") {
		t.Fatalf("note ID %q does not retain the post rkey", first)
	}

	second, err := noteIDForPostURI("at://did:plc:two/app.bsky.feed.post/3lq7example")
	if err != nil {
		t.Fatalf("noteIDForPostURI second URI: %v", err)
	}
	if first == second {
		t.Fatalf("different AT URIs produced the same note ID %q", first)
	}
}
