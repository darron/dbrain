package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/internal/schedulerstate"
)

type tsnetStateInfo struct {
	Hostname     string                        `json:"hostname"`
	StateDir     string                        `json:"state_dir"`
	Exists       bool                          `json:"exists"`
	Locked       bool                          `json:"locked"`
	Running      bool                          `json:"running"`
	Reachable    bool                          `json:"reachable"`
	WebReachable bool                          `json:"web_reachable"`
	MCPReachable bool                          `json:"mcp_reachable"`
	TailnetIPs   []string                      `json:"tailnet_ips,omitempty"`
	WebURL       string                        `json:"web_url,omitempty"`
	MCPURL       string                        `json:"mcp_url,omitempty"`
	TLS          bool                          `json:"tls"`
	Funnel       bool                          `json:"funnel"`
	ControlURL   string                        `json:"control_url"`
	State        string                        `json:"state"`
	CertHealth   string                        `json:"cert_health"`
	NeedsLogin   bool                          `json:"needs_login"`
	LockPath     string                        `json:"lock_path"`
	WebError     string                        `json:"web_error,omitempty"`
	MCPError     string                        `json:"mcp_error,omitempty"`
	CertError    string                        `json:"cert_error,omitempty"`
	Warning      string                        `json:"warning,omitempty"`
	SyncAll      *schedulerstate.SyncAllStatus `json:"sync_all,omitempty"`
}

func tsnetStateStatus(ctx context.Context, opts remote.Options) (tsnetStateInfo, error) {
	return tsnetStateStatusWithDeps(ctx, opts, defaultTSNetStatusDeps())
}

func tsnetStateStatusWithDeps(ctx context.Context, opts remote.Options, deps tsnetStatusDeps) (tsnetStateInfo, error) {
	resolved, err := remote.ResolveStateDir(opts.StateDir)
	if err != nil {
		return tsnetStateInfo{}, err
	}
	info := tsnetStateInfo{
		Hostname:   opts.Hostname,
		StateDir:   resolved,
		LockPath:   filepath.Join(resolved, remote.StateLockName),
		TLS:        opts.TLS,
		Funnel:     opts.Funnel,
		ControlURL: opts.ControlURL,
		State:      "not_configured",
	}
	if remote.LooksLikeSyncedPath(resolved) {
		appendTSNetWarning(&info, "state directory appears to be under a sync folder")
	}
	if strings.TrimSpace(opts.ControlURL) != "" {
		appendTSNetWarning(&info, "custom tsnet control URL is experimental; DNS and tsnet HTTPS certificate behavior may differ from Tailscale SaaS")
	}
	if opts.Funnel {
		appendTSNetWarning(&info, "Tailscale Funnel is enabled; configured surfaces may be reachable from the public internet if tailnet policy permits it")
	}

	stat, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info.NeedsLogin = true
			info.CertHealth = certHealthForMissingState(opts.TLS)
			return info, nil
		}
		return tsnetStateInfo{}, err
	}
	if !stat.IsDir() {
		return tsnetStateInfo{}, fmt.Errorf("state path is not a directory: %s", resolved)
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err == nil && canonical != resolved {
		resolved = canonical
		info.StateDir = resolved
		info.LockPath = filepath.Join(resolved, remote.StateLockName)
		if remote.LooksLikeSyncedPath(resolved) {
			appendTSNetWarning(&info, "state directory appears to be under a sync folder")
		}
	}
	info.Exists = true
	info.NeedsLogin = !hasTSNetAuthState(resolved)
	if info.NeedsLogin {
		info.State = "needs_login"
	}

	certState := deps.readCertState(resolved, opts.TLS)
	info.CertHealth = certState.Health
	info.CertError = certState.Error

	lock, err := deps.acquireStateLock(resolved)
	if err != nil {
		if errors.Is(err, remote.ErrAlreadyLocked) {
			info.Locked = true
			info.Running = true
			applyTSNetHealth(ctx, opts, &info, certState, deps)
			return info, nil
		}
		return tsnetStateInfo{}, err
	}
	if lock != nil {
		_ = lock.Close()
	}
	if info.NeedsLogin {
		return info, nil
	}
	info.State = "stopped"
	return info, nil
}
