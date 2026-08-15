package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/web"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
)

type whoIsClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

type remoteNode interface {
	Up(ctx context.Context) (*ipnstate.Status, error)
	LocalClient() (whoIsClient, error)
	Listen(network string, addr string) (net.Listener, error)
	ListenTLS(network string, addr string) (net.Listener, error)
	ListenFunnel(network string, addr string) (net.Listener, error)
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
	buildHandler     func(context.Context, config.Config, Options, whoIsClient, io.Writer) (http.Handler, func() error, error)
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// Remote web includes long-lived SSE research/chat streams. Go's
		// WriteTimeout is an absolute response deadline, not an idle timeout,
		// so handler-level runner/stage/model timeouts must bound that work.
		WriteTimeout: 0,
	}
}

func runRemoteAsyncCleanup(shutdown func() error, cleanup func() error, logOut io.Writer) {
	if err := shutdown(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_, _ = fmt.Fprintf(logOut, "WARNING remote async cleanup failed component=http_server: %v\n", err)
	}
	if err := cleanup(); err != nil {
		_, _ = fmt.Fprintf(logOut, "WARNING remote async cleanup failed component=runtime_or_store: %v\n", err)
	}
}

func Serve(ctx context.Context, cfg config.Config, opts Options, logOut io.Writer) error {
	return serveWithDeps(ctx, cfg, opts, logOut, defaultRemoteDeps())
}

func validateFunnelSurfaceAuth(ctx context.Context, cfg config.Config, opts Options) error {
	if !opts.Funnel {
		return nil
	}
	if opts.Web {
		if err := web.RequirePublicAuthConfig(ctx, cfg); err != nil {
			return err
		}
	}
	if opts.MCP && !mcpserver.AuthEnabled(cfg) {
		return fmt.Errorf("mcp bearer auth must be enabled for Tailscale Funnel; set mcp.auth.enabled=true, or disable --tsnet-funnel or --mcp")
	}
	return nil
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

func serveWithDeps(ctx context.Context, cfg config.Config, opts Options, logOut io.Writer, deps remoteDeps) (returnErr error) {
	if logOut == nil {
		logOut = os.Stderr
	}
	if err := opts.Validate(); err != nil {
		return err
	}
	if err := validateFunnelSurfaceAuth(ctx, cfg, opts); err != nil {
		return err
	}
	startuplog.WriteVersion(logOut)

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
		_, _ = fmt.Fprintf(logOut, "WARNING --tsnet-control-url is experimental; DNS and tsnet HTTPS certificate behavior may differ from Tailscale SaaS: %s\n", opts.ControlURL)
		if opts.TLS {
			_, _ = fmt.Fprintln(logOut, "WARNING --tsnet-control-url with --tsnet-tls=true may not produce .ts.net HTTPS URLs on custom control servers.")
		}
	}
	if opts.Funnel {
		_, _ = fmt.Fprintln(logOut, "WARNING --tsnet-funnel exposes configured dbrain surfaces to the public internet through Tailscale Funnel; it uses the same tsnet node identity, hostname, state dir, and auth credentials.")
		_, _ = fmt.Fprintln(logOut, "WARNING Tailscale Funnel requires tailnet Funnel policy, MagicDNS, HTTPS certificates, and a supported port (:443, :8443, or :10000).")
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

	handler, cleanup, err := deps.buildHandler(ctx, cfg, opts, lc, logOut)
	if err != nil {
		return err
	}
	cleanupPending := true
	defer func() {
		if cleanupPending {
			returnErr = errors.Join(returnErr, cleanup())
		}
	}()

	listener, err := listen(node, opts)
	if err != nil {
		return err
	}
	if listener == nil {
		return fmt.Errorf("tsnet listener unavailable")
	}

	httpServer := newHTTPServer(handler)
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	stopProcessHeartbeat := startProcessHeartbeat(ctx, logOut)
	defer stopProcessHeartbeat()

	urls := URLs(status, opts)
	if opts.Web && urls.WebURL != "" {
		_, _ = fmt.Fprintf(logOut, "Remote Web UI: %s\n", urls.WebURL)
	}
	if opts.MCP && urls.MCPURL != "" {
		_, _ = fmt.Fprintf(logOut, "Remote MCP: %s\n", urls.MCPURL)
	}
	if opts.Web {
		if opts.Funnel {
			_, _ = fmt.Fprintln(logOut, "WARNING remote web is read/write and Tailscale Funnel can make it publicly reachable; configure web OAuth auth or disable --web before public exposure.")
		} else {
			_, _ = fmt.Fprintln(logOut, "WARNING remote web is read/write; Tailscale ACLs govern access.")
		}
	}
	if opts.MCP && !mcpserver.AuthEnabled(cfg) {
		mcpserver.WriteOpenAuthWarning(logOut, "Remote MCP")
	}
	if opts.OnReady != nil {
		opts.OnReady()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		var serveErr error
		select {
		case serveErr = <-errCh:
			if errors.Is(serveErr, http.ErrServerClosed) {
				serveErr = nil
			}
		case <-shutdownCtx.Done():
			if shutdownErr == nil {
				shutdownErr = shutdownCtx.Err()
			}
		}
		if shutdownCtx.Err() != nil && errors.Is(shutdownErr, shutdownCtx.Err()) {
			cleanupPending = false
			go func() {
				runRemoteAsyncCleanup(func() error {
					return httpServer.Shutdown(context.Background())
				}, cleanup, logOut)
			}()
		}
		closeErr := node.Close()
		nodeClosed = true
		lockErr := lock.Close()
		lockHeld = false
		return firstErr(shutdownErr, serveErr, closeErr, lockErr, ctx.Err())
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
