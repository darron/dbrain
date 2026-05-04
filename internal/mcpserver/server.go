package mcpserver

import (
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
)

const protocolVersion = "2025-03-26"

type Server struct {
	cfg config.Config
	st  *store.Store
}

func New(cfg config.Config, st *store.Store) *Server {
	return &Server{cfg: cfg, st: st}
}

func serverVersion() string {
	details := version.Current()
	if short := strings.TrimSpace(details.Short); short != "" && short != "unknown" {
		if strings.TrimSpace(strings.ToLower(details.GitStatus)) == "modified" && !strings.Contains(short, "+dirty") {
			return short + "+dirty"
		}
		return short
	}
	if moduleVersion := strings.TrimSpace(details.ModuleVersion); moduleVersion != "" && moduleVersion != "(devel)" {
		return moduleVersion
	}
	return "unknown"
}
