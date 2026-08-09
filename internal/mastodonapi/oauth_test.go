package mastodonapi

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
)

func TestBuildAuthorizationRequestUsesPKCEAndReadOnlyScopes(t *testing.T) {
	request, err := BuildAuthorizationRequest(OAuthEndpoints{
		AuthorizationURL: "https://hachyderm.io:443/oauth/authorize",
	}, "client-id", "http://127.0.0.1:8743/oauth/mastodon/callback", bytes.NewReader(bytes.Repeat([]byte{0x42}, 96)))
	if err != nil {
		t.Fatalf("BuildAuthorizationRequest: %v", err)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" || query.Get("client_id") != "client-id" {
		t.Fatalf("authorization query = %v", query)
	}
	if query.Get("redirect_uri") != "http://127.0.0.1:8743/oauth/mastodon/callback" {
		t.Fatalf("redirect_uri = %q", query.Get("redirect_uri"))
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Fatalf("PKCE query = %v", query)
	}
	if query.Get("state") != request.State || request.Verifier == "" {
		t.Fatalf("state/verifier = %#v", request)
	}
	if !strings.Contains(query.Get("scope"), "read:bookmarks") || !strings.Contains(query.Get("scope"), "read:statuses") || strings.Contains(query.Get("scope"), "write:") {
		t.Fatalf("scope = %q", query.Get("scope"))
	}
}

func TestBuildAuthorizationRequestRejectsNonFixedCallback(t *testing.T) {
	if _, err := BuildAuthorizationRequest(OAuthEndpoints{AuthorizationURL: "https://hachyderm.io:443/oauth/authorize"}, "client-id", "http://127.0.0.1:8743/other", bytes.NewReader(bytes.Repeat([]byte{0x42}, 96))); err == nil {
		t.Fatal("BuildAuthorizationRequest accepted a non-fixed callback")
	}
}

func TestBuildAuthorizationRequestRejectsEndpointQueryAndFragment(t *testing.T) {
	for _, endpoint := range []string{
		"https://hachyderm.io:443/oauth/authorize?next=evil",
		"https://hachyderm.io:443/oauth/authorize#fragment",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := BuildAuthorizationRequest(OAuthEndpoints{AuthorizationURL: endpoint}, "client-id", DefaultMastodonRedirectURI, bytes.NewReader(bytes.Repeat([]byte{0x42}, 96))); err == nil {
				t.Fatal("BuildAuthorizationRequest accepted an endpoint query or fragment")
			}
		})
	}
}

func TestValidateCallbackRequiresExactLoopbackPathAndState(t *testing.T) {
	const callback = "http://127.0.0.1:8743/oauth/mastodon/callback"
	valid := callback + "?code=auth-code&state=expected"
	code, err := ValidateCallback(valid, "expected", callback)
	if err != nil || code != "auth-code" {
		t.Fatalf("valid callback = %q, %v", code, err)
	}

	for _, raw := range []string{
		callback + "?code=auth-code&state=wrong",
		"http://evil.example/oauth/mastodon/callback?code=auth-code&state=expected",
		"http://127.0.0.1:8743/other?code=auth-code&state=expected",
		callback + "?state=expected",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ValidateCallback(raw, "expected", callback); err == nil {
				t.Fatal("ValidateCallback accepted an invalid callback")
			}
		})
	}
}
