package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
	"golang.org/x/oauth2"
)

const (
	authSessionCookieName = "dbrain_session_token"
	authStateCookieName   = "dbrain_oauth_state"
	authReturnCookieName  = "dbrain_return_to"
)

var (
	errAuthAccessDenied = errors.New("GitHub user is not approved")

	githubOAuthEndpoint = oauth2.Endpoint{
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
	}
	githubUserAPIURL = "https://api.github.com/user"
)

type authContextKey struct{}

type authManager struct {
	cfg         authConfig
	store       *store.Store
	sessions    *authSessionStore
	githubOAuth *oauth2.Config
}

type githubOAuthUser struct {
	ID        json.Number `json:"id"`
	Login     string      `json:"login"`
	Email     string      `json:"email"`
	Name      string      `json:"name"`
	AvatarURL string      `json:"avatar_url"`
}

func newAuthManager(cfg authConfig, st *store.Store) (*authManager, error) {
	return newAuthManagerWithCleanup(context.TODO(), cfg, st, false)
}

func newAuthManagerWithContext(ctx context.Context, cfg authConfig, st *store.Store) (*authManager, error) {
	return newAuthManagerWithCleanup(ctx, cfg, st, true)
}

func newAuthManagerWithCleanup(ctx context.Context, cfg authConfig, st *store.Store, cleanup bool) (*authManager, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if st == nil {
		return nil, fmt.Errorf("auth.enabled requires a writable store")
	}

	manager := &authManager{
		cfg:      cfg,
		store:    st,
		sessions: newAuthSessionStore(),
	}
	if cleanup {
		manager.sessions.startCleanup(ctx, cfg.SessionTTL/2)
	}
	if oauthProviderAllowed(cfg.Providers, authProviderGitHub) {
		manager.githubOAuth = &oauth2.Config{
			ClientID:     cfg.GitHubClientID,
			ClientSecret: cfg.GitHubClientSecret,
			Endpoint:     githubOAuthEndpoint,
			RedirectURL:  cfg.BaseURL + "/auth/github/callback",
			Scopes:       []string{"read:user"},
		}
	}
	if manager.githubOAuth == nil {
		return nil, fmt.Errorf("auth.enabled has no configured OAuth provider")
	}
	return manager, nil
}

func (a *authManager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	returnTo := cleanReturnTo(r.URL.Query().Get("return_to"))
	if returnTo != "" {
		setCookie(w, authReturnCookieName, returnTo, a.cfg.OAuthStateTTL, a.secureCookies())
	}

	if _, ok := a.sessionFromRequest(r); ok {
		if returnTo == "" {
			returnTo = cleanReturnToFromCookie(r)
		}
		if returnTo == "" {
			returnTo = "/"
		}
		clearCookie(w, authReturnCookieName, a.secureCookies())
		http.Redirect(w, r, returnTo, http.StatusSeeOther)
		return
	}

	a.renderLogin(w, http.StatusOK, "")
}

func (a *authManager) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if cookie, err := r.Cookie(authSessionCookieName); err == nil {
		a.sessions.delete(cookie.Value)
	}
	clearCookie(w, authSessionCookieName, a.secureCookies())
	clearCookie(w, authReturnCookieName, a.secureCookies())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *authManager) handleAuth(w http.ResponseWriter, r *http.Request) {
	provider, callback, ok := authRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if provider != authProviderGitHub || !oauthProviderAllowed(a.cfg.Providers, provider) {
		writeMessage(w, http.StatusNotFound, "OAuth provider is not enabled")
		return
	}
	if callback {
		a.handleOAuthCallback(w, r)
		return
	}
	a.handleBeginOAuth(w, r)
}

func (a *authManager) handleBeginOAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	setCookie(w, authStateCookieName, a.signState(state), a.cfg.OAuthStateTTL, a.secureCookies())
	http.Redirect(w, r, a.githubOAuth.AuthCodeURL(state), http.StatusSeeOther)
}

func (a *authManager) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if oauthErr := strings.TrimSpace(r.URL.Query().Get("error")); oauthErr != "" {
		a.renderLogin(w, http.StatusForbidden, oauthErr)
		return
	}

	cookie, err := r.Cookie(authStateCookieName)
	if err != nil {
		writeMessage(w, http.StatusBadRequest, "missing OAuth state")
		return
	}
	expectedState, ok := a.verifyState(cookie.Value)
	if !ok || expectedState == "" || expectedState != r.URL.Query().Get("state") {
		writeMessage(w, http.StatusBadRequest, "invalid OAuth state")
		return
	}
	clearCookie(w, authStateCookieName, a.secureCookies())

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeMessage(w, http.StatusBadRequest, "missing OAuth code")
		return
	}

	token, err := a.githubOAuth.Exchange(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("exchange GitHub OAuth code: %w", err))
		return
	}
	githubUser, err := fetchGitHubOAuthUser(r.Context(), a.githubOAuth.Client(r.Context(), token))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	session, err := a.createSessionForGitHubUser(r.Context(), githubUser)
	if errors.Is(err, errAuthAccessDenied) {
		a.renderLogin(w, http.StatusForbidden, "GitHub user is not approved for this dbrain instance.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	setCookie(w, authSessionCookieName, session.Token, time.Until(session.ExpiresAt), a.secureCookies())
	returnTo := cleanReturnToFromCookie(r)
	if returnTo == "" {
		returnTo = "/"
	}
	clearCookie(w, authReturnCookieName, a.secureCookies())
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func (a *authManager) createSessionForGitHubUser(ctx context.Context, user githubOAuthUser) (authSession, error) {
	username := strings.TrimPrefix(strings.TrimSpace(user.Login), "@")
	normalizedUsername := store.NormalizeGitHubUsername(username)
	if normalizedUsername == "" {
		return authSession{}, fmt.Errorf("GitHub OAuth response did not include a login")
	}
	githubID := strings.TrimSpace(user.ID.String())

	approved, found, err := a.store.GetGitHubAuthUserByID(ctx, githubID)
	if err != nil {
		return authSession{}, err
	}
	if !found {
		approved, found, err = a.store.GetGitHubAuthUserByUsername(ctx, normalizedUsername)
		if err != nil {
			return authSession{}, err
		}
		if !found {
			return authSession{}, errAuthAccessDenied
		}
		if approved.GitHubID != "" && approved.GitHubID != githubID {
			return authSession{}, errAuthAccessDenied
		}
	}

	approved, err = a.store.UpdateGitHubAuthUserFromOAuth(ctx, approved.ID, store.GitHubAuthProfile{
		GitHubID:       githubID,
		GitHubUsername: username,
		Email:          user.Email,
		Name:           user.Name,
		AvatarURL:      user.AvatarURL,
	})
	if err != nil {
		return authSession{}, err
	}

	sessionID := strings.TrimSpace(approved.GitHubID)
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", approved.ID)
	}
	sessionUsername := strings.TrimSpace(approved.GitHubUsername)
	if sessionUsername == "" {
		sessionUsername = normalizedUsername
	}
	if approved.Provider != authProviderGitHub {
		return authSession{}, errAuthAccessDenied
	}
	return a.sessions.create(authUser{
		Provider: authProviderGitHub,
		ID:       sessionID,
		Username: sessionUsername,
		Email:    strings.TrimSpace(approved.Email),
	}, a.cfg.SessionTTL)
}

func fetchGitHubOAuthUser(ctx context.Context, client *http.Client) (githubOAuthUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserAPIURL, nil)
	if err != nil {
		return githubOAuthUser{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dbrain")

	resp, err := client.Do(req)
	if err != nil {
		return githubOAuthUser{}, fmt.Errorf("fetch GitHub OAuth user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return githubOAuthUser{}, fmt.Errorf("fetch GitHub OAuth user: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var user githubOAuthUser
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&user); err != nil {
		return githubOAuthUser{}, fmt.Errorf("decode GitHub OAuth user: %w", err)
	}
	return user, nil
}

func (a *authManager) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := a.sessionFromRequest(r)
		if ok {
			ctx := context.WithValue(r.Context(), authContextKey{}, session.User)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if wantsAuthJSON(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error":    "authentication required",
				"redirect": "/login",
			})
			return
		}

		loginURL := "/login"
		if returnTo := authReturnToForRequest(r); returnTo != "" {
			loginURL += "?return_to=" + url.QueryEscape(returnTo)
		}
		http.Redirect(w, r, loginURL, http.StatusSeeOther)
	})
}

func (a *authManager) sessionFromRequest(r *http.Request) (authSession, bool) {
	cookie, err := r.Cookie(authSessionCookieName)
	if err != nil {
		return authSession{}, false
	}
	return a.sessions.get(cookie.Value)
}

func (a *authManager) signState(state string) string {
	mac := hmac.New(sha256.New, []byte(a.cfg.SessionKey))
	_, _ = mac.Write([]byte(state))
	return state + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *authManager) verifyState(value string) (string, bool) {
	state, signature, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok || state == "" || signature == "" {
		return "", false
	}
	expected := a.signState(state)
	_, expectedSignature, _ := strings.Cut(expected, ".")
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", false
	}
	want, err := base64.RawURLEncoding.DecodeString(expectedSignature)
	if err != nil {
		return "", false
	}
	if !hmac.Equal(got, want) {
		return "", false
	}
	return state, true
}

func (a *authManager) secureCookies() bool {
	return strings.HasPrefix(strings.ToLower(a.cfg.BaseURL), "https://")
}

func (a *authManager) renderLogin(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, struct {
		Error string
	}{
		Error: strings.TrimSpace(message),
	})
}

func authUserFromContext(ctx context.Context) (authUser, bool) {
	user, ok := ctx.Value(authContextKey{}).(authUser)
	return user, ok
}

func (s *server) authInfo(ctx context.Context) AuthInfo {
	if s.auth == nil {
		return AuthInfo{Enabled: false}
	}
	info := AuthInfo{Enabled: true}
	if len(s.auth.cfg.Providers) > 0 {
		info.Provider = s.auth.cfg.Providers[0]
	}
	if user, ok := authUserFromContext(ctx); ok {
		info.Provider = user.Provider
		info.User = &AuthUserInfo{
			Provider: user.Provider,
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
		}
	}
	return info
}

func authRoute(requestPath string) (provider string, callback bool, ok bool) {
	rest := strings.Trim(strings.TrimPrefix(requestPath, "/auth/"), "/")
	if rest == "" {
		return "", false, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		return strings.ToLower(parts[0]), false, true
	}
	if len(parts) == 2 && parts[1] == "callback" {
		return strings.ToLower(parts[0]), true, true
	}
	return "", false, false
}

func wantsAuthJSON(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}

func authReturnToForRequest(r *http.Request) string {
	if !isReturnToCandidate(r.URL.Path) {
		return ""
	}
	return cleanReturnTo(r.URL.RequestURI())
}

func cleanReturnToFromCookie(r *http.Request) string {
	cookie, err := r.Cookie(authReturnCookieName)
	if err != nil {
		return ""
	}
	return cleanReturnTo(cookie.Value)
}

func cleanReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if !isValidReturnURL(value) {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if !isReturnToCandidate(u.Path) {
		return ""
	}
	return value
}

func isValidReturnURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	if strings.Contains(value, "://") || strings.Contains(value, "\\") {
		return false
	}
	return true
}

func isReturnToCandidate(requestPath string) bool {
	cleaned := path.Clean("/" + strings.TrimSpace(requestPath))
	if cleaned == "/login" || cleaned == "/logout" || cleaned == "/favicon.ico" {
		return false
	}
	if strings.HasPrefix(cleaned, "/api/") || strings.HasPrefix(cleaned, "/auth/") {
		return false
	}
	return true
}

func setCookie(w http.ResponseWriter, name string, value string, ttl time.Duration, secure bool) {
	maxAge := int(ttl.Seconds())
	if maxAge <= 0 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   maxAge,
		Expires:  time.Now().Add(time.Duration(maxAge) * time.Second),
	})
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>dbrain login</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { min-height: 100vh; margin: 0; display: grid; place-items: center; background: #f5f7fb; color: #172033; }
    main { width: min(92vw, 360px); }
    h1 { margin: 0 0 8px; font-size: 28px; line-height: 1.1; letter-spacing: 0; }
    p { margin: 0 0 20px; color: #586273; line-height: 1.45; }
    a.button { display: inline-flex; align-items: center; justify-content: center; gap: 10px; width: 100%; min-height: 44px; border-radius: 8px; background: #1f2937; color: #fff; font-weight: 700; text-decoration: none; }
    .error { margin-bottom: 16px; padding: 10px 12px; border-left: 3px solid #c2410c; background: #fff7ed; color: #7c2d12; line-height: 1.4; }
    @media (prefers-color-scheme: dark) {
      body { background: #111827; color: #f9fafb; }
      p { color: #cbd5e1; }
      a.button { background: #f9fafb; color: #111827; }
      .error { background: #431407; color: #fed7aa; }
    }
  </style>
</head>
<body>
  <main>
    <h1>dbrain</h1>
    <p>Sign in with an approved GitHub account.</p>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
    <a class="button" href="/auth/github">Continue with GitHub</a>
  </main>
</body>
</html>
`))
