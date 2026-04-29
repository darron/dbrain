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
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

type ServeResult struct {
	StateDir string
	WebURL   string
	MCPURL   string
}

type whoIsClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

func Serve(ctx context.Context, cfg config.Config, opts Options, logOut io.Writer) error {
	if logOut == nil {
		logOut = os.Stderr
	}
	if err := opts.Validate(); err != nil {
		return err
	}

	stateDir, err := PrepareStateDir(opts.StateDir)
	if err != nil {
		return err
	}
	opts.StateDir = stateDir
	if LooksLikeSyncedPath(stateDir) {
		_, _ = fmt.Fprintf(logOut, "WARNING tsnet state dir appears to be under a sync folder: %s\n", stateDir)
	}

	lock, err := AcquireStateLock(stateDir)
	if err != nil {
		return err
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = lock.Close()
		}
	}()

	auth, err := ResolveAuthKey(ctx, opts)
	if err != nil {
		return err
	}
	for _, warning := range auth.Warnings {
		_, _ = fmt.Fprintf(logOut, "WARNING %s\n", warning)
	}

	userLogf := newUserLogger(logOut)
	ts := &tsnet.Server{
		Dir:           stateDir,
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
	tsClosed := false
	defer func() {
		if !tsClosed {
			_ = ts.Close()
		}
	}()

	upCtx, cancel := context.WithTimeout(ctx, opts.StartupTimeout)
	status, err := ts.Up(upCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("start tsnet: %w", err)
	}

	lc, err := ts.LocalClient()
	if err != nil {
		_, _ = fmt.Fprintf(logOut, "WARNING tsnet LocalClient unavailable; request identity logging will use remote addresses: %v\n", err)
	}

	handler, cleanup, err := buildHandler(cfg, opts, lc)
	if err != nil {
		return err
	}
	defer cleanup()

	listener, err := listen(ts, opts)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
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
		closeErr := ts.Close()
		tsClosed = true
		lockErr := lock.Close()
		lockHeld = false
		return firstErr(shutdownErr, closeErr, lockErr, ctx.Err())
	case err := <-errCh:
		closeErr := ts.Close()
		tsClosed = true
		lockErr := lock.Close()
		lockHeld = false
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return firstErr(err, closeErr, lockErr)
	}
}

func buildHandler(cfg config.Config, opts Options, lc *local.Client) (http.Handler, func(), error) {
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
		handler = identityLogger(lc, handler)
	}
	return handler, cleanup, nil
}

func listen(ts *tsnet.Server, opts Options) (net.Listener, error) {
	if opts.TLS {
		return ts.ListenTLS("tcp", opts.Listen)
	}
	return ts.Listen("tcp", opts.Listen)
}

func identityLogger(client whoIsClient, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := r.RemoteAddr
		if who, err := client.WhoIs(r.Context(), r.RemoteAddr); err == nil {
			identity = whoIsLabel(who, r.RemoteAddr)
		}
		logRemoteRequest(r, identity)
		next.ServeHTTP(w, r)
	})
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

func logRemoteRequest(r *http.Request, identity string) {
	_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s remote request method=%s path=%s identity=%q remote=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, identity, r.RemoteAddr)
}

func newUserLogger(out io.Writer) func(string, ...any) {
	seenLines := map[string]bool{}
	seenURLs := map[string]bool{}
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		lineKey := strings.TrimSpace(line)
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
	base := scheme + "://" + host
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

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}
