package remote

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
)

func TestOptionsFromRuntimeReadsConfigAndEnv(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
tsnet:
  hostname: cfg-host
  tls: false
  startup_timeout: 12s
  web: false
  mcp: true
  mcp_path: /brain-mcp/
  auth_key_ref: env:TS_AUTHKEY
  auth_key_command:
    - op
    - read
    - op://Private/dbrain/tsnet-auth-key
  allow_secret_command: true
  advertise_tags:
    - tag:dbrain
    - tag:research
  control_url: https://control.example
  verbose: true
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("DBRAIN_TSNET_HOSTNAME", "env-host")

	opts, err := OptionsFromRuntime(cfg)
	if err != nil {
		t.Fatalf("OptionsFromRuntime: %v", err)
	}
	if opts.Hostname != "env-host" {
		t.Fatalf("Hostname = %q, want env-host", opts.Hostname)
	}
	if opts.StateDir != filepath.Join(cfg.DataDir, "tsnet", "env-host") {
		t.Fatalf("StateDir = %q", opts.StateDir)
	}
	if opts.Listen != ":80" {
		t.Fatalf("Listen = %q, want :80", opts.Listen)
	}
	if opts.TLS {
		t.Fatalf("TLS = true, want false")
	}
	if opts.StartupTimeout != 12*time.Second {
		t.Fatalf("StartupTimeout = %s, want 12s", opts.StartupTimeout)
	}
	if opts.Web {
		t.Fatalf("Web = true, want false")
	}
	if !opts.MCP {
		t.Fatalf("MCP = false, want true")
	}
	if opts.MCPPath != "/brain-mcp" {
		t.Fatalf("MCPPath = %q, want /brain-mcp", opts.MCPPath)
	}
	if len(opts.AuthKeyCommand) != 3 || opts.AuthKeyCommand[0] != "op" {
		t.Fatalf("AuthKeyCommand = %#v", opts.AuthKeyCommand)
	}
	if len(opts.AdvertiseTags) != 2 || opts.AdvertiseTags[1] != "tag:research" {
		t.Fatalf("AdvertiseTags = %#v", opts.AdvertiseTags)
	}
	if opts.ControlURL != "https://control.example" {
		t.Fatalf("ControlURL = %q", opts.ControlURL)
	}
	if !opts.Verbose {
		t.Fatalf("Verbose = false, want true")
	}
}

func TestOptionsFromRuntimeDoesNotReadAuthKeyCommandEnv(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv("DBRAIN_TSNET_AUTH_KEY_COMMAND", "op,read,op://Private/dbrain/key")

	opts, err := OptionsFromRuntime(cfg)
	if err != nil {
		t.Fatalf("OptionsFromRuntime: %v", err)
	}
	if len(opts.AuthKeyCommand) != 0 {
		t.Fatalf("AuthKeyCommand = %#v, want none", opts.AuthKeyCommand)
	}
}

func TestValidateMCPPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "default", path: "", want: "/mcp"},
		{name: "normal", path: "/mcp", want: "/mcp"},
		{name: "trailing slash", path: "/mcp/", want: "/mcp"},
		{name: "nested", path: "/api/mcp", want: "/api/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateMCPPath(tc.path)
			if err != nil {
				t.Fatalf("ValidateMCPPath: %v", err)
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}

	for _, tc := range []string{"mcp", "/", "/mcp path", "/mcp/../x", "/mcp/.."} {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateMCPPath(tc); err == nil {
				t.Fatalf("ValidateMCPPath(%q) succeeded, want error", tc)
			}
		})
	}
}
