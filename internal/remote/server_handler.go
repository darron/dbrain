package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
)

type runtimeOwner interface {
	Close() error
	Shutdown(context.Context) error
}

func closeOwnedStore(owner runtimeOwner, closeStore func() error, timeout time.Duration) error {
	return closeOwnedStoreWithLogger(owner, closeStore, timeout, nil)
}

func closeOwnedStoreWithLogger(owner runtimeOwner, closeStore func() error, timeout time.Duration, logCleanup func(string, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := owner.Shutdown(ctx)
	if ctx.Err() != nil && errors.Is(shutdownErr, ctx.Err()) {
		go func() {
			if err := owner.Close(); err != nil && logCleanup != nil {
				logCleanup("runtime", err)
			}
			if err := closeStore(); err != nil && logCleanup != nil {
				logCleanup("store", err)
			}
		}()
		return shutdownErr
	}
	return errors.Join(shutdownErr, closeStore())
}

const handlerCleanupTimeout = 10 * time.Second

func buildHandler(ctx context.Context, cfg config.Config, opts Options, lc whoIsClient, logOut io.Writer) (http.Handler, func() error, error) {
	var cleanups []func() error
	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanupErr = errors.Join(cleanupErr, cleanups[i]())
			}
		})
		return cleanupErr
	}
	fail := func(err error) (http.Handler, func() error, error) {
		return nil, nil, errors.Join(err, cleanup())
	}

	var webHandler http.Handler
	if opts.Web {
		st, err := store.OpenWithSemanticCacheOptions(cfg.DBPath, cfg.CacheDir, store.OpenOptions{
			MigrationReporter: startuplog.MigrationReporter(logOut),
		})
		if err != nil {
			return fail(err)
		}
		webHandler, err = web.NewHandlerWithOptions(cfg, st, web.HandlerOptions{
			SchedulerStatus:       opts.SchedulerStatus,
			Context:               ctx,
			LogOutput:             logOut,
			AuditReports:          opts.webAudit.Reports,
			AuditRuns:             opts.webAudit.Runs,
			AuditSyncInterval:     opts.webAudit.SyncInterval,
			AuditStandardInterval: opts.webAudit.StandardInterval,
		})
		if err != nil {
			return fail(errors.Join(err, st.Close()))
		}
		owner, ok := webHandler.(web.CloseableHandler)
		if !ok {
			return fail(errors.Join(errors.New("web handler does not expose lifecycle cleanup"), st.Close()))
		}
		cleanups = append(cleanups, func() error {
			return closeOwnedStoreWithLogger(owner, st.Close, handlerCleanupTimeout, func(component string, err error) {
				if logOut != nil {
					_, _ = fmt.Fprintf(logOut, "WARNING remote async cleanup failed component=%s: %v\n", component, err)
				}
			})
		})
	}

	var mcpHandler http.Handler
	if opts.MCP {
		st, err := store.OpenReadOnly(cfg.DBPath)
		if err != nil {
			return fail(err)
		}
		httpOptions := mcpserver.HTTPOptions{
			Path:      opts.MCPPath,
			LogOutput: logOut,
		}
		if err := mcpserver.ApplyRuntimeAuthOptions(cfg, st, &httpOptions); err != nil {
			return fail(errors.Join(err, st.Close()))
		}
		server := mcpserver.NewWithDependencies(cfg, st, opts.mcpDependencies)
		mcpHandler = server.HTTPHandler(httpOptions)
		cleanups = append(cleanups, func() error {
			return closeOwnedStoreWithLogger(server, st.Close, handlerCleanupTimeout, func(component string, err error) {
				if logOut != nil {
					_, _ = fmt.Fprintf(logOut, "WARNING remote async cleanup failed component=%s: %v\n", component, err)
				}
			})
		})
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
		return fail(err)
	}
	if lc != nil {
		handler = identityLogger(lc, handler, logOut)
	}
	return handler, cleanup, nil
}
