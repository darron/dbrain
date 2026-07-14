package remote

import (
	"context"
	"io"
	"net/http"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
)

func buildHandler(ctx context.Context, cfg config.Config, opts Options, lc whoIsClient, logOut io.Writer) (http.Handler, func(), error) {
	var closers []io.Closer
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i].Close()
		}
	}

	var webHandler http.Handler
	if opts.Web {
		st, err := store.OpenWithOptions(cfg.DBPath, store.OpenOptions{
			MigrationReporter: startuplog.MigrationReporter(logOut),
		})
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		closers = append(closers, st)
		webHandler, err = web.NewHandlerWithOptions(cfg, st, web.HandlerOptions{
			SchedulerStatus: opts.SchedulerStatus,
			Context:         ctx,
			LogOutput:       logOut,
		})
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
		httpOptions := mcpserver.HTTPOptions{
			Path:      opts.MCPPath,
			LogOutput: logOut,
		}
		if err := mcpserver.ApplyRuntimeAuthOptions(cfg, st, &httpOptions); err != nil {
			cleanup()
			return nil, nil, err
		}
		mcpHandler = mcpserver.NewWithDependencies(cfg, st, opts.mcpDependencies).HTTPHandler(httpOptions)
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
