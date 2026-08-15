package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/serviceauth"
	"golang.org/x/oauth2"
)

func TestNormalizeOAuthProvidersAllowsGitHubOnly(t *testing.T) {
	providers, err := normalizeOAuthProviders([]string{"GitHub", "github"})
	if err != nil {
		t.Fatalf("normalize providers: %v", err)
	}
	if len(providers) != 1 || providers[0] != authProviderGitHub {
		t.Fatalf("expected one github provider, got %#v", providers)
	}

	if _, err := normalizeOAuthProviders([]string{"google"}); err == nil {
		t.Fatalf("expected unsupported provider error")
	}
}

func TestLoadAuthConfigReadsGitHubProvider(t *testing.T) {
	cfg := loadTestConfig(t)
	writeAuthConfig(t, cfg, `
auth:
  enabled: true
  providers: [github]
  base_url: "https://dbrain.example.test"
  session_key: "test-session-key-32-characters-long"
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`)

	authCfg, err := loadAuthConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if !authCfg.Enabled {
		t.Fatalf("expected auth enabled")
	}
	if !oauthProviderAllowed(authCfg.Providers, authProviderGitHub) {
		t.Fatalf("expected github provider in %#v", authCfg.Providers)
	}
	if authCfg.BaseURL != "https://dbrain.example.test" {
		t.Fatalf("unexpected base URL %q", authCfg.BaseURL)
	}
}

func TestLoadAuthConfigIgnoresLegacyGitHubAllowlist(t *testing.T) {
	cfg := loadTestConfig(t)
	writeAuthConfig(t, cfg, `
auth:
  enabled: true
  providers: [github]
  session_key: "test-session-key-32-characters-long"
  allowed_github_users: ["not-authoritative"]
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`)

	authCfg, err := loadAuthConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if !authCfg.Enabled || !oauthProviderAllowed(authCfg.Providers, authProviderGitHub) {
		t.Fatalf("unexpected auth config: %#v", authCfg)
	}
}

func TestAccessLogLabelsPublicRouteAsBypassWhenAuthEnabled(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	var accessLog bytes.Buffer
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{LogOutput: &accessLog})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", recorder.Code, http.StatusOK)
	}
	logOutput := accessLog.String()
	if !strings.Contains(logOutput, `path=/login`) || !strings.Contains(logOutput, `auth="bypass"`) {
		t.Fatalf("public auth-bypass log = %q", logOutput)
	}
	if strings.Contains(logOutput, `auth="disabled"`) {
		t.Fatalf("auth-enabled bypass route was logged as disabled: %q", logOutput)
	}
}

func TestLoadAuthConfigRequiresStrongSessionKey(t *testing.T) {
	cfg := loadTestConfig(t)
	writeAuthConfig(t, cfg, `
auth:
  enabled: true
  providers: [github]
  session_key: "short"
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`)

	_, err := loadAuthConfig(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "session_key") {
		t.Fatalf("expected session_key length error, got %v", err)
	}
}

func TestLoadAuthConfigRequiresHTTPSBaseURLForNonLocalhost(t *testing.T) {
	cfg := loadTestConfig(t)
	writeAuthConfig(t, cfg, `
auth:
  enabled: true
  providers: [github]
  base_url: "http://dbrain.example.test"
  session_key: "test-session-key-32-characters-long"
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`)

	_, err := loadAuthConfig(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https base_url error, got %v", err)
	}
}

func TestAuthEnabledProtectsAppRoutesAndLoginIsPublic(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	t.Run("api gets json 401", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
		req.Header.Set("Accept", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"/login"`) {
			t.Fatalf("expected login redirect hint, got %s", rec.Body.String())
		}
	})

	t.Run("browser gets login redirect", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "text/html")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
		}
		if location := rec.Header().Get("Location"); location != "/login?return_to=%2F" {
			t.Fatalf("unexpected redirect location %q", location)
		}
	})

	t.Run("non api accept json gets login redirect", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
		}
		if location := rec.Header().Get("Location"); location != "/login?return_to=%2F" {
			t.Fatalf("unexpected redirect location %q", location)
		}
	})

	t.Run("login renders github option", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `href="/auth/github"`) || !strings.Contains(body, "Continue with GitHub") {
			t.Fatalf("expected GitHub login option, got %s", body)
		}
	})
}

func TestAuthEnabledProtectsKnownNonMCPRoutes(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	tests := []struct {
		name   string
		method string
		target string
		api    bool
	}{
		{name: "root app", method: http.MethodGet, target: "/"},
		{name: "static app route", method: http.MethodGet, target: "/items/item:test"},
		{name: "embedded asset route", method: http.MethodGet, target: "/assets/index.js"},
		{name: "bootstrap", method: http.MethodGet, target: "/api/bootstrap", api: true},
		{name: "search", method: http.MethodGet, target: "/api/search?q=memory", api: true},
		{name: "get", method: http.MethodGet, target: "/api/get?key=item:test", api: true},
		{name: "whats new", method: http.MethodGet, target: "/api/whats-new?since=24h", api: true},
		{name: "backlog stats", method: http.MethodGet, target: "/api/stats/backlog", api: true},
		{name: "activity stats", method: http.MethodGet, target: "/api/stats/activity", api: true},
		{name: "source activity stats", method: http.MethodGet, target: "/api/stats/source-activity", api: true},
		{name: "scheduler sync all", method: http.MethodGet, target: "/api/scheduler/sync-all", api: true},
		{name: "doctor full disk access", method: http.MethodGet, target: "/api/doctor/full-disk-access", api: true},
		{name: "removed ask endpoint", method: http.MethodGet, target: "/api/ask", api: true},
		{name: "research", method: http.MethodPost, target: "/api/research", api: true},
		{name: "research synthesize", method: http.MethodPost, target: "/api/research/synthesize", api: true},
		{name: "chat transcripts", method: http.MethodPost, target: "/api/chat/transcripts", api: true},
		{name: "links", method: http.MethodPost, target: "/api/links", api: true},
		{name: "tag", method: http.MethodPost, target: "/api/tag", api: true},
		{name: "media signed url", method: http.MethodGet, target: "/api/media/signed-url?id=1", api: true},
		{name: "media asset", method: http.MethodGet, target: "/media/asset/1"},
		{name: "media asset head", method: http.MethodHead, target: "/media/asset/1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.target, nil)
			if tt.api {
				req.Header.Set("Accept", "application/json")
			} else {
				req.Header.Set("Accept", "text/html")
			}
			handler.ServeHTTP(rec, req)

			if tt.api {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("expected api route to return 401 before handler execution, got %d: %s", rec.Code, rec.Body.String())
				}
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode auth response: %v", err)
				}
				if body["error"] != "authentication required" || body["redirect"] != "/login" {
					t.Fatalf("unexpected auth response body: %#v", body)
				}
				return
			}

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("expected browser route to redirect before handler execution, got %d: %s", rec.Code, rec.Body.String())
			}
			wantLocation := "/login"
			if returnTo := cleanReturnTo(req.URL.RequestURI()); returnTo != "" {
				wantLocation += "?return_to=" + url.QueryEscape(returnTo)
			}
			if location := rec.Header().Get("Location"); location != wantLocation {
				t.Fatalf("unexpected redirect location %q, want %q", location, wantLocation)
			}
		})
	}
}

func TestWhatsNewEndpointRequiresAuthWhenWebAuthEnabled(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/whats-new?since=24h", nil)
	req.Header.Set("Accept", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected api route to return 401 before handler execution, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if body["error"] != "authentication required" || body["redirect"] != "/login" {
		t.Fatalf("unexpected auth response body: %#v", body)
	}
}

func TestServiceAuthAllowsDoctorFullDiskAccessProbeOnceAndRejectsReplay(t *testing.T) {
	t.Setenv("DBRAIN_AUTH_BASE_URL", "")
	t.Setenv("DBRAIN_AUTH_ENABLED", "")
	t.Setenv("DBRAIN_AUTH_GITHUB_CLIENT_ID", "")
	t.Setenv("DBRAIN_AUTH_GITHUB_CLIENT_SECRET", "")
	t.Setenv("DBRAIN_AUTH_PROVIDERS", "")
	t.Setenv("DBRAIN_AUTH_SESSION_KEY", "")

	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	probePath := filepath.Join(t.TempDir(), "NoteStore.sqlite")
	if err := os.WriteFile(probePath, []byte("notes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{
		FullDiskAccessPath: probePath,
	})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions: %v", err)
	}
	header, err := serviceauth.SignHeader(http.MethodGet, "/api/doctor/full-disk-access", "test-session-key-32-characters-long", time.Now())
	if err != nil {
		t.Fatalf("SignHeader: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/doctor/full-disk-access", nil)
	req.Header.Set(serviceauth.HeaderName, header)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response FullDiskAccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode full disk access response: %v", err)
	}
	if !response.OK || !response.Readable || response.Path != probePath {
		t.Fatalf("unexpected response: %#v", response)
	}

	replay := httptest.NewRecorder()
	replayReq := httptest.NewRequest(http.MethodGet, "/api/doctor/full-disk-access", nil)
	replayReq.Header.Set(serviceauth.HeaderName, header)
	handler.ServeHTTP(replay, replayReq)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("expected replay 401, got %d: %s", replay.Code, replay.Body.String())
	}
}

func TestValidatePublicAuthConfigRequiresPublicBaseURL(t *testing.T) {
	cfg := loadTestConfig(t)
	writeAuthConfig(t, cfg, `
auth:
  enabled: true
  providers: [github]
  session_key: "test-session-key-32-characters-long"
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`)

	err := ValidatePublicAuthConfig(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "public https origin") {
		t.Fatalf("expected public base URL error, got %v", err)
	}

	writeAuthConfig(t, cfg, validAuthConfigYAML())
	if err := ValidatePublicAuthConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ValidatePublicAuthConfig with public base URL: %v", err)
	}
}

func TestRequirePublicAuthConfigRequiresEnabledAuth(t *testing.T) {
	cfg := loadTestConfig(t)

	err := RequirePublicAuthConfig(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected enabled web auth error")
	}
	for _, want := range []string{"auth.enabled=true", "--tsnet-funnel", "--web"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain actionable guidance %q", err, want)
		}
	}
}

func TestRequirePublicAuthConfigAcceptsEnabledPublicAuth(t *testing.T) {
	cfg := loadTestConfig(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())

	if err := RequirePublicAuthConfig(context.Background(), cfg); err != nil {
		t.Fatalf("RequirePublicAuthConfig: %v", err)
	}
}

func TestValidatePublicAuthConfigAllowsDisabledAuth(t *testing.T) {
	cfg := loadTestConfig(t)

	if err := ValidatePublicAuthConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ValidatePublicAuthConfig with disabled auth: %v", err)
	}
}

func TestNewHandlerWithOptionsLogsAuthStatus(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		cfg, st := openTestStore(t)
		var out bytes.Buffer
		if _, err := NewHandlerWithOptions(cfg, st, HandlerOptions{LogOutput: &out}); err != nil {
			t.Fatalf("NewHandlerWithOptions: %v", err)
		}
		if !strings.Contains(out.String(), "WARNING web auth disabled") || !strings.Contains(out.String(), "unauthenticated") {
			t.Fatalf("unexpected auth startup log: %q", out.String())
		}
	})

	t.Run("enabled", func(t *testing.T) {
		cfg, st := openTestStore(t)
		writeAuthConfig(t, cfg, validAuthConfigYAML())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out bytes.Buffer
		if _, err := NewHandlerWithOptions(cfg, st, HandlerOptions{Context: ctx, LogOutput: &out}); err != nil {
			t.Fatalf("NewHandlerWithOptions: %v", err)
		}
		if !strings.Contains(out.String(), "Web auth enabled") || !strings.Contains(out.String(), "sessions are in-memory") {
			t.Fatalf("unexpected auth startup log: %q", out.String())
		}
	})
}

func TestAuthSessionStoreCleanupExpired(t *testing.T) {
	base := time.Date(2026, time.May, 14, 12, 0, 0, 0, time.UTC)
	now := base
	sessions := newAuthSessionStore()
	sessions.now = func() time.Time { return now }

	expired, err := sessions.create(authUser{Provider: authProviderGitHub, ID: "1", Username: "old"}, time.Hour)
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	now = base.Add(30 * time.Minute)
	active, err := sessions.create(authUser{Provider: authProviderGitHub, ID: "2", Username: "active"}, time.Hour)
	if err != nil {
		t.Fatalf("create active session: %v", err)
	}

	removed := sessions.cleanupExpired(base.Add(89 * time.Minute))
	if removed != 1 {
		t.Fatalf("cleanup removed %d sessions, want 1", removed)
	}
	if _, ok := sessions.get(expired.Token); ok {
		t.Fatalf("expired session still present")
	}
	if got, ok := sessions.get(active.Token); !ok || got.User.Username != "active" {
		t.Fatalf("active session missing after cleanup: %#v ok=%v", got, ok)
	}
}

func TestAuthSessionRequiresCurrentGitHubApproval(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	if _, _, err := st.ApproveGitHubAuthUser(context.Background(), "darron"); err != nil {
		t.Fatalf("approve github auth user: %v", err)
	}
	authCfg, err := loadAuthConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	manager, err := newAuthManager(authCfg, st)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	session, err := manager.sessions.create(authUser{Provider: authProviderGitHub, Username: "darron"}, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	handler := manager.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.Token})
	ok := httptest.NewRecorder()
	handler.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Fatalf("expected approved session to pass, got %d: %s", ok.Code, ok.Body.String())
	}

	if _, removed, err := st.RemoveGitHubAuthUser(context.Background(), "darron"); err != nil {
		t.Fatalf("remove github auth user: %v", err)
	} else if !removed {
		t.Fatalf("expected github auth user to be removed")
	}

	revokedReq := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	revokedReq.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.Token})
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, revokedReq)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("expected removed user session to be rejected, got %d: %s", revoked.Code, revoked.Body.String())
	}
}

func TestGitHubOAuthCallbackCreatesAuthenticatedSession(t *testing.T) {
	fakeGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				t.Errorf("expected token POST, got %s", r.Method)
				http.Error(w, "wrong method", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"bearer"}`))
		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
				t.Errorf("expected bearer token, got %q", got)
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":12345,"login":"darron","email":"darron@example.test","name":"Darron","avatar_url":"https://avatars.example.test/darron.png"}`))
		default:
			t.Errorf("unexpected fake GitHub path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer fakeGitHub.Close()

	oldEndpoint := githubOAuthEndpoint
	oldUserURL := githubUserAPIURL
	githubOAuthEndpoint = oauth2.Endpoint{
		AuthURL:  fakeGitHub.URL + "/login/oauth/authorize",
		TokenURL: fakeGitHub.URL + "/login/oauth/access_token",
	}
	githubUserAPIURL = fakeGitHub.URL + "/user"
	t.Cleanup(func() {
		githubOAuthEndpoint = oldEndpoint
		githubUserAPIURL = oldUserURL
	})

	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	approvedBefore, created, err := st.ApproveGitHubAuthUser(context.Background(), "darron")
	if err != nil {
		t.Fatalf("approve github auth user: %v", err)
	}
	if !created || approvedBefore.GitHubID != "" {
		t.Fatalf("expected newly approved unbound user, got created=%v user=%#v", created, approvedBefore)
	}
	var accessLog bytes.Buffer
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{LogOutput: &accessLog})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	begin := httptest.NewRecorder()
	handler.ServeHTTP(begin, httptest.NewRequest(http.MethodGet, "/auth/github", nil))
	if begin.Code != http.StatusSeeOther {
		t.Fatalf("expected begin redirect, got %d: %s", begin.Code, begin.Body.String())
	}
	redirectURL, err := url.Parse(begin.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse begin redirect: %v", err)
	}
	state := redirectURL.Query().Get("state")
	if state == "" {
		t.Fatalf("expected OAuth state in redirect %q", begin.Header().Get("Location"))
	}
	if scope := redirectURL.Query().Get("scope"); strings.Contains(scope, "user:email") {
		t.Fatalf("GitHub OAuth scope should not request private email access, got %q", scope)
	}

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=ok&state="+url.QueryEscape(state), nil)
	for _, cookie := range begin.Result().Cookies() {
		callbackReq.AddCookie(cookie)
	}
	handler.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("expected callback redirect, got %d: %s", callback.Code, callback.Body.String())
	}
	if location := callback.Header().Get("Location"); location != "/" {
		t.Fatalf("unexpected callback redirect %q", location)
	}

	var sessionCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == authSessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected session cookie in callback response")
	}

	bootstrap := httptest.NewRecorder()
	bootstrapReq := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	bootstrapReq.AddCookie(sessionCookie)
	handler.ServeHTTP(bootstrap, bootstrapReq)
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("expected authenticated bootstrap 200, got %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var response BootstrapResponse
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if !response.Auth.Enabled || response.Auth.Provider != authProviderGitHub || response.Auth.User == nil || response.Auth.User.Username != "darron" {
		t.Fatalf("unexpected auth bootstrap info: %#v", response.Auth)
	}
	logOutput := accessLog.String()
	for _, value := range []string{"web request", "method=GET", "path=/api/bootstrap", "status=200", "duration=", "db_max_open=", "db_open=", "db_in_use=", "db_idle=", "db_wait_count=", "db_wait_duration=", `auth="github"`, `identity="darron"`} {
		if !strings.Contains(logOutput, value) {
			t.Fatalf("expected authenticated web access log to contain %q, got %q", value, logOutput)
		}
	}
	if strings.Contains(logOutput, sessionCookie.Value) || strings.Contains(logOutput, "fake-token") {
		t.Fatalf("web access log exposed secret material: %q", logOutput)
	}
	approvedAfter, found, err := st.GetGitHubAuthUserByUsername(context.Background(), "DARRON")
	if err != nil {
		t.Fatalf("load approved github auth user: %v", err)
	}
	if !found {
		t.Fatalf("expected approved github auth user after login")
	}
	if approvedAfter.GitHubID != "12345" || approvedAfter.Email != "darron@example.test" || approvedAfter.Name != "Darron" || approvedAfter.AvatarURL == "" || approvedAfter.LastLoginAt == "" {
		t.Fatalf("expected first login to bind github id and profile fields, got %#v", approvedAfter)
	}

	logoutGet := httptest.NewRecorder()
	logoutGetReq := httptest.NewRequest(http.MethodGet, "/logout", nil)
	logoutGetReq.AddCookie(sessionCookie)
	handler.ServeHTTP(logoutGet, logoutGetReq)
	if logoutGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected logout GET to be rejected, got %d: %s", logoutGet.Code, logoutGet.Body.String())
	}

	logout := httptest.NewRecorder()
	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	handler.ServeHTTP(logout, logoutReq)
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/login" {
		t.Fatalf("unexpected logout response %d location %q body %s", logout.Code, logout.Header().Get("Location"), logout.Body.String())
	}

	afterLogout := httptest.NewRecorder()
	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	afterLogoutReq.AddCookie(sessionCookie)
	handler.ServeHTTP(afterLogout, afterLogoutReq)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("expected old session to be invalid after logout, got %d: %s", afterLogout.Code, afterLogout.Body.String())
	}
}

func TestGitHubOAuthCallbackRejectsUnapprovedValidUser(t *testing.T) {
	fakeGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"bearer"}`))
		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
				t.Errorf("expected bearer token, got %q", got)
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":999,"login":"intruder","email":"intruder@example.test"}`))
		default:
			t.Errorf("unexpected fake GitHub path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer fakeGitHub.Close()

	oldEndpoint := githubOAuthEndpoint
	oldUserURL := githubUserAPIURL
	githubOAuthEndpoint = oauth2.Endpoint{
		AuthURL:  fakeGitHub.URL + "/login/oauth/authorize",
		TokenURL: fakeGitHub.URL + "/login/oauth/access_token",
	}
	githubUserAPIURL = fakeGitHub.URL + "/user"
	t.Cleanup(func() {
		githubOAuthEndpoint = oldEndpoint
		githubUserAPIURL = oldUserURL
	})

	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	begin := httptest.NewRecorder()
	handler.ServeHTTP(begin, httptest.NewRequest(http.MethodGet, "/auth/github", nil))
	if begin.Code != http.StatusSeeOther {
		t.Fatalf("expected begin redirect, got %d: %s", begin.Code, begin.Body.String())
	}
	redirectURL, err := url.Parse(begin.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse begin redirect: %v", err)
	}
	state := redirectURL.Query().Get("state")
	if state == "" {
		t.Fatalf("expected OAuth state in redirect %q", begin.Header().Get("Location"))
	}

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=ok&state="+url.QueryEscape(state), nil)
	for _, cookie := range begin.Result().Cookies() {
		callbackReq.AddCookie(cookie)
	}
	handler.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusForbidden {
		t.Fatalf("expected unapproved callback 403, got %d: %s", callback.Code, callback.Body.String())
	}
	if !strings.Contains(callback.Body.String(), "not approved") {
		t.Fatalf("expected not approved message, got %s", callback.Body.String())
	}
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == authSessionCookieName && cookie.Value != "" {
			t.Fatalf("unexpected session cookie for unapproved user: %#v", cookie)
		}
	}
}

func TestGitHubOAuthCallbackRejectsStateMismatch(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	begin := httptest.NewRecorder()
	handler.ServeHTTP(begin, httptest.NewRequest(http.MethodGet, "/auth/github", nil))
	if begin.Code != http.StatusSeeOther {
		t.Fatalf("expected begin redirect, got %d: %s", begin.Code, begin.Body.String())
	}

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=ok&state=tampered", nil)
	for _, cookie := range begin.Result().Cookies() {
		callbackReq.AddCookie(cookie)
	}
	handler.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusBadRequest || !strings.Contains(callback.Body.String(), "invalid OAuth state") {
		t.Fatalf("expected invalid state 400, got %d: %s", callback.Code, callback.Body.String())
	}
}

func TestGitHubSessionCreationRejectsUsersOutsideApprovedDatabase(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	authCfg, err := loadAuthConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	manager, err := newAuthManager(authCfg, st)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	_, err = manager.createSessionForGitHubUser(context.Background(), githubOAuthUser{
		ID:    json.Number("999"),
		Login: "other",
	})
	if !errors.Is(err, errAuthAccessDenied) {
		t.Fatalf("expected access denied, got %v", err)
	}
}

func TestGitHubSessionCreationMatchesApprovedUsernameCaseInsensitive(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	if _, _, err := st.ApproveGitHubAuthUser(context.Background(), "@Darron"); err != nil {
		t.Fatalf("approve github auth user: %v", err)
	}
	authCfg, err := loadAuthConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	manager, err := newAuthManager(authCfg, st)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	session, err := manager.createSessionForGitHubUser(context.Background(), githubOAuthUser{
		ID:    json.Number("777"),
		Login: "dArRoN",
		Email: "case@example.test",
	})
	if err != nil {
		t.Fatalf("create session for approved mixed-case user: %v", err)
	}
	if session.User.ID != "777" || session.User.Username != "dArRoN" || session.User.Email != "case@example.test" {
		t.Fatalf("unexpected session user: %#v", session.User)
	}
	approved, found, err := st.GetGitHubAuthUserByUsername(context.Background(), "DARRON")
	if err != nil {
		t.Fatalf("load approved github auth user: %v", err)
	}
	if !found || approved.GitHubID != "777" || approved.GitHubUsernameNormalized != "darron" {
		t.Fatalf("expected case-insensitive match to bind approved user, got found=%v user=%#v", found, approved)
	}
}

func TestIsValidReturnURL(t *testing.T) {
	for _, value := range []string{"/", "/search?q=memory", "/foo/bar"} {
		if !isValidReturnURL(value) {
			t.Fatalf("expected valid return URL %q", value)
		}
	}
	for _, value := range []string{"", "https://example.test", "//example.test", "foo", "/\\evil"} {
		if isValidReturnURL(value) {
			t.Fatalf("expected invalid return URL %q", value)
		}
	}
}

func loadTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	return cfg
}

func writeAuthConfig(t *testing.T, cfg config.Config, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfg.RootDir, "config.yaml"), []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("write auth config: %v", err)
	}
}

func validAuthConfigYAML() string {
	return `
auth:
  enabled: true
  providers: [github]
  base_url: "https://example.test"
  session_key: "test-session-key-32-characters-long"
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`
}
