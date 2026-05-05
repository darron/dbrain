package remote

import (
	"io"
	"net/http"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
)

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
