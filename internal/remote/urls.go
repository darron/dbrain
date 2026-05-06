package remote

import (
	"net"
	"strings"

	"tailscale.com/ipn/ipnstate"
)

type ServeResult struct {
	StateDir string
	WebURL   string
	MCPURL   string
}

func URLs(status *ipnstate.Status, opts Options) ServeResult {
	result := ServeResult{StateDir: opts.StateDir}
	host := statusHost(status)
	if host == "" {
		return result
	}
	scheme := "https"
	if !opts.TLS {
		scheme = "http"
	}
	base := scheme + "://" + hostWithListenPort(host, opts.Listen, scheme)
	if opts.Web {
		result.WebURL = base + "/"
	}
	if opts.MCP {
		result.MCPURL = base + opts.MCPPath
	}
	return result
}

func statusHost(status *ipnstate.Status) string {
	if status == nil {
		return ""
	}
	if len(status.CertDomains) > 0 {
		return strings.TrimSuffix(status.CertDomains[0], ".")
	}
	if status.Self != nil && strings.TrimSpace(status.Self.DNSName) != "" {
		return strings.TrimSuffix(status.Self.DNSName, ".")
	}
	return ""
}

func hostWithListenPort(host string, listen string, scheme string) string {
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
