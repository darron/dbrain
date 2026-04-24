package xapi

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestBuildTweetResultByRestIDURLIncludesArticleFieldToggles(t *testing.T) {
	t.Parallel()

	rawURL := buildTweetResultByRestIDURL("2028894099483578872")
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if got := parsed.Path; got != "/i/api/graphql/"+tweetResultByRestIDQueryID+"/"+tweetResultByRestIDOperation {
		t.Fatalf("unexpected path: %q", got)
	}

	query := parsed.Query()

	var variables map[string]any
	if err := json.Unmarshal([]byte(query.Get("variables")), &variables); err != nil {
		t.Fatalf("unmarshal variables: %v", err)
	}
	if variables["tweetId"] != "2028894099483578872" {
		t.Fatalf("unexpected tweet id: %#v", variables["tweetId"])
	}
	if variables["includePromotedContent"] != true {
		t.Fatalf("expected includePromotedContent=true, got %#v", variables["includePromotedContent"])
	}
	if variables["withCommunity"] != true {
		t.Fatalf("expected withCommunity=true, got %#v", variables["withCommunity"])
	}
	if variables["withVoice"] != true {
		t.Fatalf("expected withVoice=true, got %#v", variables["withVoice"])
	}

	var fieldToggles map[string]bool
	if err := json.Unmarshal([]byte(query.Get("fieldToggles")), &fieldToggles); err != nil {
		t.Fatalf("unmarshal field toggles: %v", err)
	}
	for _, key := range []string{
		"withArticleRichContentState",
		"withArticlePlainText",
		"withArticleSummaryText",
		"withArticleVoiceOver",
	} {
		if !fieldToggles[key] {
			t.Fatalf("expected %s=true", key)
		}
	}
}
