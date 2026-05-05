package app

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
