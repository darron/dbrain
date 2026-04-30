package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	tslocal "tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"

	"github.com/darron/dbrain/internal/remote"
)

func newTSNetCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tsnet",
		Short: "Inspect or reset built-in Tailscale state",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newTSNetStatusCommand(root), newTSNetResetCommand(root))
	return cmd
}

func newTSNetStatusCommand(root *rootOptions) *cobra.Command {
	flags := defaultTSNetStateFlags()
	var jsonOut bool

	cmd := &cobra.Command{
		Use:         "status",
		Short:       "Print resolved tsnet state status",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			if err := applyTSNetStateFlagOverrides(cmd, cfg.DataDir, &opts, flags); err != nil {
				return err
			}

			status, err := tsnetStateStatus(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hostname: %s\n", status.Hostname)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "state_dir: %s\n", status.StateDir)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "exists: %t\n", status.Exists)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "locked: %t\n", status.Locked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "running: %t\n", status.Running)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "reachable: %t\n", status.Reachable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "web_reachable: %t\n", status.WebReachable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp_reachable: %t\n", status.MCPReachable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tls: %t\n", status.TLS)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "control_url: %s\n", status.ControlURL)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", status.State)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cert_health: %s\n", status.CertHealth)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "needs_login: %t\n", status.NeedsLogin)
			if len(status.TailnetIPs) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tailnet_ips: %s\n", strings.Join(status.TailnetIPs, ", "))
			}
			if status.WebURL != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "web_url: %s\n", status.WebURL)
			}
			if status.MCPURL != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp_url: %s\n", status.MCPURL)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "lock_path: %s\n", status.LockPath)
			if status.WebError != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "web_error: %s\n", status.WebError)
			}
			if status.MCPError != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp_error: %s\n", status.MCPError)
			}
			if status.CertError != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cert_error: %s\n", status.CertError)
			}
			if status.Warning != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", status.Warning)
			}
			return nil
		},
	}

	addTSNetStateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print status as JSON")

	return cmd
}

func newTSNetResetCommand(root *rootOptions) *cobra.Command {
	flags := defaultTSNetStateFlags()
	var yes bool

	cmd := &cobra.Command{
		Use:         "reset",
		Short:       "Remove built-in Tailscale tsnet state",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			if err := applyTSNetStateFlagOverrides(cmd, cfg.DataDir, &opts, flags); err != nil {
				return err
			}

			resolved, err := remote.ResolveStateDir(opts.StateDir)
			if err != nil {
				return err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "State directory does not exist: %s\n", resolved)
					return nil
				}
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("state path is not a directory: %s", resolved)
			}

			prepared, err := remote.PrepareStateDir(resolved)
			if err != nil {
				return err
			}
			lock, err := remote.AcquireStateLock(prepared)
			if err != nil {
				return err
			}
			defer func() {
				_ = lock.Close()
			}()

			if !yes {
				ok, err := confirmTSNetReset(cmd.InOrStdin(), cmd.OutOrStdout(), opts.Hostname, prepared)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}

			// The advisory lock proves no running dbrain owns the current state
			// directory. On Unix, RemoveAll unlinks the locked file while this FD
			// stays open; a later process may recreate a fresh state dir/lock.
			if err := os.RemoveAll(prepared); err != nil {
				return fmt.Errorf("remove tsnet state dir %s: %w", prepared, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed tsnet state directory: %s\n", prepared)
			return nil
		},
	}

	addTSNetStateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&yes, "yes", false, "Reset without interactive confirmation")

	return cmd
}

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

func readTSNetCertState(stateDir string, tlsEnabled bool) tsnetCertState {
	if !tlsEnabled {
		return tsnetCertState{Health: "disabled"}
	}
	certsDir := filepath.Join(stateDir, "certs")
	entries, err := os.ReadDir(certsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tsnetCertState{Health: "missing"}
		}
		return tsnetCertState{Health: "unknown", Error: err.Error()}
	}
	var invalid []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".crt") {
			continue
		}
		cert, err := readFirstPEMCert(filepath.Join(certsDir, entry.Name()))
		if err != nil {
			invalid = append(invalid, err.Error())
			continue
		}
		health, certErr := certValidity(cert)
		return tsnetCertState{Health: health, Error: certErr, Domains: cert.DNSNames}
	}
	if len(invalid) > 0 {
		return tsnetCertState{Health: "invalid", Error: strings.Join(invalid, "; ")}
	}
	return tsnetCertState{Health: "missing"}
}

func readFirstPEMCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM certificate in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate %s: %w", path, err)
	}
	return cert, nil
}

func certValidity(cert *x509.Certificate) (string, string) {
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return "not_yet_valid", fmt.Sprintf("certificate not valid before %s", cert.NotBefore.Format(time.RFC3339))
	}
	if now.After(cert.NotAfter) {
		return "expired", fmt.Sprintf("certificate expired at %s", cert.NotAfter.Format(time.RFC3339))
	}
	return "ok", ""
}

func hasTSNetAuthState(stateDir string) bool {
	info, err := os.Stat(filepath.Join(stateDir, "tailscaled.state"))
	return err == nil && info.Size() > 0
}

func certHealthForMissingState(tlsEnabled bool) string {
	if !tlsEnabled {
		return "disabled"
	}
	return "missing"
}

type tsnetStateFlags struct {
	webEnabled bool
	mcpEnabled bool
	mcpPath    string
	hostname   string
	stateDir   string
	listen     string
	tlsEnabled bool
	controlURL string
}

func defaultTSNetStateFlags() tsnetStateFlags {
	return tsnetStateFlags{
		webEnabled: true,
		mcpEnabled: true,
		mcpPath:    remote.DefaultMCPPath,
		tlsEnabled: true,
	}
}

func addTSNetStateFlags(cmd *cobra.Command, flags *tsnetStateFlags) {
	cmd.Flags().BoolVar(&flags.webEnabled, "web", true, "Configured remote web surface")
	cmd.Flags().BoolVar(&flags.mcpEnabled, "mcp", true, "Configured remote MCP surface")
	cmd.Flags().StringVar(&flags.mcpPath, "mcp-path", remote.DefaultMCPPath, "Remote MCP endpoint path")
	cmd.Flags().StringVar(&flags.hostname, "tsnet-hostname", "", "Stable tailnet machine name used to derive the default state directory")
	cmd.Flags().StringVar(&flags.stateDir, "tsnet-state-dir", "", "Durable tsnet state directory")
	cmd.Flags().StringVar(&flags.listen, "tsnet-listen", "", "Tailnet listen address")
	cmd.Flags().BoolVar(&flags.tlsEnabled, "tsnet-tls", true, "Use Tailscale HTTPS via ListenTLS")
	cmd.Flags().StringVar(&flags.controlURL, "tsnet-control-url", "", "Experimental alternate Tailscale control server URL")
}

func applyTSNetStateFlagOverrides(cmd *cobra.Command, dataDir string, opts *remote.Options, flags tsnetStateFlags) error {
	changed := cmd.Flags().Changed
	if changed("web") {
		opts.Web = flags.webEnabled
	}
	if changed("mcp") {
		opts.MCP = flags.mcpEnabled
	}
	if changed("mcp-path") {
		opts.MCPPath = flags.mcpPath
	}
	if changed("tsnet-hostname") {
		opts.Hostname = flags.hostname
		if !changed("tsnet-state-dir") {
			opts.StateDir = filepath.Join(dataDir, "tsnet", opts.Hostname)
		}
	}
	if changed("tsnet-state-dir") {
		opts.StateDir = flags.stateDir
	}
	if changed("tsnet-listen") {
		opts.Listen = flags.listen
	}
	if changed("tsnet-tls") {
		opts.TLS = flags.tlsEnabled
		if !changed("tsnet-listen") {
			opts.Listen = ""
		}
	}
	if changed("tsnet-control-url") {
		opts.ControlURL = flags.controlURL
	}
	finalizeRemoteServeDefaults(dataDir, opts)
	cleaned, err := remote.ValidateMCPPath(opts.MCPPath)
	if err != nil {
		return err
	}
	opts.MCPPath = cleaned
	return nil
}

func confirmTSNetReset(in io.Reader, out io.Writer, hostname string, stateDir string) (bool, error) {
	_, _ = fmt.Fprintf(out, "Remove tsnet state directory?\n")
	_, _ = fmt.Fprintf(out, "Hostname: %s\n", hostname)
	_, _ = fmt.Fprintf(out, "State dir: %s\n", stateDir)
	_, _ = fmt.Fprintf(out, "This will require dbrain to authenticate with Tailscale again.\n")
	_, _ = fmt.Fprintf(out, "Type 'reset' to continue: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(scanner.Text()) == "reset", nil
}
