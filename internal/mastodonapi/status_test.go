package mastodonapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeStatusRequiresParseableCreatedAt(t *testing.T) {
	for _, createdAt := range []string{"", "not-a-timestamp"} {
		t.Run(createdAt, func(t *testing.T) {
			_, err := NormalizeStatusForAccount(StatusRecord{
				ID:        "1",
				URI:       "https://example.com/users/a/statuses/1",
				URL:       "https://example.com/@a/1",
				Content:   "hello",
				CreatedAt: createdAt,
				Account:   Account{ID: "42", Username: "a"},
			}, "https://example.com", "42", time.Now())
			if !errors.Is(err, ErrMalformedStatus) {
				t.Fatalf("error = %v, want ErrMalformedStatus", err)
			}
		})
	}
}

func TestStrictMastodonTimestampRejectsRubyDate(t *testing.T) {
	rubyDate := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC).Format(time.RubyDate)
	if _, err := strictMastodonTimestamp(rubyDate); err == nil {
		t.Fatalf("strictMastodonTimestamp accepted RubyDate %q", rubyDate)
	}
}

func TestMastodonRedactionCoversCredentialVariantsWithoutRemovingState(t *testing.T) {
	value := map[string]any{
		"state":         "retain",
		"code_verifier": "verifier-secret",
		"password":      "password-secret",
		"nested": []any{
			map[string]any{"api_key": "api-secret", "ordinary": "retain"},
		},
		"url": "https://cdn.example/file?client_secret=url-secret&keep=value",
	}
	redactMastodonJSON(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal redacted value: %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{"verifier-secret", "password-secret", "api-secret", "url-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("redacted evidence retained %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "state") || !strings.Contains(text, "ordinary") || !strings.Contains(text, "keep=value") {
		t.Fatalf("redaction removed ordinary evidence: %s", text)
	}
	errText := redactMastodonError(errors.New(`request failed: Bearer bearer-secret access_token=token-secret client_secret=client-secret code_verifier=verifier-secret`))
	for _, secret := range []string{"bearer-secret", "token-secret", "client-secret", "verifier-secret"} {
		if strings.Contains(errText, secret) {
			t.Fatalf("redacted error retained %q: %s", secret, errText)
		}
	}
}

func TestSanitizedStatusJSONPreservesLargeIntegersAndRedactsArrayURLs(t *testing.T) {
	var status StatusRecord
	raw := `{"id":"large-1","uri":"https://example.com/users/a/statuses/large-1","url":"https://example.com/@a/large-1","content":"hello","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"a"},"extension":{"count":9007199254740993,"values":["https://cdn.example/file?access_token=array-secret&keep=value",{"url":"https://cdn.example/other?refresh_token=nested-secret&keep=ok"}]}}`
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	projection, err := NormalizeStatus(status, "https://example.com", time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeStatus: %v", err)
	}
	text := projection.Item.RawJSON
	if !strings.Contains(text, `"count":9007199254740993`) {
		t.Fatalf("large extension integer lost precision: %s", text)
	}
	for _, secret := range []string{"array-secret", "nested-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("array URL redaction retained %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "keep=value") || !strings.Contains(text, "keep=ok") {
		t.Fatalf("array URL redaction removed safe query evidence: %s", text)
	}
}

func TestNormalizeStatusSanitizesHTMLCollectsLinksAndAcceptsMediaOnly(t *testing.T) {
	status := StatusRecord{
		ID:               "109",
		URI:              "https://hachyderm.io/users/alice/statuses/109",
		URL:              "https://hachyderm.io/@alice/109",
		Content:          `<p>Hello&nbsp;<a href="https://example.com/a?x=1">world</a></p><script>alert(1)</script><p>next<br>line</p><a href="javascript:alert(1)">bad</a><a href="https://alice:secret@example.net/private">bad2</a>`,
		CreatedAt:        "2026-08-08T12:00:00.000Z",
		Account:          Account{ID: "42", Username: "alice", DisplayName: "Alice"},
		MediaAttachments: []MediaAttachment{{ID: "m1", Type: "image", URL: "https://cdn.example/image.jpg", RemoteURL: "https://cdn.example/image.jpg", Description: "a photo", Meta: map[string]any{"original": map[string]any{"width": 800, "height": 600}}}},
		Card:             &Card{URL: "https://example.org/card", Title: "Card title", Description: "Card description"},
		SpoilerText:      "spoiler",
		Visibility:       "private",
		Sensitive:        true,
	}

	projection, err := NormalizeStatus(status, "https://hachyderm.io", time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeStatus: %v", err)
	}
	if got, want := projection.Item.SourceType, "mastodon_bookmark"; got != want {
		t.Fatalf("source type = %q, want %q", got, want)
	}
	if got, want := projection.Item.SavedAt, ""; got != want {
		t.Fatalf("saved_at = %q, want empty", got)
	}
	if !strings.Contains(projection.Item.Text, "Hello world") || !strings.Contains(projection.Item.Text, "next\nline") {
		t.Fatalf("normalized text = %q", projection.Item.Text)
	}
	if strings.Contains(projection.Item.Text, "alert") || !strings.Contains(projection.Item.Text, "bad2") || !strings.Contains(projection.Item.Text, "bad") {
		t.Fatalf("unsafe HTML evidence was not normalized safely: %q", projection.Item.Text)
	}
	if !strings.Contains(projection.Item.Text, "spoiler") {
		t.Fatalf("spoiler text missing from normalized evidence: %q", projection.Item.Text)
	}
	if got, want := projection.Item.Title, "Hello world"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if !strings.Contains(projection.Item.LinksJSON, "https://example.com/a?x=1") || !strings.Contains(projection.Item.LinksJSON, "https://example.org/card") {
		t.Fatalf("links = %s", projection.Item.LinksJSON)
	}
	if strings.Contains(projection.Item.LinksJSON, "javascript") || strings.Contains(projection.Item.LinksJSON, "alice:secret") {
		t.Fatalf("unsafe links leaked: %s", projection.Item.LinksJSON)
	}
	if len(projection.MediaCandidates) != 1 || projection.MediaCandidates[0].MediaType != "photo" {
		t.Fatalf("media candidates = %#v", projection.MediaCandidates)
	}
	if projection.MediaCandidates[0].Width != 800 || projection.MediaCandidates[0].Height != 600 {
		t.Fatalf("media dimensions = %#v", projection.MediaCandidates[0])
	}
	if projection.Item.RawJSON == "" || strings.Contains(projection.Item.RawJSON, "token") {
		t.Fatalf("raw json was not sanitized: %q", projection.Item.RawJSON)
	}
}

func TestNormalizeStatusScopesNotePathsByOriginAndVerifiedAccount(t *testing.T) {
	status := StatusRecord{
		ID:        "local-1",
		URI:       "https://remote.example/users/author/statuses/1",
		URL:       "https://remote.example/@author/1",
		Content:   "same status",
		CreatedAt: "2026-08-08T12:00:00Z",
		Account:   Account{ID: "author-1", Username: "author"},
	}
	a, err := NormalizeStatusForAccount(status, "https://hachyderm.io", "42", time.Now())
	if err != nil {
		t.Fatalf("first projection: %v", err)
	}
	b, err := NormalizeStatusForAccount(status, "https://other.example", "42", time.Now())
	if err != nil {
		t.Fatalf("second projection: %v", err)
	}
	if a.Item.NotePath == b.Item.NotePath {
		t.Fatalf("account/origin-scoped items share note path %q", a.Item.NotePath)
	}
}

func TestNormalizeStatusDoesNotClearMediaWhenAttachmentProjectionIsIncomplete(t *testing.T) {
	projection, err := NormalizeStatus(StatusRecord{
		ID:        "3",
		URI:       "https://example.com/users/a/statuses/3",
		URL:       "https://example.com/@a/3",
		Content:   "caption",
		CreatedAt: "2026-08-08T12:00:00Z",
		Account:   Account{ID: "42", Username: "a"},
		MediaAttachments: []MediaAttachment{
			{ID: "known", Type: "image", URL: "https://cdn.example/known.jpg"},
			{ID: "unknown", Type: "future-type", URL: "https://cdn.example/future"},
		},
	}, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus: %v", err)
	}
	if projection.MediaComplete || !projection.MediaUnavailable || len(projection.MediaCandidates) != 1 {
		t.Fatalf("projection = %#v, want incomplete media projection", projection)
	}
}

func TestNormalizeStatusDistinguishesOmittedAndEmptyAttachmentProjections(t *testing.T) {
	base := `{"id":"3","uri":"https://example.com/users/a/statuses/3","url":"https://example.com/@a/3","content":"caption","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"a"}`
	var omitted, explicitEmpty StatusRecord
	if err := json.Unmarshal([]byte(base+`}`), &omitted); err != nil {
		t.Fatalf("unmarshal omitted media projection: %v", err)
	}
	if err := json.Unmarshal([]byte(base+`,"media_attachments":[]}`), &explicitEmpty); err != nil {
		t.Fatalf("unmarshal empty media projection: %v", err)
	}
	omittedProjection, err := NormalizeStatus(omitted, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus omitted: %v", err)
	}
	explicitProjection, err := NormalizeStatus(explicitEmpty, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus explicit empty: %v", err)
	}
	if omittedProjection.MediaComplete {
		t.Fatal("omitted media_attachments field was treated as authoritative")
	}
	if !explicitProjection.MediaComplete {
		t.Fatal("explicit empty media_attachments field was not treated as authoritative")
	}
}

func TestNormalizeStatusPreservesExplicitEmptyMediaPresenceInRawJSON(t *testing.T) {
	var status StatusRecord
	if err := json.Unmarshal([]byte(`{"id":"3","uri":"https://example.com/users/a/statuses/3","url":"https://example.com/@a/3","content":"caption","created_at":"2026-08-08T12:00:00Z","account":{"id":"42","username":"a"},"media_attachments":[]}`), &status); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	projection, err := NormalizeStatus(status, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus: %v", err)
	}
	if !strings.Contains(projection.Item.RawJSON, `"media_attachments":[]`) {
		t.Fatalf("raw JSON dropped explicit empty media_attachments: %s", projection.Item.RawJSON)
	}

	var replay StatusRecord
	if err := json.Unmarshal([]byte(projection.Item.RawJSON), &replay); err != nil {
		t.Fatalf("replay raw JSON: %v", err)
	}
	replayed, err := NormalizeStatus(replay, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus replay: %v", err)
	}
	if !replayed.MediaComplete {
		t.Fatalf("replayed explicit empty media projection was incomplete: %#v", replayed)
	}
}

func TestNormalizeStatusRejectsStatusWithoutSupportedContent(t *testing.T) {
	_, err := NormalizeStatus(StatusRecord{
		ID:        "1",
		URI:       "https://example.com/users/a/statuses/1",
		URL:       "https://example.com/@a/1",
		CreatedAt: "2026-08-08T12:00:00Z",
		Account:   Account{ID: "42", Username: "a"},
	}, "https://example.com", time.Now())
	if err == nil || !strings.Contains(err.Error(), "no supported content") {
		t.Fatalf("NormalizeStatus error = %v, want unsupported-content error", err)
	}
}

func TestNormalizeStatusRetainsMediaOnlyStatusWhenAttachmentCannotBeDownloaded(t *testing.T) {
	projection, err := NormalizeStatus(StatusRecord{
		ID:        "2",
		URI:       "https://example.com/users/a/statuses/2",
		URL:       "https://example.com/@a/2",
		CreatedAt: "2026-08-08T12:00:00Z",
		Account:   Account{ID: "42", Username: "a"},
		MediaAttachments: []MediaAttachment{{
			ID:   "unknown-1",
			Type: "unknown-extension",
			URL:  "https://cdn.example/attachment",
		}},
	}, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus: %v", err)
	}
	if !projection.MediaUnavailable || len(projection.MediaCandidates) != 0 {
		t.Fatalf("projection = %#v, want retained unavailable media-only status", projection)
	}
}

func TestNormalizeStatusDoesNotHashMutableEngagementCounts(t *testing.T) {
	base := StatusRecord{
		ID:              "1",
		URI:             "https://example.com/users/a/statuses/1",
		URL:             "https://example.com/@a/1",
		Content:         "stable",
		CreatedAt:       "2026-08-08T12:00:00Z",
		Account:         Account{ID: "42", Username: "a"},
		FavouritesCount: 1,
		ReblogsCount:    2,
		RepliesCount:    3,
		BookmarksCount:  4,
	}
	changed := base
	changed.FavouritesCount = 900
	changed.ReblogsCount = 901
	changed.RepliesCount = 902
	changed.BookmarksCount = 903
	a, err := NormalizeStatus(base, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus(base): %v", err)
	}
	b, err := NormalizeStatus(changed, "https://example.com", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatus(changed): %v", err)
	}
	if a.Item.ContentHash != b.Item.ContentHash {
		t.Fatalf("content hashes changed with engagement counts: %s != %s", a.Item.ContentHash, b.Item.ContentHash)
	}
}

func TestNormalizeStatusUpdatesIdentityHashWhenLocalStatusIDChanges(t *testing.T) {
	base := StatusRecord{
		ID:        "local-1",
		URI:       "https://remote.example/users/a/statuses/1",
		URL:       "https://remote.example/@a/1",
		Content:   "stable",
		CreatedAt: "2026-08-08T12:00:00Z",
		Account:   Account{ID: "42", Username: "a"},
	}
	changed := base
	changed.ID = "local-2"
	a, err := NormalizeStatusForAccount(base, "https://hachyderm.io", "42", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatusForAccount(base): %v", err)
	}
	b, err := NormalizeStatusForAccount(changed, "https://hachyderm.io", "42", time.Now())
	if err != nil {
		t.Fatalf("NormalizeStatusForAccount(changed): %v", err)
	}
	if a.Item.SourceKey != b.Item.SourceKey || a.Item.ContentHash == b.Item.ContentHash || a.Item.ExternalID == b.Item.ExternalID {
		t.Fatalf("local ID change was not retained as an item update: base=%#v changed=%#v", a.Item, b.Item)
	}
}

func TestNormalizeStatusUsesVerifiedAccountScopeAndPreservesSafeExtensions(t *testing.T) {
	var status StatusRecord
	if err := json.Unmarshal([]byte(`{"id":"1","uri":"https://remote.example/users/author/statuses/1","url":"https://remote.example/@author/1","content":"<p>hello</p>","created_at":"2026-08-08T12:00:00Z","account":{"id":"author-99","username":"author","future_account":{"safe":"retain"}},"media_attachments":[{"id":"m1","type":"image","url":"https://cdn.example/image.jpg?access_token=do-not-retain"}],"extension_field":{"future":true,"token":"token-do-not-retain","bearer_token":"bearer-do-not-retain","authorization":"authorization-do-not-retain","nested_url":"https://cdn.example/future?refresh_token=refresh-do-not-retain&authorization_code=code-do-not-retain&pkce_verifier=pkce-do-not-retain"},"access_token":"access-do-not-retain"}`), &status); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	projection, err := NormalizeStatusForAccount(status, "https://hachyderm.io", "verified-42", time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeStatusForAccount: %v", err)
	}
	if !strings.Contains(projection.Item.SourceKey, "account:verified-42:uri:") || strings.Contains(projection.Item.SourceKey, "author-99") {
		t.Fatalf("source key did not use verified account scope: %q", projection.Item.SourceKey)
	}
	for _, secret := range []string{"token-do-not-retain", "bearer-do-not-retain", "authorization-do-not-retain", "refresh-do-not-retain", "code-do-not-retain", "pkce-do-not-retain", "access-do-not-retain"} {
		if strings.Contains(projection.Item.RawJSON, secret) {
			t.Fatalf("raw extension evidence retained secret %q: %s", secret, projection.Item.RawJSON)
		}
	}
	if !strings.Contains(projection.Item.RawJSON, "extension_field") || !strings.Contains(projection.Item.RawJSON, "future") {
		t.Fatalf("raw extension evidence was not safely preserved: %s", projection.Item.RawJSON)
	}
	if !strings.Contains(projection.Item.RawJSON, "future_account") || !strings.Contains(projection.Item.RawJSON, "retain") {
		t.Fatalf("unknown nested evidence was not safely preserved: %s", projection.Item.RawJSON)
	}
}
