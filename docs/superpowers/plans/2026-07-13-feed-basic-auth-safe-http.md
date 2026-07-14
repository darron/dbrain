# Feed Basic Auth Through Safe HTTP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore existing Basic-authenticated feed subscriptions without weakening dbrain's shared outbound HTTP security policy.

**Architecture:** `internal/feedimport` will translate URL userinfo into an HTTP Basic Authorization header before a request reaches `safehttp`. A per-request clone of the HTTP client will retain that header only for exact-origin redirects while delegating redirect validation to the client's existing callback.

**Tech Stack:** Go `net/http`, `net/url`, dbrain `internal/safehttp`, `httptest`, Taskfile verification gates.

## Global Constraints

- Work only in `/private/tmp/dbrain-feed-basic-auth` on `codex/feed-basic-auth`, based on merged `origin/main` commit `b733c78`.
- Do not inspect or mutate the production feed database, real credentials, Converge, or the installed Homebrew binary.
- Do not relax `safehttp` userinfo rejection or private-destination controls.
- Do not add a schema migration, new credential fields, secret references, or CLI flags.
- Preserve existing feed URL normalization, keys, storage, redaction, scheduling, parsing, and import semantics.
- Write and run failing regression tests before changing production code.
- Run `task fmt`, `task lint`, `task test-ci`, and `task build` after code changes.

---

### Task 1: Restore exact-origin Basic Auth feed fetching

**Files:**
- Modify: `internal/feedimport/http.go:45-84`
- Modify: `internal/feedimport/http_test.go:1-115`
- Modify: `CHANGELOG.md:7-35`

**Interfaces:**
- Consumes: `HTTPFetcher.Fetch(context.Context, store.Feed, Options)`, the selected feed URL in `ResolvedURL`/`NormalizedURL`/`URL`, and the configured client's existing `CheckRedirect` callback.
- Produces: `feedRequestClient(*http.Client, time.Duration, *url.URL, bool) *http.Client`, `sameFeedHTTPOrigin(*url.URL, *url.URL) bool`, and sanitized `FetchResult.RequestURL`/`FetchResult.FinalURL` values.

- [ ] **Step 1: Add failing authenticated-feed regression tests**

Add these tests to `internal/feedimport/http_test.go` using synthetic localhost servers with `HTTPFetcherOptions{AllowPrivateNetwork: true}`:

```go
func TestHTTPFetcherMovesBasicAuthOutOfRequestURL(t *testing.T) {
	var username, password string
	var authenticated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, authenticated = r.BasicAuth()
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Private</title></feed>`))
	}))
	defer server.Close()

	target := strings.Replace(server.URL, "://", "://feed:secret@", 1) + "/feed.atom"
	result, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !authenticated || username != "feed" || password != "secret" {
		t.Fatalf("BasicAuth = (%q, %q, %t)", username, password, authenticated)
	}
	if strings.Contains(result.RequestURL, "@") || strings.Contains(result.FinalURL, "@") ||
		strings.Contains(result.RequestURL, "secret") || strings.Contains(result.FinalURL, "secret") {
		t.Fatalf("credential-bearing result URLs: request=%q final=%q", result.RequestURL, result.FinalURL)
	}
}

func TestHTTPFetcherRetainsBasicAuthForSameOriginRedirect(t *testing.T) {
	var finalAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/feed.atom", http.StatusFound)
			return
		}
		finalAuthorization = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Private</title></feed>`))
	}))
	defer server.Close()

	target := strings.Replace(server.URL, "://", "://feed:secret@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if finalAuthorization == "" {
		t.Fatal("same-origin redirect lost Authorization header")
	}
}

func TestHTTPFetcherStripsBasicAuthFromCrossOriginRedirect(t *testing.T) {
	var destinationAuthorization string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuthorization = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Redirected</title></feed>`))
	}))
	defer destination.Close()

	var originAuthorization string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAuthorization = r.Header.Get("Authorization")
		http.Redirect(w, r, destination.URL+"/feed.atom", http.StatusFound)
	}))
	defer origin.Close()

	target := strings.Replace(origin.URL, "://", "://feed:secret@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if originAuthorization == "" {
		t.Fatal("origin did not receive Authorization header")
	}
	if destinationAuthorization != "" {
		t.Fatalf("cross-origin Authorization = %q, want empty", destinationAuthorization)
	}
}

func TestHTTPFetcherStillRejectsUserInfoIntroducedByRedirect(t *testing.T) {
	destinationHits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationHits++
		_, _ = w.Write([]byte(`<feed/>`))
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		location := strings.Replace(destination.URL, "://", "://attacker:secret@", 1) + "/feed.atom"
		http.Redirect(w, r, location, http.StatusFound)
	}))
	defer origin.Close()

	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: origin.URL + "/start", NormalizedURL: origin.URL + "/start", ResolvedURL: origin.URL + "/start"},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil || !safehttp.IsPolicyError(err) {
		t.Fatalf("Fetch error = %v, want safe HTTP policy error", err)
	}
	if destinationHits != 0 {
		t.Fatalf("credential-bearing redirect reached destination %d times", destinationHits)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the regression is red**

Run:

```sh
go test ./internal/feedimport -run 'TestHTTPFetcher(MovesBasicAuth|RetainsBasicAuth|StripsBasicAuth|StillRejectsUserInfo)' -count=1
```

Expected: the first three tests fail with `safe HTTP policy: URL userinfo is not allowed`; the redirect-introduced-userinfo control test passes.

- [ ] **Step 3: Move Basic Auth into a header before safe-HTTP validation**

In `internal/feedimport/http.go`, import `net` and `net/url`. Immediately after request creation, capture and clear URL userinfo before setting request headers:

```go
	credentials := req.URL.User
	req.URL.User = nil
	if credentials != nil {
		password, _ := credentials.Password()
		req.SetBasicAuth(credentials.Username(), password)
	}
	sanitizedTarget := req.URL.String()
```

Replace the current timeout-only client clone with:

```go
	client := feedRequestClient(f.client, opts.Timeout, req.URL, credentials != nil)
```

Record `sanitizedTarget`, not `target`, in `FetchResult.RequestURL`.

Add these helpers below `Fetch`:

```go
func feedRequestClient(client *http.Client, timeout time.Duration, credentialOrigin *url.URL, hasCredentials bool) *http.Client {
	clone := *client
	if clone.Timeout <= 0 {
		clone.Timeout = timeout
	}
	if !hasCredentials {
		return &clone
	}

	originalCheckRedirect := clone.CheckRedirect
	origin := *credentialOrigin
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameFeedHTTPOrigin(&origin, req.URL) {
			req.Header.Del("Authorization")
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(req, via)
		}
		return nil
	}
	return &clone
}

func sameFeedHTTPOrigin(left *url.URL, right *url.URL) bool {
	leftOrigin, leftOK := normalizedFeedHTTPOrigin(left)
	rightOrigin, rightOK := normalizedFeedHTTPOrigin(right)
	return leftOK && rightOK && leftOrigin == rightOrigin
}

func normalizedFeedHTTPOrigin(target *url.URL) (string, bool) {
	if target == nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target.Hostname()), "."))
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", false
	}
	port := target.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), true
}
```

- [ ] **Step 4: Run the focused feed tests twice and verify green behavior**

Run:

```sh
go test ./internal/feedimport -run 'TestHTTPFetcher' -count=2
```

Expected: `ok github.com/darron/dbrain/internal/feedimport`; Basic Auth succeeds, exact-origin redirect behavior is enforced, and existing localhost/conditional-header tests remain green.

- [ ] **Step 5: Document the user-visible regression fix**

Add this bullet under `Security hardening (2026-07-13)` in `CHANGELOG.md`:

```markdown
- **Authenticated feeds**: Basic-auth feed URLs are stripped of userinfo before
  safe-HTTP validation and translated into an Authorization header that is
  retained only across exact-origin redirects.
```

- [ ] **Step 6: Run repository verification gates**

Run:

```sh
task fmt
task lint
task test-ci
task build
git diff --check
```

Expected: formatting completes without a diff, lint reports `0 issues`, the clean race-enabled suite exits 0, the binary builds, and `git diff --check` is silent.

- [ ] **Step 7: Request an independent security review and resolve findings**

Ask a read-only reviewer to inspect the actual checkout and compare the diff to `46fec8c`. Require review of credential decoding, URL sanitization, same-origin normalization, redirect header propagation, preservation of `safehttp.CheckRedirect`, result/error leakage, injected-client behavior, and test validity. Apply only verified findings, adding a failing regression test before any production correction, then repeat Step 6.

- [ ] **Step 8: Commit the verified implementation**

Run:

```sh
git add CHANGELOG.md internal/feedimport/http.go internal/feedimport/http_test.go
git commit -m "fix: preserve authenticated feed fetching"
```

Expected: one implementation commit after the design/plan commits, with no unstaged source changes.
