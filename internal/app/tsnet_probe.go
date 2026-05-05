package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/darron/dbrain/internal/remote"
)

type tsnetProbeKind string

const (
	tsnetProbeWeb tsnetProbeKind = "web"
	tsnetProbeMCP tsnetProbeKind = "mcp"
)

func probeTSNetStatusURL(ctx context.Context, opts remote.Options, deps tsnetStatusDeps, rawURL string, certHost string, ipFallbacks []string, kind tsnetProbeKind) tsnetEndpointProbe {
	probe := classifyTSNetProbe(kind, deps.probeEndpoint(ctx, rawURL, ""))
	if probe.Reachable || !opts.TLS || certHost == "" || certHost == opts.Hostname {
		if probe.Reachable || len(ipFallbacks) == 0 {
			return probe
		}
	}
	if opts.TLS && certHost != "" && certHost != opts.Hostname {
		alternateURL := replaceURLHost(rawURL, opts.Hostname)
		if alternateURL != rawURL {
			alternateProbe := classifyTSNetProbe(kind, deps.probeEndpoint(ctx, alternateURL, certHost))
			if alternateProbe.Reachable {
				alternateProbe.EffectiveURL = rawURL
				return alternateProbe
			}
			mergeProbeError(&probe, alternateURL, certHost, alternateProbe)
		}
	}
	for _, ip := range ipFallbacks {
		ipURL := replaceURLHost(rawURL, ip)
		if ipURL == rawURL {
			continue
		}
		serverName := certHost
		if serverName == "" {
			serverName = opts.Hostname
		}
		ipProbe := classifyTSNetProbe(kind, deps.probeEndpoint(ctx, ipURL, serverName))
		if ipProbe.Reachable {
			ipProbe.EffectiveURL = rawURL
			return ipProbe
		}
		mergeProbeError(&probe, ipURL, serverName, ipProbe)
	}
	return probe
}

func classifyTSNetProbe(kind tsnetProbeKind, probe tsnetEndpointProbe) tsnetEndpointProbe {
	if probe.StatusCode == 0 {
		return probe
	}
	switch kind {
	case tsnetProbeWeb:
		probe.Reachable = probe.StatusCode >= 200 && probe.StatusCode < 400
	case tsnetProbeMCP:
		probe.Reachable = probe.StatusCode == http.StatusOK || probe.StatusCode == http.StatusMethodNotAllowed
	default:
		probe.Reachable = probe.StatusCode < 500
	}
	if !probe.Reachable && probe.Error == "" {
		probe.Error = fmt.Sprintf("unexpected status %d", probe.StatusCode)
	}
	return probe
}

func mergeProbeError(probe *tsnetEndpointProbe, fallbackURL string, tlsServerName string, fallback tsnetEndpointProbe) {
	if fallback.Error == "" {
		return
	}
	if probe.Error == "" {
		probe.Error = fallback.Error
		return
	}
	probe.Error = fmt.Sprintf("%s; fallback %s with TLS server name %s: %s", probe.Error, fallbackURL, tlsServerName, fallback.Error)
	if fallback.CertHealth != "" && probe.CertHealth != "ok" {
		probe.CertHealth = fallback.CertHealth
	}
	if fallback.CertError != "" {
		probe.CertError = fallback.CertError
	}
}
