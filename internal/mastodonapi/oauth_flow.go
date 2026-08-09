package mastodonapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
)

type ApplicationCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func RegisterApplication(ctx context.Context, client *http.Client, origin, redirectURI string) (ApplicationCredentials, error) {
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return ApplicationCredentials{}, err
	}
	if redirectURI != fixedMastodonRedirectURI {
		return ApplicationCredentials{}, fmt.Errorf("mastodon oauth redirect URI must be %s", fixedMastodonRedirectURI)
	}
	if client == nil {
		return ApplicationCredentials{}, fmt.Errorf("oauth HTTP client is required")
	}
	status, body, err := postForm(ctx, client, strings.TrimRight(canonical, "/")+"/api/v1/apps", url.Values{
		"client_name":   {"dbrain"},
		"redirect_uris": {redirectURI},
		"scopes":        {mastodonReadOnlyScopes},
	}, maxMastodonAPIResponseBytes)
	if err != nil {
		return ApplicationCredentials{}, fmt.Errorf("register Mastodon application: %w", err)
	}
	if status < 200 || status >= 300 {
		return ApplicationCredentials{}, fmt.Errorf("register Mastodon application: HTTP %d", status)
	}
	var app ApplicationCredentials
	if err := json.Unmarshal(body, &app); err != nil {
		return ApplicationCredentials{}, fmt.Errorf("decode Mastodon application: %w", err)
	}
	if strings.TrimSpace(app.ClientID) == "" || strings.TrimSpace(app.ClientSecret) == "" {
		return ApplicationCredentials{}, fmt.Errorf("mastodon application response omitted client credentials")
	}
	return app, nil
}

type LoginOptions struct {
	HTTPClient         *http.Client
	SecretStore        SecretStore
	Listen             func(network, address string) (net.Listener, error)
	OpenBrowser        func(string) error
	OnAuthorizationURL func(string)
	Random             io.Reader
	// CallbackCode is a deterministic test seam. Production callers leave it
	// nil and receive the code through the fixed loopback listener.
	CallbackCode func(context.Context, AuthorizationRequest) (string, error)
}

type LoginResult struct {
	Origin           string   `json:"origin"`
	AccountID        string   `json:"account_id"`
	AccountHandle    string   `json:"account_handle"`
	EffectiveScopes  []string `json:"effective_scopes"`
	TokenFingerprint string   `json:"token_fingerprint"`
}

// StatusOptions contains the injectable boundaries needed to verify an
// already-configured account without exposing its token in the result.
type StatusOptions struct {
	HTTPClient    *http.Client
	ResolveSecret func(context.Context, string) (string, error)
}

// StatusResult is the non-secret account identity and capability view shown
// by auth status. Mastodon does not expose a portable token introspection
// endpoint, so the read-only scope set is the scope contract requested by
// dbrain and accepted during login; verify_credentials proves the token is
// still valid for the configured origin.
type StatusResult struct {
	Key              string   `json:"key"`
	Origin           string   `json:"origin"`
	AccountID        string   `json:"account_id"`
	AccountHandle    string   `json:"account_handle"`
	EffectiveScopes  []string `json:"effective_scopes"`
	TokenFingerprint string   `json:"token_fingerprint"`
}

type LogoutOptions struct {
	HTTPClient   *http.Client
	SecretStore  SecretStore
	ForgetClient bool
	LocalOnly    bool
}

type LogoutResult struct {
	Revoked            bool `json:"revoked"`
	AccessTokenCleared bool `json:"access_token_cleared"`
	ClientCleared      bool `json:"client_cleared"`
}

var mastodonOAuthCallbackMu sync.Mutex

func RevokeToken(ctx context.Context, client *http.Client, endpoints OAuthEndpoints, clientID, clientSecret, accessToken string) error {
	if client == nil {
		return fmt.Errorf("oauth HTTP client is required")
	}
	if err := validateHTTPSURL(endpoints.RevocationURL); err != nil {
		return fmt.Errorf("validate OAuth revocation endpoint: %w", err)
	}
	if strings.TrimSpace(accessToken) == "" {
		return fmt.Errorf("mastodon access token is empty")
	}
	form := url.Values{"token": {accessToken}}
	if strings.TrimSpace(clientID) != "" {
		form.Set("client_id", clientID)
	}
	if strings.TrimSpace(clientSecret) != "" {
		form.Set("client_secret", clientSecret)
	}
	status, _, err := postForm(ctx, client, endpoints.RevocationURL, form, maxMastodonAPIResponseBytes)
	if err != nil {
		return fmt.Errorf("revoke Mastodon token: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("revoke Mastodon token: HTTP %d", status)
	}
	return nil
}

func Logout(ctx context.Context, account AccountConfig, opts LogoutOptions) (LogoutResult, error) {
	store := opts.SecretStore
	if store == nil {
		store = KeychainSecretStore{}
	}
	token, err := optionalSecret(ctx, store, account.AccessTokenRef)
	if err != nil {
		return LogoutResult{}, fmt.Errorf("resolve Mastodon access token: %w", err)
	}
	if token == "" {
		if !opts.ForgetClient {
			return LogoutResult{}, nil
		}
		if err := clearClientCredentials(ctx, store, account); err != nil {
			return LogoutResult{}, err
		}
		return LogoutResult{ClientCleared: true}, nil
	}
	clientID, clientSecret, err := loadClientCredentials(ctx, store, account)
	if err != nil {
		return LogoutResult{}, err
	}
	result := LogoutResult{}
	if !opts.LocalOnly {
		canonical, err := canonicalOrigin(account.Origin)
		if err != nil {
			return LogoutResult{}, err
		}
		client := opts.HTTPClient
		if client == nil {
			client = safeMastodonClient(canonical)
		}
		endpoints, err := DiscoverOAuthEndpoints(ctx, canonical, client)
		if err != nil {
			return LogoutResult{}, err
		}
		if err := RevokeToken(ctx, client, endpoints, clientID, clientSecret, token); err != nil {
			return LogoutResult{}, err
		}
		result.Revoked = true
	}
	if err := store.Delete(ctx, account.AccessTokenRef); err != nil {
		return result, fmt.Errorf("delete Mastodon access token: %w", err)
	}
	result.AccessTokenCleared = true
	if opts.ForgetClient {
		if err := clearClientCredentials(ctx, store, account); err != nil {
			return result, err
		}
		result.ClientCleared = true
	}
	return result, nil
}

func clearClientCredentials(ctx context.Context, store SecretStore, account AccountConfig) error {
	if err := store.Delete(ctx, account.ClientIDRef); err != nil {
		return fmt.Errorf("delete Mastodon client ID: %w", err)
	}
	if err := store.Delete(ctx, account.ClientSecretRef); err != nil {
		return fmt.Errorf("delete Mastodon client secret: %w", err)
	}
	return nil
}

func Login(ctx context.Context, account AccountConfig, opts LoginOptions) (LoginResult, error) {
	canonical, err := canonicalOrigin(account.Origin)
	if err != nil {
		return LoginResult{}, err
	}
	if err := validateOAuthWriteRefs(account); err != nil {
		return LoginResult{}, err
	}
	store := opts.SecretStore
	if store == nil {
		store = KeychainSecretStore{}
	}
	client := opts.HTTPClient
	if client == nil {
		client = safeMastodonClient(canonical)
	}
	endpoints, err := DiscoverOAuthEndpoints(ctx, canonical, client)
	if err != nil {
		return LoginResult{}, err
	}
	if !endpoints.SupportsPKCE {
		return LoginResult{}, fmt.Errorf("interactive Mastodon login requires Mastodon 4.3+ PKCE support; configure a manually provisioned read-only token for this server")
	}
	clientID, clientSecret, err := loadClientCredentials(ctx, store, account)
	if err != nil {
		return LoginResult{}, err
	}
	if clientID == "" && clientSecret == "" {
		application, err := RegisterApplication(ctx, client, canonical, fixedMastodonRedirectURI)
		if err != nil {
			return LoginResult{}, err
		}
		clientID, clientSecret = application.ClientID, application.ClientSecret
	}
	authorization, err := BuildAuthorizationRequest(endpoints, clientID, fixedMastodonRedirectURI, opts.Random)
	if err != nil {
		return LoginResult{}, err
	}
	var receiver *callbackReceiver
	if opts.CallbackCode == nil {
		receiver, err = startCallbackReceiver(authorization, opts.Listen)
		if err != nil {
			return LoginResult{}, err
		}
		defer receiver.Close()
	}
	if opts.OnAuthorizationURL != nil {
		opts.OnAuthorizationURL(authorization.URL)
	}
	if opts.OpenBrowser != nil {
		// Manual/headless completion remains valid when the browser opener fails.
		_ = opts.OpenBrowser(authorization.URL)
	}
	var code string
	if opts.CallbackCode != nil {
		code, err = opts.CallbackCode(ctx, authorization)
	} else {
		code, err = receiver.Wait(ctx)
	}
	if err != nil {
		return LoginResult{}, err
	}
	token, err := ExchangeAuthorizationCode(ctx, client, endpoints, clientID, clientSecret, code, authorization.Verifier, fixedMastodonRedirectURI)
	if err != nil {
		return LoginResult{}, err
	}
	verified, err := VerifyCredentials(ctx, client, canonical, token.AccessToken)
	if err != nil {
		return LoginResult{}, err
	}
	if err := PersistOAuthSecrets(ctx, store, account, token.AccessToken, clientID, clientSecret); err != nil {
		return LoginResult{}, err
	}
	scopes := strings.Fields(token.Scope)
	if len(scopes) == 0 {
		scopes = strings.Fields(mastodonReadOnlyScopes)
	}
	return LoginResult{
		Origin:           canonical,
		AccountID:        verified.ID,
		AccountHandle:    verified.Acct,
		EffectiveScopes:  scopes,
		TokenFingerprint: tokenFingerprint(token.AccessToken),
	}, nil
}

// Status resolves the configured access-token reference and verifies it
// against the configured origin. The token itself is never returned.
func Status(ctx context.Context, account AccountConfig, opts StatusOptions) (StatusResult, error) {
	canonical, err := canonicalOrigin(account.Origin)
	if err != nil {
		return StatusResult{}, err
	}
	if err := ValidateSecretRef(account.AccessTokenRef); err != nil {
		return StatusResult{}, fmt.Errorf("validate Mastodon access token ref: %w", err)
	}
	resolveSecret := opts.ResolveSecret
	if resolveSecret == nil {
		resolveSecret = ResolveTypedSecretRef
	}
	token, err := resolveSecret(ctx, account.AccessTokenRef)
	if err != nil {
		return StatusResult{}, fmt.Errorf("resolve Mastodon access token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return StatusResult{}, fmt.Errorf("mastodon access token is empty")
	}
	client := opts.HTTPClient
	if client == nil {
		client = safeMastodonClient(canonical)
	}
	verified, err := VerifyCredentials(ctx, client, canonical, token)
	if err != nil {
		return StatusResult{}, err
	}
	handle := verified.Acct
	if strings.TrimSpace(handle) == "" {
		handle = verified.Username
	}
	return StatusResult{
		Key:              account.Key,
		Origin:           canonical,
		AccountID:        verified.ID,
		AccountHandle:    handle,
		EffectiveScopes:  strings.Fields(mastodonReadOnlyScopes),
		TokenFingerprint: tokenFingerprint(token),
	}, nil
}

func safeMastodonClient(origin string) *http.Client {
	return safehttp.NewClient(safehttp.Policy{
		Timeout:        30 * time.Second,
		AllowedOrigins: []string{origin},
	})
}

func loadClientCredentials(ctx context.Context, store SecretStore, account AccountConfig) (string, string, error) {
	clientID, idErr := optionalSecret(ctx, store, account.ClientIDRef)
	if idErr != nil {
		return "", "", fmt.Errorf("resolve Mastodon client ID: %w", idErr)
	}
	clientSecret, secretErr := optionalSecret(ctx, store, account.ClientSecretRef)
	if secretErr != nil {
		return "", "", fmt.Errorf("resolve Mastodon client secret: %w", secretErr)
	}
	if (clientID == "") != (clientSecret == "") {
		return "", "", fmt.Errorf("mastodon client ID and client secret must be configured together")
	}
	return clientID, clientSecret, nil
}

func optionalSecret(ctx context.Context, store SecretStore, ref string) (string, error) {
	if err := ValidateSecretRef(ref); err != nil {
		return "", err
	}
	value, err := store.Get(ctx, ref)
	if errors.Is(err, ErrSecretNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return value, nil
}

type callbackReceiver struct {
	server   *http.Server
	listener net.Listener
	codes    chan string
}

func startCallbackReceiver(authorization AuthorizationRequest, listen func(string, string) (net.Listener, error)) (*callbackReceiver, error) {
	mastodonOAuthCallbackMu.Lock()
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", "127.0.0.1:8743")
	if err != nil {
		mastodonOAuthCallbackMu.Unlock()
		return nil, fmt.Errorf("bind fixed Mastodon OAuth callback %s: %w", fixedMastodonRedirectURI, err)
	}
	codes := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		callbackURL := "http://" + request.Host + request.URL.RequestURI()
		code, err := ValidateCallback(callbackURL, authorization.State, fixedMastodonRedirectURI)
		if err != nil {
			http.Error(writer, "OAuth callback rejected", http.StatusBadRequest)
			return
		}
		select {
		case codes <- code:
			writer.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(writer, "dbrain Mastodon authorization received; you may close this window.\n")
		default:
			http.Error(writer, "OAuth callback already consumed", http.StatusBadRequest)
		}
	})}
	receiver := &callbackReceiver{server: server, listener: listener, codes: codes}
	return receiver, nil
}

func (r *callbackReceiver) Wait(ctx context.Context) (string, error) {
	serveErr := make(chan error, 1)
	go func() { serveErr <- r.server.Serve(r.listener) }()
	select {
	case code := <-r.codes:
		return code, nil
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return "", fmt.Errorf("serve Mastodon OAuth callback: %w", err)
		}
		return "", fmt.Errorf("mastodon oauth callback closed before authorization")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (r *callbackReceiver) Close() {
	_ = r.server.Close()
	_ = r.listener.Close()
	mastodonOAuthCallbackMu.Unlock()
}

func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}
