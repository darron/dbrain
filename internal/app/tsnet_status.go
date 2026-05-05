package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/remote"
)

type tsnetStateInfo struct {
	Hostname     string   `json:"hostname"`
	StateDir     string   `json:"state_dir"`
	Exists       bool     `json:"exists"`
	Locked       bool     `json:"locked"`
	Running      bool     `json:"running"`
	Reachable    bool     `json:"reachable"`
	WebReachable bool     `json:"web_reachable"`
	MCPReachable bool     `json:"mcp_reachable"`
	TailnetIPs   []string `json:"tailnet_ips,omitempty"`
	WebURL       string   `json:"web_url,omitempty"`
	MCPURL       string   `json:"mcp_url,omitempty"`
	TLS          bool     `json:"tls"`
	ControlURL   string   `json:"control_url"`
	State        string   `json:"state"`
	CertHealth   string   `json:"cert_health"`
	NeedsLogin   bool     `json:"needs_login"`
	LockPath     string   `json:"lock_path"`
	WebError     string   `json:"web_error,omitempty"`
	MCPError     string   `json:"mcp_error,omitempty"`
	CertError    string   `json:"cert_error,omitempty"`
	Warning      string   `json:"warning,omitempty"`
}

func tsnetStateStatus(ctx context.Context, opts remote.Options) (tsnetStateInfo, error) {
	return tsnetStateStatusWithDeps(ctx, opts, defaultTSNetStatusDeps())
}

type tsnetStatusDeps struct {
	acquireStateLock func(string) (io.Closer, error)
	probeEndpoint    func(context.Context, string, string) tsnetEndpointProbe
	lookupIPs        func(context.Context, string) []string
	readCertState    func(string, bool) tsnetCertState
}

type tsnetEndpointProbe struct {
	Reachable    bool
	StatusCode   int
	Error        string
	CertHealth   string
	CertError    string
	CertDNSNames []string
	EffectiveURL string
}

type tsnetCertState struct {
	Health  string
	Error   string
	Domains []string
}

func defaultTSNetStatusDeps() tsnetStatusDeps {
	return tsnetStatusDeps{
		acquireStateLock: func(stateDir string) (io.Closer, error) {
			return remote.AcquireStateLock(stateDir)
		},
		probeEndpoint: probeTSNetEndpoint,
		lookupIPs:     lookupTSNetIPs,
		readCertState: readTSNetCertState,
	}
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
		ControlURL: opts.ControlURL,
		State:      "not_configured",
	}
	if remote.LooksLikeSyncedPath(resolved) {
		appendTSNetWarning(&info, "state directory appears to be under a sync folder")
	}
	if strings.TrimSpace(opts.ControlURL) != "" {
		appendTSNetWarning(&info, "custom tsnet control URL is experimental; DNS and ListenTLS certificate behavior may differ from Tailscale SaaS")
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

func appendTSNetWarning(info *tsnetStateInfo, warning string) {
	if strings.TrimSpace(warning) == "" || strings.Contains(info.Warning, warning) {
		return
	}
	if info.Warning == "" {
		info.Warning = warning
		return
	}
	info.Warning += "; " + warning
}

func applyTSNetHealth(ctx context.Context, opts remote.Options, info *tsnetStateInfo, certState tsnetCertState, deps tsnetStatusDeps) {
	host := ""
	if len(certState.Domains) > 0 {
		host = certState.Domains[0]
	} else if strings.TrimSpace(opts.ControlURL) == "" {
		host = opts.Hostname
	}
	if host != "" {
		info.TailnetIPs = deps.lookupIPs(ctx, host)
	}
	if len(info.TailnetIPs) == 0 && host != "" && host != opts.Hostname {
		info.TailnetIPs = deps.lookupIPs(ctx, opts.Hostname)
	}
	info.WebURL, info.MCPURL = tsnetStatusURLs(opts, host)

	if info.WebURL != "" {
		probe := probeTSNetStatusURL(ctx, opts, deps, info.WebURL, host, info.TailnetIPs, tsnetProbeWeb)
		if !probe.Reachable && len(probe.CertDNSNames) > 0 {
			info.WebURL, info.MCPURL = tsnetStatusURLs(opts, probe.CertDNSNames[0])
			probe = probeTSNetStatusURL(ctx, opts, deps, info.WebURL, probe.CertDNSNames[0], info.TailnetIPs, tsnetProbeWeb)
		}
		info.WebReachable = probe.Reachable
		info.WebError = probe.Error
		mergeProbeCert(info, probe)
	}
	if info.MCPURL != "" {
		probe := probeTSNetStatusURL(ctx, opts, deps, info.MCPURL, host, info.TailnetIPs, tsnetProbeMCP)
		if !probe.Reachable && len(probe.CertDNSNames) > 0 {
			info.WebURL, info.MCPURL = tsnetStatusURLs(opts, probe.CertDNSNames[0])
			probe = probeTSNetStatusURL(ctx, opts, deps, info.MCPURL, probe.CertDNSNames[0], info.TailnetIPs, tsnetProbeMCP)
		}
		info.MCPReachable = probe.Reachable
		info.MCPError = probe.Error
		mergeProbeCert(info, probe)
	}

	webOK := !opts.Web || info.WebReachable
	mcpOK := !opts.MCP || info.MCPReachable
	info.Reachable = webOK && mcpOK && (opts.Web || opts.MCP)
	switch {
	case info.NeedsLogin:
		info.State = "needs_login"
	case info.Reachable:
		info.State = "healthy"
	default:
		info.State = "down"
	}
}

func mergeProbeCert(info *tsnetStateInfo, probe tsnetEndpointProbe) {
	if probe.CertHealth == "ok" {
		info.CertHealth = "ok"
		info.CertError = ""
		return
	}
	if probe.CertHealth != "" && probe.CertHealth != "unknown" && info.CertHealth != "ok" {
		info.CertHealth = probe.CertHealth
	}
	if probe.CertError != "" {
		info.CertError = probe.CertError
	}
}
