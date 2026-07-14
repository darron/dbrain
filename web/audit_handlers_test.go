package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/httpsecurity"
	"github.com/darron/dbrain/internal/serviceauth"
)

type auditWebTestStore struct {
	mu      sync.Mutex
	reports []audit.Report
	saveHit chan struct{}
	saveGo  chan struct{}
	err     error
	limits  []int
}

func (s *auditWebTestStore) Latest(profile audit.Profile) (*audit.Report, error) {
	history, err := s.History(profile, 1)
	if err != nil || len(history) == 0 {
		return nil, err
	}
	report := history[0]
	return &report, nil
}

func (s *auditWebTestStore) History(profile audit.Profile, limit int) ([]audit.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = append(s.limits, limit)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]audit.Report, 0, limit)
	for index := len(s.reports) - 1; index >= 0 && len(out) < limit; index-- {
		if s.reports[index].Profile == profile {
			out = append(out, s.reports[index])
		}
	}
	return out, nil
}

func (s *auditWebTestStore) Save(report audit.Report) error {
	if s.saveHit != nil {
		select {
		case s.saveHit <- struct{}{}:
		default:
		}
	}
	if s.saveGo != nil {
		<-s.saveGo
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.reports = append(s.reports, report)
	return nil
}

func auditWebTestReport(t *testing.T, profile audit.Profile, completed time.Time, suffix string) audit.Report {
	t.Helper()
	report, err := audit.Run(t.Context(), audit.Request{Profile: profile}, audit.Dependencies{
		Features: audit.Features{Layout: "xdg", ConfigSource: "default", ConfigVerified: true, DatabaseOpenedQueryOnly: true},
		Runtime:  audit.RuntimeVersion{GitStatus: "unknown"},
		Clock:    func() time.Time { return completed.UTC() },
	})
	if err != nil {
		t.Fatalf("build audit test report: %v", err)
	}
	report.AuditID = "audit_20260714T120000.000000000Z_" + suffix
	if err := audit.ValidateReport(report); err != nil {
		t.Fatalf("validate audit test report: %v", err)
	}
	return report
}

func TestAuditRoutesFailClosedWithoutWebAuth(t *testing.T) {
	cfg, st := openTestStore(t)
	called := 0
	store := &auditWebTestStore{}
	coordinator := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			called++
			return auditWebTestReport(t, audit.ProfileFast, time.Now().UTC(), "deadbeef"), nil
		},
		Reports: store,
	})
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{AuditReports: store, AuditRuns: coordinator})
	if err != nil {
		t.Fatal(err)
	}
	for _, requestPath := range []string{
		"/api/audit", "/api/audit/", "/api/audit/latest?profile=standard",
		"/api/audit/history?profile=standard", "/api/audit/run", "/api/audit/runs/unknown",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404; body=%s", requestPath, rec.Code, rec.Body.String())
		}
	}
	if called != 0 {
		t.Fatalf("auth-disabled routes called runner %d times", called)
	}
}

func TestAuditRoutesRequireBrowserSessionAndExcludeServiceAndShare(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	store := &auditWebTestStore{}
	coordinator := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{Reports: store})
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{AuditReports: store, AuditRuns: coordinator})
	if err != nil {
		t.Fatal(err)
	}

	for _, requestPath := range []string{"/api/audit/latest?profile=standard", "/api/audit/run"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		req.Header.Set("Accept", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d, want 401", requestPath, rec.Code)
		}
	}

	header, err := serviceauth.SignHeader(http.MethodGet, "/api/audit/latest", "test-session-key-32-characters-long", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/latest?profile=standard", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set(serviceauth.HeaderName, header)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("service-auth audit = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/share/api/audit/latest", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("share-prefixed audit = %d, want 404", rec.Code)
	}
}

func TestAuditAuthenticatedSessionSucceedsAndOriginGuardRejectsCrossOriginPOST(t *testing.T) {
	cfg, st := openTestStore(t)
	writeAuthConfig(t, cfg, validAuthConfigYAML())
	if _, _, err := st.ApproveGitHubAuthUser(t.Context(), "darron"); err != nil {
		t.Fatal(err)
	}
	authCfg, err := loadAuthConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newAuthManager(authCfg, st)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.sessions.create(authUser{Provider: authProviderGitHub, Username: "darron"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &auditWebTestStore{reports: []audit.Report{auditWebTestReport(t, audit.ProfileStandard, now, "77777777")}}
	runs := NewAuditRunCoordinator(t.Context(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			return auditWebTestReport(t, audit.ProfileFast, now, "88888888"), nil
		},
		Reports: store,
		Now:     func() time.Time { return now },
	})
	s := &server{auth: manager, auditReports: store, auditRuns: runs, auditNow: func() time.Time { return now }}
	handler := httpsecurity.OriginGuard(s.newMux())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/latest?profile=standard", nil)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.Token})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated latest = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/audit/run", strings.NewReader(`{"profile":"fast"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.Token})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/audit/run", strings.NewReader(`{"profile":"fast"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://dbrain.example.test")
	req.Host = "dbrain.example.test"
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.Token})
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("same-origin authenticated POST = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAuditLatestAndHistoryAreExactProfileBoundedReads(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &auditWebTestStore{reports: []audit.Report{
		auditWebTestReport(t, audit.ProfileFast, now.Add(-time.Hour), "11111111"),
		auditWebTestReport(t, audit.ProfileStandard, now.Add(-13*time.Hour), "22222222"),
		auditWebTestReport(t, audit.ProfileStandard, now.Add(-time.Hour), "33333333"),
	}}
	runCalls := 0
	runs := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			runCalls++
			return audit.Report{}, errors.New("GET must not run")
		},
		Reports:          store,
		SyncInterval:     30 * time.Minute,
		StandardInterval: 4 * time.Hour,
		Now:              func() time.Time { return now },
	})
	s := &server{auditReports: store, auditRuns: runs, auditSyncInterval: 30 * time.Minute, auditStandardInterval: 4 * time.Hour, auditNow: func() time.Time { return now }}

	rec := httptest.NewRecorder()
	s.handleAuditLatest(rec, httptest.NewRequest(http.MethodGet, "/api/audit/latest?profile=standard", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"audit_id": "audit_20260714T120000.000000000Z_33333333"`) || !strings.Contains(rec.Body.String(), `"status": "current"`) {
		t.Fatalf("latest = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleAuditHistory(rec, httptest.NewRequest(http.MethodGet, "/api/audit/history?profile=standard&limit=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("history = %d %s", rec.Code, rec.Body.String())
	}
	var history AuditHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.Profile != audit.ProfileStandard || len(history.History) != 1 || history.History[0].AuditID != "audit_20260714T120000.000000000Z_33333333" {
		t.Fatalf("history = %#v", history)
	}
	if runCalls != 0 {
		t.Fatalf("audit GET routes started %d runs", runCalls)
	}
	encoded, _ := json.Marshal(history)
	if bytes.Contains(encoded, []byte(`"checks"`)) || bytes.Contains(encoded, []byte(`"boundary"`)) {
		t.Fatalf("history is not compact: %s", encoded)
	}

	for _, requestPath := range []string{
		"/api/audit/latest?profile=deep", "/api/audit/latest?profile=", "/api/audit/latest?profile=standard&extra=1",
		"/api/audit/history?profile=standard&limit=0", "/api/audit/history?profile=standard&limit=101",
		"/api/audit/history?profile=standard&limit=garbage", "/api/audit/history?profile=standard&limit=",
		"/api/audit/history?profile=standard;limit=20",
	} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		if strings.Contains(requestPath, "history") {
			s.handleAuditHistory(rec, req)
		} else {
			s.handleAuditLatest(rec, req)
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", requestPath, rec.Code)
		}
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request){s.handleAuditLatest, s.handleAuditHistory} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/audit/latest", nil)
		req.URL.RawQuery = "profile=%zz"
		handler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("malformed query = %d, want 400", rec.Code)
		}
	}
}

func TestAuditReadQueriesRequireBoundedCanonicalEncoding(t *testing.T) {
	s := &server{auditReports: &auditWebTestStore{}}
	tests := []struct {
		name    string
		history bool
		raw     string
		force   bool
		want    int
	}{
		{name: "default latest", want: http.StatusOK},
		{name: "default history", history: true, want: http.StatusOK},
		{name: "latest fast", raw: "profile=fast", want: http.StatusOK},
		{name: "history profile then limit", history: true, raw: "profile=standard&limit=20", want: http.StatusOK},
		{name: "history limit then profile", history: true, raw: "limit=1&profile=fast", want: http.StatusOK},
		{name: "bare question", force: true, want: http.StatusBadRequest},
		{name: "over byte cap", raw: "profile=standard&" + strings.Repeat("x", maxAuditReadQueryBytes), want: http.StatusBadRequest},
		{name: "leading separator", raw: "&profile=fast", want: http.StatusBadRequest},
		{name: "trailing separator", raw: "profile=fast&", want: http.StatusBadRequest},
		{name: "double separator", raw: "profile=fast&&limit=1", history: true, want: http.StatusBadRequest},
		{name: "literal leading whitespace", raw: "profile= fast", want: http.StatusBadRequest},
		{name: "literal trailing whitespace", raw: "profile=fast ", want: http.StatusBadRequest},
		{name: "plus whitespace", raw: "profile=+fast", want: http.StatusBadRequest},
		{name: "encoded whitespace", raw: "profile=%20fast", want: http.StatusBadRequest},
		{name: "encoded key alternate", raw: "pro%66ile=fast", want: http.StatusBadRequest},
		{name: "encoded value alternate", raw: "profile=f%61st", want: http.StatusBadRequest},
		{name: "encoded integer alternate", raw: "limit=%31", history: true, want: http.StatusBadRequest},
		{name: "zero-padded integer", raw: "limit=01", history: true, want: http.StatusBadRequest},
		{name: "signed integer", raw: "limit=+1", history: true, want: http.StatusBadRequest},
		{name: "malformed percent", raw: "profile=%zz", want: http.StatusBadRequest},
		{name: "semicolon", raw: "profile=fast;limit=1", history: true, want: http.StatusBadRequest},
		{name: "empty profile", raw: "profile=", want: http.StatusBadRequest},
		{name: "empty limit", raw: "limit=", history: true, want: http.StatusBadRequest},
		{name: "duplicate profile", raw: "profile=fast&profile=standard", want: http.StatusBadRequest},
		{name: "duplicate limit", raw: "limit=1&limit=2", history: true, want: http.StatusBadRequest},
		{name: "unknown key", raw: "other=fast", want: http.StatusBadRequest},
		{name: "encoded equals", raw: "profile%3Dfast", want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			path := "/api/audit/latest"
			if test.history {
				path = "/api/audit/history"
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.URL.RawQuery = test.raw
			req.URL.ForceQuery = test.force
			if test.history {
				s.handleAuditHistory(rec, req)
			} else {
				s.handleAuditLatest(rec, req)
			}
			if rec.Code != test.want {
				t.Fatalf("raw=%q force=%t status=%d want=%d body=%s", test.raw, test.force, rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestAuditReadWireFormsDefaultsAndSanitizedFailures(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &auditWebTestStore{}
	s := &server{auditReports: store, auditSyncInterval: time.Hour, auditStandardInterval: 4 * time.Hour, auditNow: func() time.Time { return now }}
	rec := httptest.NewRecorder()
	s.handleAuditLatest(rec, httptest.NewRequest(http.MethodGet, "/api/audit/latest", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{
  "report": null,
  "freshness": {
    "status": "unknown",
    "reason": "not_found",
    "deadline_seconds": 43200
  }
}` {
		t.Fatalf("absent latest = %d %s", rec.Code, rec.Body.String())
	}

	store.reports = []audit.Report{auditWebTestReport(t, audit.ProfileStandard, now.Add(-13*time.Hour), "99999999")}
	rec = httptest.NewRecorder()
	s.handleAuditLatest(rec, httptest.NewRequest(http.MethodGet, "/api/audit/latest?profile=standard", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reason": "stale"`) || !strings.Contains(rec.Body.String(), `"age_seconds": 46800`) {
		t.Fatalf("stale latest = %d %s", rec.Code, rec.Body.String())
	}

	store.reports = nil
	rec = httptest.NewRecorder()
	s.handleAuditHistory(rec, httptest.NewRequest(http.MethodGet, "/api/audit/history", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"history": []`) {
		t.Fatalf("empty history = %d %s", rec.Code, rec.Body.String())
	}
	store.mu.Lock()
	lastLimit := store.limits[len(store.limits)-1]
	store.mu.Unlock()
	if lastLimit != 20 {
		t.Fatalf("default history limit = %d, want 20", lastLimit)
	}

	store.err = errors.New("secret /private/path source_key=x")
	rec = httptest.NewRecorder()
	s.handleAuditLatest(rec, httptest.NewRequest(http.MethodGet, "/api/audit/latest", nil))
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), "source_key") {
		t.Fatalf("read error leaked = %d %s", rec.Code, rec.Body.String())
	}

	store.err = nil
	invalid := auditWebTestReport(t, audit.ProfileStandard, now, "aaaaaaaa")
	invalid.Checks = nil
	store.reports = []audit.Report{invalid}
	rec = httptest.NewRecorder()
	s.handleAuditLatest(rec, httptest.NewRequest(http.MethodGet, "/api/audit/latest", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid persisted report = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAuditRunRequestBoundsDedupConflictRateAndOrigin(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := now
	runStarted := make(chan audit.Profile, 2)
	release := make(chan struct{})
	store := &auditWebTestStore{}
	runs := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(ctx context.Context, profile audit.Profile) (audit.Report, error) {
			runStarted <- profile
			select {
			case <-release:
				return auditWebTestReport(t, profile, now, "44444444"), nil
			case <-ctx.Done():
				return audit.Report{}, ctx.Err()
			}
		},
		Reports: store,
		Now:     func() time.Time { return clock },
	})
	s := &server{auditReports: store, auditRuns: runs}

	post := func(body string, contentType string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/audit/run", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		s.handleAuditRun(rec, req)
		return rec
	}
	first := post(`{"profile":"standard"}`, "application/json")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first = %d %s", first.Code, first.Body.String())
	}
	var accepted AuditRunStatusResponse
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.AuditID == "" || accepted.State != AuditRunRunning || accepted.StatusPath != "/api/audit/runs/"+accepted.AuditID {
		t.Fatalf("accepted = %#v", accepted)
	}
	if !regexp.MustCompile(`^run_[0-9a-f]{32}$`).MatchString(accepted.AuditID) {
		t.Fatalf("outer run ID is not opaque fixed shape: %q", accepted.AuditID)
	}
	<-runStarted

	duplicate := post(`{"profile":"standard"}`, "application/json; charset=utf-8")
	if duplicate.Code != http.StatusAccepted || !strings.Contains(duplicate.Body.String(), accepted.AuditID) {
		t.Fatalf("duplicate = %d %s", duplicate.Code, duplicate.Body.String())
	}
	conflict := post(`{"profile":"fast"}`, "application/json")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), accepted.AuditID) || !strings.Contains(conflict.Body.String(), "standard") {
		t.Fatalf("conflict = %d %s", conflict.Code, conflict.Body.String())
	}

	for name, testCase := range map[string][2]string{
		"missing mime": {`{"profile":"fast"}`, ""},
		"wrong mime":   {`{"profile":"fast"}`, "text/plain"},
		"unknown":      {`{"profile":"fast","deep":true}`, "application/json"},
		"trailing":     {`{"profile":"fast"} {}`, "application/json"},
		"invalid":      {`{"profile":"deep"}`, "application/json"},
	} {
		rec := post(testCase[0], testCase[1])
		if rec.Code != http.StatusUnsupportedMediaType && (name == "missing mime" || name == "wrong mime") {
			t.Errorf("%s = %d, want 415", name, rec.Code)
		} else if name != "missing mime" && name != "wrong mime" && rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
	oversized := post(`{"profile":"fast","padding":"`+strings.Repeat("x", 4096)+`"}`, "application/json")
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized = %d %s", oversized.Code, oversized.Body.String())
	}
	close(release)
	waitAuditRunState(t, runs, accepted.AuditID, AuditRunCompleted)
	clock = now.Add(500 * time.Millisecond)

	rateLimited := post(`{"profile":"standard"}`, "application/json")
	if rateLimited.Code != http.StatusTooManyRequests || !strings.Contains(rateLimited.Body.String(), `"retry_after_seconds": 60`) {
		t.Fatalf("rate limited = %d %s", rateLimited.Code, rateLimited.Body.String())
	}
}

func TestAuditHandlerMethodsAndUnknownRunAreBounded(t *testing.T) {
	s := &server{auditReports: &auditWebTestStore{}, auditRuns: NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{})}
	tests := []struct {
		handler func(http.ResponseWriter, *http.Request)
		method  string
		path    string
		want    int
	}{
		{s.handleAuditLatest, http.MethodPost, "/api/audit/latest", http.StatusMethodNotAllowed},
		{s.handleAuditHistory, http.MethodPost, "/api/audit/history", http.StatusMethodNotAllowed},
		{s.handleAuditRun, http.MethodGet, "/api/audit/run", http.StatusMethodNotAllowed},
		{s.handleAuditRunStatus, http.MethodPost, "/api/audit/runs/missing", http.StatusMethodNotAllowed},
		{s.handleAuditRunStatus, http.MethodGet, "/api/audit/runs/missing", http.StatusNotFound},
		{s.handleAuditRunStatus, http.MethodGet, "/api/audit/runs/missing/extra", http.StatusNotFound},
	}
	for _, test := range tests {
		rec := httptest.NewRecorder()
		test.handler(rec, httptest.NewRequest(test.method, test.path, nil))
		if rec.Code != test.want {
			t.Errorf("%s %s = %d, want %d", test.method, test.path, rec.Code, test.want)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/audit/run", strings.NewReader(`{"profile":"fast"}`))
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = "bad=%zz"
	s.handleAuditRun(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed run query = %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/audit/runs/missing", nil)
	req.URL.RawQuery = "bad=%zz"
	s.handleAuditRunStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed status query = %d, want 400", rec.Code)
	}
}

func TestAuditRunPersistsBeforeCompletedAndSanitizesFailures(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &auditWebTestStore{saveHit: make(chan struct{}, 1), saveGo: make(chan struct{})}
	runs := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			return auditWebTestReport(t, audit.ProfileFast, now, "55555555"), nil
		},
		Reports: store,
		Now:     func() time.Time { return now },
	})
	startedResult := runs.Start(audit.ProfileFast)
	started := startedResult.Status
	if startedResult.Kind != AuditRunStarted || started.State != AuditRunRunning {
		t.Fatalf("start = %#v", started)
	}
	<-store.saveHit
	if current, ok := runs.Status(started.AuditID); !ok || current.State != AuditRunRunning {
		t.Fatalf("state before save = %#v, %v", current, ok)
	}
	close(store.saveGo)
	completed := waitAuditRunState(t, runs, started.AuditID, AuditRunCompleted)
	if completed.Report == nil || completed.Report.AuditID == completed.AuditID || completed.Freshness.Status != audit.FreshnessCurrent {
		t.Fatalf("completed = %#v", completed)
	}
	originalSummary := completed.Report.Checks[0].Summary
	completed.Report.Checks[0].Summary = "mutated by caller"
	completed.Report.Checks[0].Evidence["layout"] = "mutated"
	again, ok := runs.Status(started.AuditID)
	if !ok || again.Report == nil || again.Report.Checks[0].Summary != originalSummary || again.Report.Checks[0].Evidence["layout"] == "mutated" {
		t.Fatalf("status caller mutated coordinator report: %#v", again.Report)
	}
	store.mu.Lock()
	persistedSummary := store.reports[0].Checks[0].Summary
	store.mu.Unlock()
	if persistedSummary != originalSummary {
		t.Fatalf("status caller mutated persisted report: %q", persistedSummary)
	}

	failing := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			return audit.Report{}, errors.New("secret /Users/darron/private source_key=x")
		},
		Reports: &auditWebTestStore{},
		Now:     func() time.Time { return now.Add(2 * time.Minute) },
	})
	failedStart := failing.Start(audit.ProfileFast).Status
	failed := waitAuditRunState(t, failing, failedStart.AuditID, AuditRunFailed)
	data, _ := json.Marshal(failed)
	if failed.ErrorCode != AuditRunErrorFailed || bytes.Contains(data, []byte("darron")) || bytes.Contains(data, []byte("source_key")) {
		t.Fatalf("failed response = %s", data)
	}

	persistStore := &auditWebTestStore{err: errors.New("secret /private/report.jsonl")}
	persistFailing := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			return auditWebTestReport(t, audit.ProfileFast, now, "bbbbbbbb"), nil
		},
		Reports: persistStore,
		Now:     func() time.Time { return now.Add(3 * time.Minute) },
	})
	persistStart := persistFailing.Start(audit.ProfileFast).Status
	persistFailed := waitAuditRunState(t, persistFailing, persistStart.AuditID, AuditRunFailed)
	if persistFailed.ErrorCode != AuditRunErrorPersist || persistFailed.Report != nil {
		t.Fatalf("persist failure = %#v", persistFailed)
	}

	invalidStore := &auditWebTestStore{}
	invalidRuns := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			report := auditWebTestReport(t, audit.ProfileFast, now, "cccccccc")
			report.Checks = nil
			return report, nil
		},
		Reports: invalidStore,
		Now:     func() time.Time { return now.Add(4 * time.Minute) },
	})
	invalidStart := invalidRuns.Start(audit.ProfileFast).Status
	invalidFailed := waitAuditRunState(t, invalidRuns, invalidStart.AuditID, AuditRunFailed)
	invalidStore.mu.Lock()
	saved := len(invalidStore.reports)
	invalidStore.mu.Unlock()
	if invalidFailed.ErrorCode != AuditRunErrorFailed || saved != 0 {
		t.Fatalf("invalid report state=%#v saves=%d", invalidFailed, saved)
	}
}

func TestAuditRunRetentionAndFreshnessRecompute(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := now
	runs := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Reports:          &auditWebTestStore{},
		SyncInterval:     time.Hour,
		StandardInterval: 6 * time.Hour,
		Now:              func() time.Time { return clock },
	})
	runs.mu.Lock()
	for index := 0; index < 101; index++ {
		id := "run_" + strings.Repeat("0", 28) + string(rune('a'+index%26)) + string(rune('A'+index/26))
		runs.records[id] = &auditRunRecord{AuditID: id, Profile: audit.ProfileFast, State: AuditRunFailed, TerminalAt: now.Add(time.Duration(index) * time.Second), ErrorCode: AuditRunErrorFailed}
	}
	runs.cleanupLocked(now.Add(101 * time.Second))
	if len(runs.records) != 100 {
		t.Fatalf("terminal retention = %d, want 100", len(runs.records))
	}
	active := &auditRunRecord{AuditID: "run_active", Profile: audit.ProfileFast, State: AuditRunRunning, StartedAt: now.Add(-48 * time.Hour)}
	runs.records[active.AuditID] = active
	runs.active = active
	runs.cleanupLocked(now.Add(25 * time.Hour))
	if _, ok := runs.records[active.AuditID]; !ok {
		t.Fatal("cleanup evicted active run")
	}
	runs.mu.Unlock()

	report := auditWebTestReport(t, audit.ProfileFast, now, "66666666")
	runs.mu.Lock()
	runs.records["run_done"] = &auditRunRecord{AuditID: "run_done", Profile: audit.ProfileFast, State: AuditRunCompleted, StartedAt: now, TerminalAt: now, Report: &report}
	runs.mu.Unlock()
	first, _ := runs.Status("run_done")
	clock = now.Add(3 * time.Hour)
	second, _ := runs.Status("run_done")
	if first.Freshness == nil || second.Freshness == nil || first.Freshness.Status != audit.FreshnessCurrent || second.Freshness.Reason != audit.FreshnessStale {
		t.Fatalf("freshness did not recompute: first=%#v second=%#v", first.Freshness, second.Freshness)
	}
}

func TestAuditRunTerminalTransitionHourlyCleanupAndSharedCoordinator(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	runs := NewAuditRunCoordinator(ctx, AuditRunCoordinatorOptions{
		Runner: func(context.Context, audit.Profile) (audit.Report, error) {
			return audit.Report{}, errors.New("failed")
		},
		Reports:         &auditWebTestStore{},
		Now:             func() time.Time { return time.Unix(0, clock.Load()).UTC() },
		CleanupInterval: time.Millisecond,
	})
	for index := 0; index < 101; index++ {
		id := "run_terminal_" + strconv.Itoa(index)
		record := &auditRunRecord{AuditID: id, Profile: audit.ProfileFast, State: AuditRunRunning, StartedAt: now}
		runs.mu.Lock()
		runs.records[id] = record
		runs.active = record
		runs.mu.Unlock()
		clock.Store(now.Add(time.Duration(index) * time.Second).UnixNano())
		runs.finishFailed(record, AuditRunErrorFailed)
	}
	runs.mu.Lock()
	if len(runs.records) != 100 {
		runs.mu.Unlock()
		t.Fatalf("terminal transition retained %d, want 100", len(runs.records))
	}
	runs.mu.Unlock()

	clock.Store(now.Add(26 * time.Hour).UnixNano())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runs.mu.Lock()
		remaining := len(runs.records)
		runs.mu.Unlock()
		if remaining == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	runs.mu.Lock()
	remaining := len(runs.records)
	runs.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("hourly cleanup left %d expired records", remaining)
	}
	cancel()
	select {
	case <-runs.cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("cleanup loop did not stop with lifecycle context")
	}
	if result := runs.Start(audit.ProfileFast); result.Kind != AuditRunUnavailable {
		t.Fatalf("canceled lifecycle accepted a new run: %#v", result)
	}

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	shared := NewAuditRunCoordinator(context.Background(), AuditRunCoordinatorOptions{
		Runner: func(ctx context.Context, profile audit.Profile) (audit.Report, error) {
			started <- struct{}{}
			select {
			case <-release:
				return auditWebTestReport(t, profile, now, "dddddddd"), nil
			case <-ctx.Done():
				return audit.Report{}, ctx.Err()
			}
		},
		Reports: &auditWebTestStore{},
		Now:     func() time.Time { return now },
	})
	s1 := &server{auditRuns: shared}
	s2 := &server{auditRuns: shared}
	post := func(s *server, profile audit.Profile) AuditRunStatusResponse {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/audit/run", strings.NewReader(`{"profile":"`+string(profile)+`"}`))
		req.Header.Set("Content-Type", "application/json")
		s.handleAuditRun(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("shared POST = %d %s", rec.Code, rec.Body.String())
		}
		var out AuditRunStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := post(s1, audit.ProfileFast)
	<-started
	second := post(s2, audit.ProfileFast)
	if first.AuditID != second.AuditID {
		t.Fatalf("shared coordinators did not deduplicate: %q vs %q", first.AuditID, second.AuditID)
	}
	close(release)
}

func waitAuditRunState(t *testing.T, runs *AuditRunCoordinator, id string, want AuditRunState) AuditRunStatusResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, ok := runs.Status(id)
		if ok && status.State == want {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	status, _ := runs.Status(id)
	t.Fatalf("run %s state = %s, want %s", id, status.State, want)
	return AuditRunStatusResponse{}
}
