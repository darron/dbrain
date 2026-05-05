package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	tslocal "tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"

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

func replaceURLHost(rawURL string, host string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(host) == "" {
		return rawURL
	}
	parsed.Host = hostWithStatusPort(host, parsed.Port(), parsed.Scheme)
	return parsed.String()
}

func tsnetStatusURLs(opts remote.Options, host string) (string, string) {
	if strings.TrimSpace(host) == "" {
		if strings.TrimSpace(opts.ControlURL) != "" {
			return "", ""
		}
		host = opts.Hostname
	}
	scheme := "https"
	if !opts.TLS {
		scheme = "http"
	}
	base := scheme + "://" + hostWithStatusPort(host, opts.Listen, scheme)
	var webURL string
	var mcpURL string
	if opts.Web {
		webURL = base + "/"
	}
	if opts.MCP {
		mcpURL = base + opts.MCPPath
	}
	return webURL, mcpURL
}

func hostWithStatusPort(host string, listen string, scheme string) string {
	port := listenPort(listen)
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			return "[" + host + "]"
		}
		return host
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return net.JoinHostPort(host, port)
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func listenPort(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return ""
	}
	if strings.HasPrefix(listen, ":") {
		return strings.TrimPrefix(listen, ":")
	}
	if host, port, err := net.SplitHostPort(listen); err == nil && (host != "" || port != "") {
		return port
	}
	if !strings.Contains(listen, ":") {
		return listen
	}
	return ""
}

func lookupTSNetIPs(ctx context.Context, host string) []string {
	if strings.TrimSpace(host) == "" {
		return nil
	}
	resolver := net.DefaultResolver
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return lookupTSNetPeerIPs(ctx, host)
	}
	values := make([]string, 0, len(ips))
	for _, ip := range ips {
		values = append(values, ip.String())
	}
	if len(values) == 0 {
		return lookupTSNetPeerIPs(ctx, host)
	}
	return values
}

func lookupTSNetPeerIPs(ctx context.Context, host string) []string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return nil
	}
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, err := (&tslocal.Client{}).Status(statusCtx)
	if err != nil || status == nil {
		return nil
	}
	if ips := peerStatusIPs(status.Self, host); len(ips) > 0 {
		return ips
	}
	for _, peer := range status.Peer {
		if ips := peerStatusIPs(peer, host); len(ips) > 0 {
			return ips
		}
	}
	return nil
}

func peerStatusIPs(peer *ipnstate.PeerStatus, host string) []string {
	if peer == nil {
		return nil
	}
	dnsName := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(peer.DNSName)), ".")
	hostName := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(peer.HostName)), ".")
	shortDNS := dnsName
	if dot := strings.Index(shortDNS, "."); dot > 0 {
		shortDNS = shortDNS[:dot]
	}
	if host != dnsName && host != hostName && host != shortDNS {
		return nil
	}
	values := make([]string, 0, len(peer.TailscaleIPs))
	for _, ip := range peer.TailscaleIPs {
		values = append(values, ip.String())
	}
	return values
}

func probeTSNetEndpoint(ctx context.Context, rawURL string, tlsServerName string) tsnetEndpointProbe {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return tsnetEndpointProbe{Error: err.Error(), CertHealth: "unknown"}
	}
	transport := cloneDefaultHTTPTransport()
	if strings.TrimSpace(tlsServerName) != "" {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: tlsServerName}
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second, Transport: transport}).Do(req)
	if err != nil {
		probe := tsnetEndpointProbe{Error: err.Error(), CertHealth: "unknown"}
		if dnsNames := certDNSNamesFromError(err); len(dnsNames) > 0 {
			probe.CertDNSNames = dnsNames
			probe.CertHealth = "error"
			probe.CertError = err.Error()
		}
		return probe
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	certHealth, certError := responseCertHealth(resp)
	return tsnetEndpointProbe{
		Reachable:    resp.StatusCode < 500,
		StatusCode:   resp.StatusCode,
		EffectiveURL: rawURL,
		CertHealth:   certHealth,
		CertError:    certError,
	}
}

func cloneDefaultHTTPTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	return base.Clone()
}

func certDNSNamesFromError(err error) []string {
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) && hostnameErr.Certificate != nil {
		return hostnameErr.Certificate.DNSNames
	}
	return nil
}

func responseCertHealth(resp *http.Response) (string, string) {
	if resp == nil || resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return "unknown", ""
	}
	cert := resp.TLS.PeerCertificates[0]
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return "not_yet_valid", fmt.Sprintf("certificate not valid before %s", cert.NotBefore.Format(time.RFC3339))
	}
	if now.After(cert.NotAfter) {
		return "expired", fmt.Sprintf("certificate expired at %s", cert.NotAfter.Format(time.RFC3339))
	}
	return "ok", ""
}
