package app

import (
	"context"
	"net"
	"strings"
	"time"

	tslocal "tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

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
