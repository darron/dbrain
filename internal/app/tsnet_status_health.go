package app

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/remote"
)

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
	applyTSNetSchedulerStatus(ctx, opts, info, host, info.TailnetIPs, deps)

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
