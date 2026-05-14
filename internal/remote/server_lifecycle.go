package remote

import (
	"context"
	"errors"
	"net"
)

func listen(ts remoteNode, opts Options) (net.Listener, error) {
	if opts.Funnel {
		return ts.ListenFunnel("tcp", opts.Listen)
	}
	if opts.TLS {
		return ts.ListenTLS("tcp", opts.Listen)
	}
	return ts.Listen("tcp", opts.Listen)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}
