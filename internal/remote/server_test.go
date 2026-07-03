package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func TestRemoteUserLoggerTeesAndDedupesAuthURLs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logf := newUserLogger(&out)

	logf("open https://login.tailscale.com/a")
	logf("open https://login.tailscale.com/a")
	logf("again https://login.tailscale.com/a.")

	output := out.String()
	if strings.Count(output, "Visit this URL to authenticate dbrain:") != 1 {
		t.Fatalf("expected one deduped auth hint, got %q", output)
	}
	if strings.Count(output, "open https://login.tailscale.com/a") != 1 {
		t.Fatalf("expected duplicate raw tsnet log lines to be suppressed, got %q", output)
	}
	if strings.Count(output, "again https://login.tailscale.com/a.") != 1 {
		t.Fatalf("expected distinct raw tsnet log lines to still be teed, got %q", output)
	}
}

func TestRemoteUserLoggerConcurrentSafe(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logf := newUserLogger(&out)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logf("open https://login.tailscale.com/a")
		}()
	}
	wg.Wait()

	output := out.String()
	if strings.Count(output, "Visit this URL to authenticate dbrain:") != 1 {
		t.Fatalf("expected one deduped auth hint, got %q", output)
	}
	if strings.Count(output, "open https://login.tailscale.com/a") != 1 {
		t.Fatalf("expected duplicate raw tsnet log lines to be suppressed, got %q", output)
	}
}

func TestRemoteURLs(t *testing.T) {
	t.Parallel()

	status := &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}}
	result := URLs(status, Options{
		Web:     true,
		MCP:     true,
		MCPPath: "/mcp",
		TLS:     true,
	})
	if result.WebURL != "https://dbrain.example.ts.net/" {
		t.Fatalf("WebURL = %q", result.WebURL)
	}
	if result.MCPURL != "https://dbrain.example.ts.net/mcp" {
		t.Fatalf("MCPURL = %q", result.MCPURL)
	}

	status = &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "dbrain.tailnet.ts.net."}}
	result = URLs(status, Options{MCP: true, MCPPath: "/brain", TLS: false})
	if result.MCPURL != "http://dbrain.tailnet.ts.net/brain" {
		t.Fatalf("MCPURL = %q", result.MCPURL)
	}

	status = &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}}
	result = URLs(status, Options{Web: true, MCP: true, MCPPath: "/mcp", TLS: true, Listen: ":8443"})
	if result.WebURL != "https://dbrain.example.ts.net:8443/" {
		t.Fatalf("non-default WebURL = %q", result.WebURL)
	}
	if result.MCPURL != "https://dbrain.example.ts.net:8443/mcp" {
		t.Fatalf("non-default MCPURL = %q", result.MCPURL)
	}

	result = URLs(status, Options{Web: true, MCPPath: "/mcp", TLS: true, Funnel: true, Listen: ":10000"})
	if result.WebURL != "https://dbrain.example.ts.net:10000/" {
		t.Fatalf("funnel WebURL = %q", result.WebURL)
	}

	result = URLs(&ipnstate.Status{}, Options{Web: true, MCP: true, MCPPath: "/mcp", TLS: true, ControlURL: "https://control.example"})
	if result.WebURL != "" || result.MCPURL != "" {
		t.Fatalf("custom control without status host should not synthesize URLs: %#v", result)
	}
}

func TestWhoIsLabelPrefersUserThenNodeThenFallback(t *testing.T) {
	t.Parallel()

	if got := whoIsLabel(nil, "100.64.0.1:1234"); got != "100.64.0.1:1234" {
		t.Fatalf("nil whois label = %q", got)
	}
	if got := whoIsLabel(&apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
		Node:        &tailcfg.Node{Name: "node.example.ts.net."},
	}, "fallback"); got != "alice@example.com" {
		t.Fatalf("user whois label = %q", got)
	}
	if got := whoIsLabel(&apitype.WhoIsResponse{
		Node: &tailcfg.Node{Name: "node.example.ts.net."},
	}, "fallback"); got != "node.example.ts.net" {
		t.Fatalf("node whois label = %q", got)
	}
}

func TestIdentityLoggerWritesToProvidedOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	nextCalled := false
	handler := identityLogger(fakeWhoIsClient{
		response: &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"}},
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}), &out)

	req, err := http.NewRequest(http.MethodGet, "/mcp", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.RemoteAddr = "100.64.0.1:1234"
	handler.ServeHTTP(noopResponseWriter{}, req)

	if !nextCalled {
		t.Fatalf("expected next handler to be called")
	}
	output := out.String()
	for _, value := range []string{"remote request", "method=GET", "path=/mcp", `identity="alice@example.com"`} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected log output to contain %q, got %q", value, output)
		}
	}
}

func TestIdentityLoggerCachesWhoIsByRemoteHost(t *testing.T) {
	t.Parallel()

	calls := 0
	handler := identityLogger(fakeWhoIsClient{
		response: &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"}},
		calls:    &calls,
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), io.Discard)

	for _, remoteAddr := range []string{"100.64.0.1:1111", "100.64.0.1:2222"} {
		req, err := http.NewRequest(http.MethodGet, "/mcp", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.RemoteAddr = remoteAddr
		handler.ServeHTTP(noopResponseWriter{}, req)
	}

	if calls != 1 {
		t.Fatalf("WhoIs calls = %d, want 1", calls)
	}
}

func TestIdentityLoggerDebouncesWhoIsFailures(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	calls := 0
	handler := identityLogger(fakeWhoIsClient{
		err:   errors.New("whois unavailable"),
		calls: &calls,
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), &out)

	for _, remoteAddr := range []string{"100.64.0.1:1111", "100.64.0.1:2222"} {
		req, err := http.NewRequest(http.MethodGet, "/mcp", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.RemoteAddr = remoteAddr
		handler.ServeHTTP(noopResponseWriter{}, req)
	}

	if calls != 2 {
		t.Fatalf("WhoIs calls = %d, want 2", calls)
	}
	if strings.Count(out.String(), "WARNING tsnet WhoIs failed") != 1 {
		t.Fatalf("expected one debounced WhoIs warning, got %q", out.String())
	}
}

func TestServeWithDepsUsesStartupTimeoutAndUnwinds(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	events := []string{}
	node := &fakeRemoteNode{upErr: boom, events: &events}
	opts := testServeOptions(t)
	opts.StartupTimeout = 25 * time.Millisecond
	start := time.Now()
	var out bytes.Buffer

	err := serveWithDeps(context.Background(), config.Config{}, opts, &out, testRemoteDeps(node, &events))
	if err == nil || !strings.Contains(err.Error(), "start tsnet") {
		t.Fatalf("expected start tsnet error, got %v", err)
	}
	if !strings.Contains(out.String(), "dbrain version:") {
		t.Fatalf("expected startup version in serve output, got %q", out.String())
	}
	if !node.upDeadlineSet {
		t.Fatalf("expected Up context to have a deadline")
	}
	if got := node.upDeadline.Sub(start); got < 0 || got > 250*time.Millisecond {
		t.Fatalf("unexpected startup deadline delta %s", got)
	}
	assertEventOrder(t, events, "up", "node-close", "lock-close")
}

func TestServeWithDepsRejectsNoSurfacesBeforeStartingNode(t *testing.T) {
	t.Parallel()

	events := []string{}
	node := &fakeRemoteNode{events: &events}
	opts := testServeOptions(t)
	opts.Web = false
	opts.MCP = false

	err := serveWithDeps(context.Background(), config.Config{}, opts, &bytes.Buffer{}, testRemoteDeps(node, &events))
	if err == nil || !strings.Contains(err.Error(), "at least one surface") {
		t.Fatalf("expected at-least-one-surface error, got %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no node or lock activity, got %v", events)
	}
}

func TestServeWithDepsRejectsInvalidFunnelBeforeStartingNode(t *testing.T) {
	t.Parallel()

	events := []string{}
	node := &fakeRemoteNode{events: &events}
	opts := testServeOptions(t)
	opts.Funnel = true
	opts.Listen = ":80"

	err := serveWithDeps(context.Background(), config.Config{}, opts, &bytes.Buffer{}, testRemoteDeps(node, &events))
	if err == nil || !strings.Contains(err.Error(), "tsnet funnel listen port must be 443, 8443, or 10000") {
		t.Fatalf("expected Funnel port validation error, got %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no node or lock activity, got %v", events)
	}
}

func TestServeWithDepsRejectsFunnelWebAuthWithLocalhostBaseURL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
auth:
  enabled: true
  providers: [github]
  session_key: "test-session-key-32-characters-long"
  github:
    client_id: "client-id"
    client_secret: "client-secret"
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	events := []string{}
	node := &fakeRemoteNode{events: &events}
	opts := testServeOptions(t)
	opts.Funnel = true
	opts.Web = true
	opts.MCP = false

	err = serveWithDeps(context.Background(), cfg, opts, &bytes.Buffer{}, testRemoteDeps(node, &events))
	if err == nil || !strings.Contains(err.Error(), "auth.base_url must be a public https origin") {
		t.Fatalf("expected public auth base URL validation error, got %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no node or lock activity, got %v", events)
	}
}

func TestServeWithDepsSelectsListenMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		tls    bool
		funnel bool
	}{
		{name: "tls", tls: true},
		{name: "plain", tls: false},
		{name: "funnel", tls: true, funnel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			events := []string{}
			node := &fakeRemoteNode{
				status:    &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}},
				localErr:  errors.New("no local client"),
				listenErr: errors.New("listen failed"),
				events:    &events,
			}
			opts := testServeOptions(t)
			opts.TLS = tc.tls
			opts.Funnel = tc.funnel

			err := serveWithDeps(context.Background(), config.Config{}, opts, &bytes.Buffer{}, testRemoteDeps(node, &events))
			if err == nil || !strings.Contains(err.Error(), "listen failed") {
				t.Fatalf("expected listen error, got %v", err)
			}
			if tc.funnel && !node.listenFunnelCalled {
				t.Fatalf("expected ListenFunnel to be called")
			}
			if !tc.funnel && tc.tls && !node.listenTLSCalled {
				t.Fatalf("expected ListenTLS to be called")
			}
			if !tc.tls && !node.listenCalled {
				t.Fatalf("expected Listen to be called")
			}
		})
	}
}

func TestRemoteHTTPServerTimeoutsAllowLongWebStreams(t *testing.T) {
	t.Parallel()

	server := newHTTPServer(http.NotFoundHandler())
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 10s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != time.Minute {
		t.Fatalf("ReadTimeout = %s, want 1m", server.ReadTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want disabled for long-lived SSE streams", server.WriteTimeout)
	}
}

func TestServeWithDepsShutdownOrder(t *testing.T) {
	t.Parallel()

	events := []string{}
	listener := newBlockingListener(&events)
	node := &fakeRemoteNode{
		status:   &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}},
		localErr: errors.New("no local client"),
		listener: listener,
		events:   &events,
		listened: make(chan struct{}),
	}
	opts := testServeOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serveWithDeps(ctx, config.Config{}, opts, &bytes.Buffer{}, testRemoteDeps(node, &events))
	}()

	select {
	case <-node.listened:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for listener")
	}
	select {
	case <-listener.accepting:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for server accept loop")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveWithDeps returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for shutdown")
	}

	assertEventOrder(t, events, "listener-close", "node-close", "lock-close")
}

func TestServeWithDepsWarnsWhenFunnelEnabled(t *testing.T) {
	t.Parallel()

	events := []string{}
	listenerEvents := []string{}
	listener := newBlockingListener(&listenerEvents)
	node := &fakeRemoteNode{
		status:   &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}},
		localErr: errors.New("no local client"),
		listener: listener,
		events:   &events,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := testServeOptions(t)
	opts.Web = true
	opts.MCP = false
	opts.Funnel = true
	opts.OnReady = cancel
	var out bytes.Buffer
	deps := testRemoteDeps(node, &events)
	deps.buildHandler = func(context.Context, config.Config, Options, whoIsClient, io.Writer) (http.Handler, func(), error) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		return handler, func() {}, nil
	}

	if err := serveWithDeps(ctx, config.Config{RootDir: t.TempDir()}, opts, &out, deps); err != nil {
		t.Fatalf("serveWithDeps returned error: %v", err)
	}
	output := out.String()
	for _, want := range []string{"--tsnet-funnel", "public internet", "same tsnet node identity", "remote web is read/write"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected Funnel warning to contain %q, got %q", want, output)
		}
	}
}

func TestServeWithDepsWarnsWhenMCPAuthDisabled(t *testing.T) {
	t.Parallel()

	events := []string{}
	listener := newBlockingListener(&events)
	node := &fakeRemoteNode{
		status:   &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}},
		localErr: errors.New("no local client"),
		listener: listener,
		events:   &events,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := testServeOptions(t)
	opts.Web = false
	opts.MCP = true
	opts.OnReady = cancel
	var out bytes.Buffer

	if err := serveWithDeps(ctx, config.Config{RootDir: t.TempDir()}, opts, &out, testRemoteDeps(node, &events)); err != nil {
		t.Fatalf("serveWithDeps returned error: %v", err)
	}
	if !strings.Contains(out.String(), "WARNING MCP AUTH DISABLED") || !strings.Contains(out.String(), "Tailscale Funnel") {
		t.Fatalf("expected open MCP auth warning, got %q", out.String())
	}
}

func TestServeWithDepsPassesPreparedStateDirToNode(t *testing.T) {
	t.Parallel()

	events := []string{}
	node := &fakeRemoteNode{upErr: errors.New("stop after node construction"), events: &events}
	opts := testServeOptions(t)
	preparedStateDir := opts.StateDir + "-prepared"
	var nodeStateDir string

	deps := testRemoteDeps(node, &events)
	deps.prepareStateDir = func(string) (string, error) {
		return preparedStateDir, nil
	}
	deps.newNode = func(opts Options, _ SecretResult, _ func(string, ...any), _ io.Writer) remoteNode {
		nodeStateDir = opts.StateDir
		return node
	}

	err := serveWithDeps(context.Background(), config.Config{}, opts, &bytes.Buffer{}, deps)
	if err == nil || !strings.Contains(err.Error(), "start tsnet") {
		t.Fatalf("expected start tsnet error, got %v", err)
	}
	if nodeStateDir != preparedStateDir {
		t.Fatalf("node state dir = %q, want %q", nodeStateDir, preparedStateDir)
	}
}

func testServeOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		Web:            true,
		MCP:            false,
		MCPPath:        DefaultMCPPath,
		Hostname:       "dbrain-test",
		StateDir:       t.TempDir(),
		Listen:         ":443",
		TLS:            true,
		StartupTimeout: DefaultStartupTimeout,
	}
}

func testRemoteDeps(node *fakeRemoteNode, events *[]string) remoteDeps {
	return remoteDeps{
		prepareStateDir: func(path string) (string, error) {
			return path, nil
		},
		acquireStateLock: func(string) (stateLock, error) {
			return fakeStateLock{events: events}, nil
		},
		resolveAuthKey: func(context.Context, Options) (SecretResult, error) {
			return SecretResult{}, nil
		},
		newNode: func(Options, SecretResult, func(string, ...any), io.Writer) remoteNode {
			return node
		},
		buildHandler: func(context.Context, config.Config, Options, whoIsClient, io.Writer) (http.Handler, func(), error) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			})
			cleanup := func() {
				*events = append(*events, "cleanup")
			}
			return handler, cleanup, nil
		},
	}
}

type fakeStateLock struct {
	events *[]string
}

func (l fakeStateLock) Close() error {
	*l.events = append(*l.events, "lock-close")
	return nil
}

type fakeRemoteNode struct {
	status             *ipnstate.Status
	upErr              error
	localErr           error
	listenErr          error
	listener           net.Listener
	events             *[]string
	listened           chan struct{}
	upDeadlineSet      bool
	upDeadline         time.Time
	listenCalled       bool
	listenTLSCalled    bool
	listenFunnelCalled bool
}

func (n *fakeRemoteNode) Up(ctx context.Context) (*ipnstate.Status, error) {
	*n.events = append(*n.events, "up")
	if deadline, ok := ctx.Deadline(); ok {
		n.upDeadlineSet = true
		n.upDeadline = deadline
	}
	if n.upErr != nil {
		return nil, n.upErr
	}
	if n.status != nil {
		return n.status, nil
	}
	return &ipnstate.Status{}, nil
}

func (n *fakeRemoteNode) LocalClient() (whoIsClient, error) {
	if n.localErr != nil {
		return nil, n.localErr
	}
	return nil, nil
}

func (n *fakeRemoteNode) Listen(string, string) (net.Listener, error) {
	n.listenCalled = true
	*n.events = append(*n.events, "listen")
	if n.listened != nil {
		close(n.listened)
	}
	if n.listenErr != nil {
		return nil, n.listenErr
	}
	return n.listener, nil
}

func (n *fakeRemoteNode) ListenTLS(string, string) (net.Listener, error) {
	n.listenTLSCalled = true
	*n.events = append(*n.events, "listen-tls")
	if n.listened != nil {
		close(n.listened)
	}
	if n.listenErr != nil {
		return nil, n.listenErr
	}
	return n.listener, nil
}

func (n *fakeRemoteNode) ListenFunnel(string, string) (net.Listener, error) {
	n.listenFunnelCalled = true
	*n.events = append(*n.events, "listen-funnel")
	if n.listened != nil {
		close(n.listened)
	}
	if n.listenErr != nil {
		return nil, n.listenErr
	}
	return n.listener, nil
}

func (n *fakeRemoteNode) Close() error {
	*n.events = append(*n.events, "node-close")
	return nil
}

type fakeWhoIsClient struct {
	response *apitype.WhoIsResponse
	err      error
	calls    *int
}

func (c fakeWhoIsClient) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	if c.calls != nil {
		(*c.calls)++
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}

type noopResponseWriter struct{}

func (noopResponseWriter) Header() http.Header {
	return http.Header{}
}

func (noopResponseWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func (noopResponseWriter) WriteHeader(int) {}

type blockingListener struct {
	events     *[]string
	accepting  chan struct{}
	closed     chan struct{}
	acceptOnce sync.Once
	closeOnce  sync.Once
}

func newBlockingListener(events *[]string) *blockingListener {
	return &blockingListener{
		events:    events,
		accepting: make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() {
		close(l.accepting)
	})
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	l.closeOnce.Do(func() {
		*l.events = append(*l.events, "listener-close")
		close(l.closed)
	})
	return nil
}

func (l *blockingListener) Addr() net.Addr {
	return fakeAddr("tsnet")
}

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }

func (a fakeAddr) String() string { return string(a) }

func assertEventOrder(t *testing.T, events []string, names ...string) {
	t.Helper()
	last := -1
	for _, name := range names {
		index := -1
		for i, event := range events {
			if event == name {
				index = i
				break
			}
		}
		if index == -1 {
			t.Fatalf("event %q not found in %v", name, events)
		}
		if index <= last {
			t.Fatalf("event %q occurred out of order in %v", name, events)
		}
		last = index
	}
}
