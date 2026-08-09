package mastodonapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRegisterApplicationUsesExactLoopbackRedirectAndReadScopes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/v1/apps" {
			t.Fatalf("request = %s %s", req.Method, req.URL)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read form: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if form.Get("redirect_uris") != fixedMastodonRedirectURI || strings.Contains(form.Get("scopes"), "write:") {
			t.Fatalf("registration form = %v", form)
		}
		return jsonResponse(http.StatusOK, `{"client_id":"client-id","client_secret":"client-secret"}`), nil
	})}

	app, err := RegisterApplication(context.Background(), client, "https://hachyderm.io:443", fixedMastodonRedirectURI)
	if err != nil {
		t.Fatalf("RegisterApplication: %v", err)
	}
	if app.ClientID != "client-id" || app.ClientSecret != "client-secret" {
		t.Fatalf("application = %#v", app)
	}
}

func TestExchangeAuthorizationCodeUsesPKCEAndReadOnlyScope(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/oauth/token" {
			t.Fatalf("request = %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Authorization") != "" {
			t.Fatal("token exchange sent a bearer token")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read form: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if form.Get("code_verifier") != "verifier" || form.Get("redirect_uri") != fixedMastodonRedirectURI || form.Get("client_id") != "client-id" {
			t.Fatalf("token form = %v", form)
		}
		return jsonResponse(http.StatusOK, `{"access_token":"token-value","scope":"read:bookmarks read:statuses","token_type":"Bearer"}`), nil
	})}

	token, err := ExchangeAuthorizationCode(context.Background(), client, OAuthEndpoints{TokenURL: "https://hachyderm.io:443/oauth/token"}, "client-id", "client-secret", "auth-code", "verifier", fixedMastodonRedirectURI)
	if err != nil {
		t.Fatalf("ExchangeAuthorizationCode: %v", err)
	}
	if token.AccessToken != "token-value" || !strings.Contains(token.Scope, "read:bookmarks") {
		t.Fatalf("token = %#v", token)
	}
}

func TestVerifyCredentialsRequiresStableAccountID(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		return jsonResponse(http.StatusOK, `{"id":"123","username":"darron","acct":"darron","url":"https://hachyderm.io/users/darron"}`), nil
	})}

	account, err := VerifyCredentials(context.Background(), client, "https://hachyderm.io:443", "access-token")
	if err != nil {
		t.Fatalf("VerifyCredentials: %v", err)
	}
	if account.ID != "123" || account.Acct != "darron" {
		t.Fatalf("account = %#v", account)
	}
}

func TestVerifyCredentialsRejectsMissingHTTPClient(t *testing.T) {
	if _, err := VerifyCredentials(context.Background(), nil, "https://hachyderm.io", "token"); err == nil {
		t.Fatal("VerifyCredentials accepted a nil HTTP client")
	}
}

func TestLogoutRevokesBeforeDeletingAccessToken(t *testing.T) {
	store := &testSecretStore{values: map[string]string{
		"keychain://dbrain/access-token":  "access-token",
		"keychain://dbrain/client-id":     "client-id",
		"keychain://dbrain/client-secret": "client-secret",
	}}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v2/instance":
			return jsonResponse(http.StatusOK, `{"version":"4.3.1"}`), nil
		case "/.well-known/oauth-authorization-server":
			return jsonResponse(http.StatusNotFound, `{}`), nil
		case "/oauth/revoke":
			body, _ := io.ReadAll(req.Body)
			form, _ := url.ParseQuery(string(body))
			if form.Get("token") != "access-token" || req.Header.Get("Authorization") != "" {
				t.Fatalf("revoke request form/auth = %v/%q", form, req.Header.Get("Authorization"))
			}
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			return nil, errors.New("unexpected request " + req.URL.Path)
		}
	})}
	account := AccountConfig{
		Origin:          "https://hachyderm.io",
		AccessTokenRef:  "keychain://dbrain/access-token",
		ClientIDRef:     "keychain://dbrain/client-id",
		ClientSecretRef: "keychain://dbrain/client-secret",
	}

	result, err := Logout(context.Background(), account, LogoutOptions{HTTPClient: client, SecretStore: store})
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if !result.Revoked || !result.AccessTokenCleared {
		t.Fatalf("logout result = %#v", result)
	}
	if _, ok := store.values[account.AccessTokenRef]; ok {
		t.Fatal("access token remained after successful revocation")
	}
	if _, ok := store.values[account.ClientIDRef]; !ok {
		t.Fatal("client credentials were cleared without --forget-client")
	}
}

func TestLogoutKeepsTokenWhenRemoteRevocationFails(t *testing.T) {
	store := &testSecretStore{values: map[string]string{
		"keychain://dbrain/access-token":  "access-token",
		"keychain://dbrain/client-id":     "client-id",
		"keychain://dbrain/client-secret": "client-secret",
	}}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v2/instance":
			return jsonResponse(http.StatusOK, `{"version":"4.3.1"}`), nil
		case "/.well-known/oauth-authorization-server":
			return jsonResponse(http.StatusNotFound, `{}`), nil
		case "/oauth/revoke":
			return jsonResponse(http.StatusInternalServerError, `{}`), nil
		default:
			return nil, errors.New("unexpected request " + req.URL.Path)
		}
	})}
	account := AccountConfig{Origin: "https://hachyderm.io", AccessTokenRef: "keychain://dbrain/access-token", ClientIDRef: "keychain://dbrain/client-id", ClientSecretRef: "keychain://dbrain/client-secret"}

	if _, err := Logout(context.Background(), account, LogoutOptions{HTTPClient: client, SecretStore: store}); err == nil {
		t.Fatal("Logout succeeded despite remote revocation failure")
	}
	if got := store.values[account.AccessTokenRef]; got != "access-token" {
		t.Fatalf("access token = %q, want retained token", got)
	}
}

func TestLogoutForgetClientClearsClientCredentialsWhenTokenIsAlreadyAbsent(t *testing.T) {
	store := &testSecretStore{values: map[string]string{
		"keychain://dbrain/client-id":     "client-id",
		"keychain://dbrain/client-secret": "client-secret",
	}}
	account := AccountConfig{
		AccessTokenRef:  "keychain://dbrain/access-token",
		ClientIDRef:     "keychain://dbrain/client-id",
		ClientSecretRef: "keychain://dbrain/client-secret",
	}

	result, err := Logout(context.Background(), account, LogoutOptions{SecretStore: store, ForgetClient: true})
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if result.ClientCleared {
		if _, ok := store.values[account.ClientIDRef]; ok {
			t.Fatal("client ID remained after --forget-client")
		}
		if _, ok := store.values[account.ClientSecretRef]; ok {
			t.Fatal("client secret remained after --forget-client")
		}
		return
	}
	t.Fatalf("logout result = %#v, want client credentials cleared", result)
}

func TestStatusResolvesTokenVerifiesAccountAndReportsRedactedIdentity(t *testing.T) {
	var gotToken string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/v1/accounts/verify_credentials" {
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
		gotToken = strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		return jsonResponse(http.StatusOK, `{"id":"42","acct":"darron@hachyderm.io","username":"darron"}`), nil
	})}
	result, err := Status(context.Background(), AccountConfig{Key: "hachyderm", Origin: "https://hachyderm.io", AccessTokenRef: "env:MASTODON_TOKEN"}, StatusOptions{
		HTTPClient: client,
		ResolveSecret: func(_ context.Context, ref string) (string, error) {
			if ref != "env:MASTODON_TOKEN" {
				t.Fatalf("ResolveSecret ref = %q", ref)
			}
			return "secret-token", nil
		},
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if gotToken != "secret-token" {
		t.Fatalf("verify token = %q", gotToken)
	}
	if result.AccountID != "42" || result.AccountHandle != "darron@hachyderm.io" {
		t.Fatalf("verified account = %#v", result)
	}
	if result.TokenFingerprint == "" || strings.Contains(result.TokenFingerprint, "secret-token") {
		t.Fatalf("token fingerprint = %q", result.TokenFingerprint)
	}
	if strings.Join(result.EffectiveScopes, " ") != mastodonReadOnlyScopes {
		t.Fatalf("effective scopes = %#v", result.EffectiveScopes)
	}
}
