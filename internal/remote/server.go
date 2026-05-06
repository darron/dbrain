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
	"github.com/darron/dbrain/internal/startuplog"
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
