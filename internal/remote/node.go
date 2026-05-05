package remote

import (
	"context"
	"fmt"
	"io"
	"net"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

type tsnetNode struct {
	server *tsnet.Server
}

func newTSNetNode(opts Options, auth SecretResult, userLogf func(string, ...any), logOut io.Writer) remoteNode {
	ts := &tsnet.Server{
		Dir:           opts.StateDir,
		Hostname:      opts.Hostname,
		AuthKey:       auth.Value,
		AdvertiseTags: opts.AdvertiseTags,
		ControlURL:    opts.ControlURL,
		UserLogf:      userLogf,
	}
	if opts.Verbose {
		ts.Logf = func(format string, args ...any) {
			_, _ = fmt.Fprintf(logOut, "tsnet debug: "+format+"\n", args...)
		}
	}
	return tsnetNode{server: ts}
}

func (n tsnetNode) Up(ctx context.Context) (*ipnstate.Status, error) {
	return n.server.Up(ctx)
}

func (n tsnetNode) LocalClient() (whoIsClient, error) {
	return n.server.LocalClient()
}

func (n tsnetNode) Listen(network string, addr string) (net.Listener, error) {
	return n.server.Listen(network, addr)
}

func (n tsnetNode) ListenTLS(network string, addr string) (net.Listener, error) {
	return n.server.ListenTLS(network, addr)
}

func (n tsnetNode) Close() error {
	return n.server.Close()
}
