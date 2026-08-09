package mastodonapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDiscoverOAuthEndpointsRequiresConfiguredOriginForMetadata(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/api/v2/instance":
			body = `{"version":"4.3.1"}`
		case "/.well-known/oauth-authorization-server":
			body = `{"issuer":"https://hachyderm.io:443","authorization_endpoint":"https://other.example/oauth/authorize","token_endpoint":"https://hachyderm.io:443/oauth/token","code_challenge_methods_supported":["S256"]}`
		default:
			return nil, errors.New("unexpected request " + req.URL.Path)
		}
		return jsonResponse(http.StatusOK, body), nil
	})}

	if _, err := DiscoverOAuthEndpoints(context.Background(), "https://hachyderm.io", client); err == nil || !strings.Contains(err.Error(), "authorization endpoint") {
		t.Fatalf("DiscoverOAuthEndpoints error = %v, want same-origin rejection", err)
	}
}

func TestDiscoverOAuthEndpointsUsesPKCEMetadataAndSameOriginDefaults(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/api/v2/instance":
			body = `{"version":"4.3.1"}`
		case "/.well-known/oauth-authorization-server":
			body = `{"issuer":"https://hachyderm.io:443","authorization_endpoint":"https://hachyderm.io:443/oauth/authorize","token_endpoint":"https://hachyderm.io:443/oauth/token","revocation_endpoint":"https://hachyderm.io:443/oauth/revoke","code_challenge_methods_supported":["S256"]}`
		default:
			return nil, errors.New("unexpected request " + req.URL.Path)
		}
		return jsonResponse(http.StatusOK, body), nil
	})}

	endpoints, err := DiscoverOAuthEndpoints(context.Background(), "https://hachyderm.io", client)
	if err != nil {
		t.Fatalf("DiscoverOAuthEndpoints: %v", err)
	}
	if !endpoints.SupportsPKCE || endpoints.AuthorizationURL != "https://hachyderm.io:443/oauth/authorize" || endpoints.TokenURL != "https://hachyderm.io:443/oauth/token" {
		t.Fatalf("endpoints = %#v", endpoints)
	}
}

func TestResolveTypedSecretRefRejectsBareValues(t *testing.T) {
	if _, err := ResolveTypedSecretRef(context.Background(), "literal-token"); err == nil {
		t.Fatal("ResolveTypedSecretRef accepted a bare secret")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
