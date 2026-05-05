package app

import (
	"context"
	"io"

	"github.com/darron/dbrain/internal/remote"
)

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
