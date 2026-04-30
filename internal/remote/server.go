package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

const identityCacheTTL = 30 * time.Second

type ServeResult struct {
	StateDir string
	WebURL   string
	MCPURL   string
}

type whoIsClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

type remoteNode interface {
	Up(ctx context.Context) (*ipnstate.Status, error)
	LocalClient() (whoIsClient, error)
	Listen(network string, addr string) (net.Listener, error)
	ListenTLS(network string, addr string) (net.Listener, error)
	Close() error
}

type stateLock interface {
	Close() error
}

type remoteDeps struct {
	prepareStateDir  func(string) (string, error)
	acquireStateLock func(string) (stateLock, error)
	resolveAuthKey   func(context.Context, Options) (SecretResult, error)
	newNode          func(Options, SecretResult, func(string, ...any), io.Writer) remoteNode
	buildHandler     func(config.Config, Options, whoIsClient, io.Writer) (http.Handler, func(), error)
}

func Serve(ctx context.Context, cfg config.Config, opts Options, logOut io.Writer) error {
	return serveWithDeps(ctx, cfg, opts, logOut, defaultRemoteDeps())
}

func defaultRemoteDeps() remoteDeps {
	return remoteDeps{
		prepareStateDir: PrepareStateDir,
		acquireStateLock: func(stateDir string) (stateLock, error) {
			return AcquireStateLock(stateDir)
		},
		resolveAuthKey: ResolveAuthKey,
		newNode:        newTSNetNode,
		buildHandler:   buildHandler,
	}
}

func serveWithDeps(ctx context.Context, cfg config.Config, opts Options, logOut io.Writer, deps remoteDeps) error {
	if logOut == nil {
		logOut = os.Stderr
	}
	if err := opts.Validate(); err != nil {
		return err
	}

	stateDir, err := deps.prepareStateDir(opts.StateDir)
	if err != nil {
		return err
	}
	opts.StateDir = stateDir
	if LooksLikeSyncedPath(stateDir) {
		_, _ = fmt.Fprintf(logOut, "WARNING tsnet state dir appears to be under a sync folder: %s\n", stateDir)
	}

	lock, err := deps.acquireStateLock(stateDir)
	if err != nil {
		return err
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = lock.Close()
		}
	}()

	auth, err := deps.resolveAuthKey(ctx, opts)
	if err != nil {
		return err
	}
	for _, warning := range auth.Warnings {
		_, _ = fmt.Fprintf(logOut, "WARNING %s\n", warning)
	}
	if strings.TrimSpace(opts.ControlURL) != "" {
		_, _ = fmt.Fprintf(logOut, "WARNING --tsnet-control-url is experimental; DNS and ListenTLS certificate behavior may differ from Tailscale SaaS: %s\n", opts.ControlURL)
		if opts.TLS {
			_, _ = fmt.Fprintln(logOut, "WARNING --tsnet-control-url with --tsnet-tls=true may not produce .ts.net HTTPS URLs on custom control servers.")
		}
	}

	userLogf := newUserLogger(logOut)
	node := deps.newNode(opts, auth, userLogf, logOut)
	nodeClosed := false
	defer func() {
		if !nodeClosed {
			_ = node.Close()
		}
	}()

	upCtx, cancel := context.WithTimeout(ctx, opts.StartupTimeout)
	status, err := node.Up(upCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("start tsnet: %w", err)
	}

	lc, err := node.LocalClient()
	if err != nil {
		_, _ = fmt.Fprintf(logOut, "WARNING tsnet LocalClient unavailable; request identity logging will use remote addresses: %v\n", err)
	}

	handler, cleanup, err := deps.buildHandler(cfg, opts, lc, logOut)
	if err != nil {
		return err
	}
	defer cleanup()

	listener, err := listen(node, opts)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	urls := URLs(status, opts)
	if opts.Web && urls.WebURL != "" {
		_, _ = fmt.Fprintf(logOut, "Remote Web UI: %s\n", urls.WebURL)
	}
	if opts.MCP && urls.MCPURL != "" {
		_, _ = fmt.Fprintf(logOut, "Remote MCP: %s\n", urls.MCPURL)
	}
	if opts.Web {
		_, _ = fmt.Fprintln(logOut, "WARNING remote web is read/write; Tailscale ACLs govern access.")
	}

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		closeErr := node.Close()
		nodeClosed = true
		lockErr := lock.Close()
		lockHeld = false
		return firstErr(shutdownErr, closeErr, lockErr, ctx.Err())
	case err := <-errCh:
		closeErr := node.Close()
		nodeClosed = true
		lockErr := lock.Close()
		lockHeld = false
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return firstErr(err, closeErr, lockErr)
	}
}

func buildHandler(cfg config.Config, opts Options, lc whoIsClient, logOut io.Writer) (http.Handler, func(), error) {
	var closers []io.Closer
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i].Close()
		}
	}

	var webHandler http.Handler
	if opts.Web {
		st, err := store.Open(cfg.DBPath)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		closers = append(closers, st)
		webHandler, err = web.NewHandler(cfg, st)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
	}

	var mcpHandler http.Handler
	if opts.MCP {
		st, err := store.OpenReadOnly(cfg.DBPath)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		closers = append(closers, st)
		mcpHandler = mcpserver.New(cfg, st).HTTPHandler(mcpserver.HTTPOptions{Path: opts.MCPPath})
	}

	handler, err := NewHandler(HandlerOptions{
		Web:        opts.Web,
		MCP:        opts.MCP,
		MCPPath:    opts.MCPPath,
		TLS:        opts.TLS,
		WebHandler: webHandler,
		MCPHandler: mcpHandler,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if lc != nil {
		handler = identityLogger(lc, handler, logOut)
	}
	return handler, cleanup, nil
}

func listen(ts remoteNode, opts Options) (net.Listener, error) {
	if opts.TLS {
		return ts.ListenTLS("tcp", opts.Listen)
	}
	return ts.Listen("tcp", opts.Listen)
}

type tsnetNode struct {
	server *tsnet.Server
}

func newTSNetNode(opts Options, auth SecretResult, userLogf func(string, ...any), logOut io.Writer) remoteNode {
	ts := &tsnet.Server{
		Dir:           opts.StateDir,
		Hostname:      opts.Hostname,
		AuthKey:       auth.Value,
		AdvertiseTags: opts.AdvertiseTags,
		ControlURL:    opts.ControlURL,
		UserLogf:      userLogf,
	}
	if opts.Verbose {
		ts.Logf = func(format string, args ...any) {
			_, _ = fmt.Fprintf(logOut, "tsnet debug: "+format+"\n", args...)
		}
	}
	return tsnetNode{server: ts}
}

func (n tsnetNode) Up(ctx context.Context) (*ipnstate.Status, error) {
	return n.server.Up(ctx)
}

func (n tsnetNode) LocalClient() (whoIsClient, error) {
	return n.server.LocalClient()
}

func (n tsnetNode) Listen(network string, addr string) (net.Listener, error) {
	return n.server.Listen(network, addr)
}

func (n tsnetNode) ListenTLS(network string, addr string) (net.Listener, error) {
	return n.server.ListenTLS(network, addr)
}

func (n tsnetNode) Close() error {
	return n.server.Close()
}

func identityLogger(client whoIsClient, next http.Handler, logOut io.Writer) http.Handler {
	var mu sync.Mutex
	cache := map[string]identityCacheEntry{}
	errorLogUntil := map[string]time.Time{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := r.RemoteAddr
		cacheKey := identityCacheKey(r.RemoteAddr)
		now := time.Now()
		mu.Lock()
		cached, ok := cache[cacheKey]
		if ok && now.Before(cached.expires) {
			identity = cached.identity
		}
		mu.Unlock()
		if !ok || !now.Before(cached.expires) {
			if who, err := client.WhoIs(r.Context(), r.RemoteAddr); err == nil {
				identity = whoIsLabel(who, r.RemoteAddr)
				mu.Lock()
				cache[cacheKey] = identityCacheEntry{identity: identity, expires: now.Add(identityCacheTTL)}
				mu.Unlock()
			} else {
				shouldLog := false
				mu.Lock()
				if !now.Before(errorLogUntil[cacheKey]) {
					errorLogUntil[cacheKey] = now.Add(identityCacheTTL)
					shouldLog = true
				}
				mu.Unlock()
				if shouldLog {
					logWhoIsFailure(logOut, r.RemoteAddr, err)
				}
			}
		}
		logRemoteRequest(logOut, r, identity)
		next.ServeHTTP(w, r)
	})
}

type identityCacheEntry struct {
	identity string
	expires  time.Time
}

func identityCacheKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || strings.TrimSpace(host) == "" {
		return remoteAddr
	}
	return host
}

func logWhoIsFailure(out io.Writer, remoteAddr string, err error) {
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, "WARNING tsnet WhoIs failed remote=%q error=%v\n", remoteAddr, err)
}

func whoIsLabel(who *apitype.WhoIsResponse, fallback string) string {
	if who == nil {
		return fallback
	}
	if who.UserProfile != nil {
		if strings.TrimSpace(who.UserProfile.LoginName) != "" {
			return who.UserProfile.LoginName
		}
		if strings.TrimSpace(who.UserProfile.DisplayName) != "" {
			return who.UserProfile.DisplayName
		}
	}
	if who.Node != nil && strings.TrimSpace(who.Node.Name) != "" {
		return strings.TrimSuffix(who.Node.Name, ".")
	}
	return fallback
}

func logRemoteRequest(out io.Writer, r *http.Request, identity string) {
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, "DEBUG %s remote request method=%s path=%s identity=%q remote=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, identity, r.RemoteAddr)
}

func newUserLogger(out io.Writer) func(string, ...any) {
	var mu sync.Mutex
	seenLines := map[string]bool{}
	seenURLs := map[string]bool{}
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		lineKey := strings.TrimSpace(line)
		mu.Lock()
		defer mu.Unlock()
		if seenLines[lineKey] {
			return
		}
		seenLines[lineKey] = true
		_, _ = fmt.Fprintln(out, line)
		for _, url := range urlPattern.FindAllString(line, -1) {
			url = strings.TrimRight(url, ".,);]")
			if seenURLs[url] {
				continue
			}
			seenURLs[url] = true
			_, _ = fmt.Fprintf(out, "Visit this URL to authenticate dbrain:\n%s\n", url)
		}
	}
}

func URLs(status *ipnstate.Status, opts Options) ServeResult {
	result := ServeResult{StateDir: opts.StateDir}
	host := statusHost(status)
	if host == "" {
		return result
	}
	scheme := "https"
	if !opts.TLS {
		scheme = "http"
	}
	base := scheme + "://" + hostWithListenPort(host, opts.Listen, scheme)
	if opts.Web {
		result.WebURL = base + "/"
	}
	if opts.MCP {
		result.MCPURL = base + opts.MCPPath
	}
	return result
}

func statusHost(status *ipnstate.Status) string {
	if status == nil {
		return ""
	}
	if len(status.CertDomains) > 0 {
		return strings.TrimSuffix(status.CertDomains[0], ".")
	}
	if status.Self != nil && strings.TrimSpace(status.Self.DNSName) != "" {
		return strings.TrimSuffix(status.Self.DNSName, ".")
	}
	return ""
}

func hostWithListenPort(host string, listen string, scheme string) string {
	port := listenPort(listen)
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			return "[" + host + "]"
		}
		return host
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return net.JoinHostPort(host, port)
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func listenPort(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return ""
	}
	if strings.HasPrefix(listen, ":") {
		return strings.TrimPrefix(listen, ":")
	}
	if host, port, err := net.SplitHostPort(listen); err == nil && (host != "" || port != "") {
		return port
	}
	if !strings.Contains(listen, ":") {
		return listen
	}
	return ""
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}
