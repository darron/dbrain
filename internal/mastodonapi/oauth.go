package mastodonapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const mastodonReadOnlyScopes = "profile read:bookmarks read:statuses"

const fixedMastodonRedirectURI = "http://127.0.0.1:8743/oauth/mastodon/callback"

// DefaultMastodonRedirectURI is the exact loopback callback registered for a
// Mastodon application. Interactive logins are serialized around this port.
const DefaultMastodonRedirectURI = fixedMastodonRedirectURI

// OAuthEndpoints contains the same-origin authorization endpoint discovered
// from a Mastodon instance or its OAuth metadata.
type OAuthEndpoints struct {
	AuthorizationURL string
	TokenURL         string
	RevocationURL    string
	Version          string
	SupportsPKCE     bool
}

// AuthorizationRequest is the non-secret data needed to start a PKCE login.
// Verifier must remain in memory until the token exchange completes.
type AuthorizationRequest struct {
	URL      string
	State    string
	Verifier string
}

// BuildAuthorizationRequest creates a read-only Authorization Code + PKCE
// request. The caller supplies the exact registered loopback redirect URI.
func BuildAuthorizationRequest(endpoints OAuthEndpoints, clientID, redirectURI string, random io.Reader) (AuthorizationRequest, error) {
	if strings.TrimSpace(endpoints.AuthorizationURL) == "" {
		return AuthorizationRequest{}, fmt.Errorf("authorization endpoint is required")
	}
	if strings.TrimSpace(clientID) == "" || redirectURI != fixedMastodonRedirectURI {
		return AuthorizationRequest{}, fmt.Errorf("client ID and redirect URI are required")
	}
	parsed, err := url.Parse(endpoints.AuthorizationURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return AuthorizationRequest{}, fmt.Errorf("authorization endpoint must be an HTTPS URL without credentials")
	}
	if random == nil {
		random = rand.Reader
	}
	state, err := randomToken(random, 32)
	if err != nil {
		return AuthorizationRequest{}, fmt.Errorf("generate oauth state: %w", err)
	}
	verifier, err := randomToken(random, 32)
	if err != nil {
		return AuthorizationRequest{}, fmt.Errorf("generate oauth PKCE verifier: %w", err)
	}
	hash := sha256.Sum256([]byte(verifier))
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", mastodonReadOnlyScopes)
	query.Set("state", state)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(hash[:]))
	query.Set("code_challenge_method", "S256")
	parsed.RawQuery = query.Encode()
	return AuthorizationRequest{URL: parsed.String(), State: state, Verifier: verifier}, nil
}

func randomToken(random io.Reader, size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// ValidateCallback accepts only the exact loopback callback registered for
// the OAuth client and the one-time state generated for that login.
func ValidateCallback(raw, expectedState, expectedCallback string) (string, error) {
	actual, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse oauth callback: %w", err)
	}
	expected, err := url.Parse(expectedCallback)
	if err != nil || expected.Scheme != "http" || expected.Host != "127.0.0.1:8743" || expected.Path == "" || expected.RawQuery != "" || expected.Fragment != "" {
		return "", fmt.Errorf("expected callback must be the fixed loopback callback")
	}
	if actual.Scheme != expected.Scheme || actual.Host != expected.Host || actual.Path != expected.Path || actual.User != nil || actual.Fragment != "" {
		return "", fmt.Errorf("oauth callback is not the registered loopback endpoint")
	}
	state := actual.Query().Get("state")
	if expectedState == "" || subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) != 1 {
		return "", fmt.Errorf("oauth callback state does not match")
	}
	if callbackError := actual.Query().Get("error"); callbackError != "" {
		return "", fmt.Errorf("oauth authorization failed: %s", callbackError)
	}
	code := actual.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("oauth callback code is missing")
	}
	return code, nil
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
}

// ExchangeAuthorizationCode performs the same-origin token exchange without
// sending a bearer token. The verifier is memory-only and never persisted.
func ExchangeAuthorizationCode(ctx context.Context, client *http.Client, endpoints OAuthEndpoints, clientID, clientSecret, code, verifier, redirectURI string) (TokenResponse, error) {
	if client == nil {
		return TokenResponse{}, fmt.Errorf("oauth HTTP client is required")
	}
	if err := validateHTTPSURL(endpoints.TokenURL); err != nil {
		return TokenResponse{}, fmt.Errorf("validate OAuth token endpoint: %w", err)
	}
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(code) == "" || strings.TrimSpace(verifier) == "" || redirectURI != fixedMastodonRedirectURI {
		return TokenResponse{}, fmt.Errorf("oauth token exchange parameters are incomplete")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"scope":         {mastodonReadOnlyScopes},
	}
	if strings.TrimSpace(clientSecret) != "" {
		form.Set("client_secret", clientSecret)
	}
	response, body, err := postForm(ctx, client, endpoints.TokenURL, form, maxMastodonAPIResponseBytes)
	if err != nil {
		return TokenResponse{}, err
	}
	if response < 200 || response >= 300 {
		return TokenResponse{}, fmt.Errorf("oauth token exchange: HTTP %d", response)
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return TokenResponse{}, fmt.Errorf("decode OAuth token response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return TokenResponse{}, fmt.Errorf("oauth token response omitted access_token")
	}
	return token, nil
}

type VerifiedAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Acct     string `json:"acct"`
	URL      string `json:"url"`
}

func VerifyCredentials(ctx context.Context, client *http.Client, origin, accessToken string) (VerifiedAccount, error) {
	if client == nil {
		return VerifiedAccount{}, fmt.Errorf("oauth HTTP client is required")
	}
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return VerifiedAccount{}, err
	}
	if strings.TrimSpace(accessToken) == "" {
		return VerifiedAccount{}, fmt.Errorf("mastodon access token is empty")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(canonical, "/")+"/api/v1/accounts/verify_credentials", nil)
	if err != nil {
		return VerifiedAccount{}, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := client.Do(request)
	if err != nil {
		return VerifiedAccount{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > maxMastodonAPIResponseBytes {
		return VerifiedAccount{}, fmt.Errorf("verify_credentials response exceeds %d bytes", maxMastodonAPIResponseBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMastodonAPIResponseBytes+1))
	if err != nil {
		return VerifiedAccount{}, fmt.Errorf("read verify_credentials response: %w", err)
	}
	if int64(len(body)) > maxMastodonAPIResponseBytes {
		return VerifiedAccount{}, fmt.Errorf("verify_credentials response exceeds %d bytes", maxMastodonAPIResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return VerifiedAccount{}, fmt.Errorf("verify_credentials: HTTP %d", response.StatusCode)
	}
	var account VerifiedAccount
	if err := json.Unmarshal(body, &account); err != nil {
		return VerifiedAccount{}, fmt.Errorf("decode verify_credentials response: %w", err)
	}
	if strings.TrimSpace(account.ID) == "" {
		return VerifiedAccount{}, fmt.Errorf("verify_credentials response omitted account id")
	}
	return account, nil
}

func validateHTTPSURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("URL must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func postForm(ctx context.Context, client *http.Client, rawURL string, form url.Values, maxBytes int64) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > maxBytes {
		return response.StatusCode, nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if int64(len(body)) > maxBytes {
		return response.StatusCode, nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return response.StatusCode, body, nil
}
