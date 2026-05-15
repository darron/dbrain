package app

import (
	"net"
	"net/url"
	"strings"

	"github.com/darron/dbrain/internal/remote"
)

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
	if !opts.TLS && !opts.Funnel {
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
