package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/version"
	"github.com/darron/dbrain/internal/xmediatranscribe"
)

func TestRootCommandHelpIncludesCoreCommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"import", "sync", "sqlite", "tsnet", "config", "eval", "entity", "topic", "worker", "link", "extract", "hydrate", "transcribe", "ocr", "repair", "serve", "stats", "research", "search", "get", "categorize", "version"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
	}
	for _, value := range []string{"DBRAIN_ROOT", "dbrain config env"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	current := version.Current()
	for _, expected := range []string{
		"commit: " + current.Commit,
		"short: " + current.Short,
		"build_time: " + current.BuildTime,
		"git_status: " + current.GitStatus,
		"go_version: " + current.GoVersion,
		"git_version: " + current.GitVersion,
		"build_platform: " + current.BuildPlatform,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout.String())
		}
	}
	if current.ModulePath != "" && !strings.Contains(stdout.String(), "module_path: "+current.ModulePath) {
		t.Fatalf("expected module_path in output, got %q", stdout.String())
	}
	if current.ModuleVersion != "" && !strings.Contains(stdout.String(), "module_version: "+current.ModuleVersion) {
		t.Fatalf("expected module_version in output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestImportAppleNotesSummarizeDefaultsEnabled(t *testing.T) {
	t.Parallel()

	cmd := newImportAppleNotesCommand(&rootOptions{})
	flag := cmd.Flags().Lookup("summarize")
	if flag == nil {
		t.Fatal("expected summarize flag to exist")
	}
	if flag.DefValue != "true" {
		t.Fatalf("summarize default = %q, want true", flag.DefValue)
	}
	value, err := cmd.Flags().GetBool("summarize")
	if err != nil {
		t.Fatalf("GetBool(summarize): %v", err)
	}
	if !value {
		t.Fatal("summarize flag value = false, want true")
	}
}

func TestWriteAppleNotesProgressSuppressesCurrentScanNoise(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	for _, event := range []applenotes.ProgressEvent{
		{Phase: "decoded_note", Total: 3, SourceKey: "apple-note:default:current"},
		{Phase: "attachments", Total: 3, SourceKey: "apple-note:default:current", Status: "start"},
		{Phase: "attachment", Total: 3, SourceKey: "apple-note:default:current", Status: "extracting"},
		{Phase: "unchanged", Total: 3, SourceKey: "apple-note:default:current"},
	} {
		writeAppleNotesProgress(&dst, event, false)
	}
	if dst.Len() != 0 {
		t.Fatalf("expected scan/current events to be suppressed, got %q", dst.String())
	}

	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "processing", Total: 3, SourceKey: "apple-note:default:summary", Reason: "summary"}, false)
	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "imported", Total: 3, SourceKey: "apple-note:default:summary", Status: "unchanged", SummaryStatus: "ok"}, false)
	if dst.Len() != 0 {
		t.Fatalf("expected summary-only processing/completion noise to be suppressed, got %q", dst.String())
	}

	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "processing", Total: 3, SourceKey: "apple-note:default:new", Reason: "new"}, false)
	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "summarizing", Total: 3, SourceKey: "apple-note:default:new", Status: "created", SummaryStatus: "running"}, false)
	writeAppleNotesProgress(&dst, applenotes.ProgressEvent{Phase: "attachments", Total: 1, SourceKey: "apple-note:default:new", Status: "start"}, false)
	output := dst.String()
	for _, value := range []string{"processing source=apple-note:default:new", "summarizing source=apple-note:default:new", "attachments source=apple-note:default:new"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected actionable progress output to contain %q, got %q", value, output)
		}
	}
}

func TestVersionCommandJSON(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload version.Details
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	current := version.Current()
	if payload.Commit != current.Commit {
		t.Fatalf("Commit = %q, want %q", payload.Commit, current.Commit)
	}
	if payload.Short != current.Short {
		t.Fatalf("Short = %q, want %q", payload.Short, current.Short)
	}
	if payload.GitStatus != current.GitStatus {
		t.Fatalf("GitStatus = %q, want %q", payload.GitStatus, current.GitStatus)
	}
	if payload.GoVersion != current.GoVersion {
		t.Fatalf("GoVersion = %q, want %q", payload.GoVersion, current.GoVersion)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	for key, want := range map[string]string{
		"config_dir":      cfg.ConfigDir,
		"config_file":     cfg.ConfigPath,
		"categories_file": cfg.CategoriesPath,
		"data_dir":        cfg.DataDir,
		"database":        cfg.DBPath,
		"vault_dir":       cfg.VaultDir,
		"temp_dir":        cfg.TempDir,
		"cache_dir":       cfg.CacheDir,
		"log_dir":         cfg.LogDir,
	} {
		if payload[key] != want {
			t.Fatalf("%s = %q, want %q", key, payload[key], want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandUsesRootEnv(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv(rootEnvVar, root)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["database"] != cfg.DBPath {
		t.Fatalf("database = %q, want %q", payload["database"], cfg.DBPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigPathsCommandRootFlagOverridesRootEnv(t *testing.T) {
	envRoot := t.TempDir()
	flagRoot := t.TempDir()
	cfg, err := config.Load(flagRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv(rootEnvVar, envRoot)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", flagRoot, "--no-debug", "config", "paths", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["database"] != cfg.DBPath {
		t.Fatalf("database = %q, want %q", payload["database"], cfg.DBPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigEnvCommandIncludesKnownEnvVars(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "config", "env"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"DBRAIN_ROOT", "DBRAIN_OPENROUTER_API_KEY", "DBRAIN_SOURCE_READER_BASE_URL", "DBRAIN_TSNET_AUTH_KEY_REF", "DBRAIN_R2_SECRET_ACCESS_KEY", "config.yaml"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected config env output to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestConfigEnvCommandMarkdownOutput(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-debug", "config", "env", "--markdown"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"| Environment variable(s) | config.yaml key | Default | Purpose |", "`DBRAIN_ROOT`", "`DBRAIN_OPENROUTER_API_KEY / OPENROUTER_API_KEY`", "`DBRAIN_TSNET_MCP_PATH`"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected markdown config env output to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestServeRemoteHelpIncludesTSNetFlags(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "remote", "--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"--tsnet-hostname", "--tsnet-state-dir", "--mcp-path", "read/write web UI"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected serve remote help to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestServeMCPHelpIncludesTSNetTransport(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "mcp", "--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"stdio, http, or tsnet", "--mcp-path", "--tsnet-hostname", "--tsnet-auth-key-ref"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected serve mcp help to contain %q, got %q", value, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestServeRemoteRequiresAtLeastOneSurface(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "serve", "remote", "--web=false", "--mcp=false"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "at least one surface") {
		t.Fatalf("expected at least-one-surface error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestTSNetStatusReportsResolvedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "status", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	wantStateDir := filepath.Join(root, "data", "tsnet", "dbrain")
	if payload["state_dir"] != wantStateDir {
		t.Fatalf("state_dir = %#v, want %q", payload["state_dir"], wantStateDir)
	}
	if payload["control_url"] != "" {
		t.Fatalf("control_url = %#v, want empty", payload["control_url"])
	}
	if payload["exists"] != false {
		t.Fatalf("exists = %#v, want false", payload["exists"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusAcceptsRemoteSurfaceAndListenFlags(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"--no-debug",
		"tsnet", "status",
		"--json",
		"--web=false",
		"--mcp=true",
		"--mcp-path", "/brain",
		"--tsnet-listen", ":8080",
		"--tsnet-tls=false",
		"--tsnet-control-url", "https://control.example",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["tls"] != false {
		t.Fatalf("tls = %#v, want false", payload["tls"])
	}
	if payload["control_url"] != "https://control.example" {
		t.Fatalf("control_url = %#v", payload["control_url"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusRejectsInvalidMCPPath(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--root", t.TempDir(),
		"--no-debug",
		"tsnet", "status",
		"--mcp-path", "/",
	})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mcp path must not be /") {
		t.Fatalf("ExecuteContext error = %v, want mcp path validation", err)
	}
}

func TestTSNetStatusIgnoresDisabledServeSurfaces(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
tsnet:
  web: false
  mcp: false
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "status"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !strings.Contains(stdout.String(), "state_dir:") {
		t.Fatalf("expected status output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStatusReportsLockedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
tsnet:
  web: false
  mcp: false
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	stateDir, err := remote.PrepareStateDir(filepath.Join(root, "data", "tsnet", "dbrain"))
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lock, err := remote.AcquireStateLock(stateDir)
	if err != nil {
		t.Fatalf("AcquireStateLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "status", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", stdout.String(), err)
	}
	if payload["exists"] != true {
		t.Fatalf("exists = %#v, want true", payload["exists"])
	}
	if payload["locked"] != true {
		t.Fatalf("locked = %#v, want true", payload["locked"])
	}
	if payload["running"] != true {
		t.Fatalf("running = %#v, want true", payload["running"])
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetStateStatusReportsHealthyRunningNode(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: 200, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return []string{"100.64.0.10"}
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "healthy" || !status.Running || !status.Reachable || !status.WebReachable || !status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net/" {
		t.Fatalf("WebURL = %q", status.WebURL)
	}
	if status.MCPURL != "https://dbrain.tailnet.ts.net/mcp" {
		t.Fatalf("MCPURL = %q", status.MCPURL)
	}
	if status.CertHealth != "ok" {
		t.Fatalf("CertHealth = %q", status.CertHealth)
	}
	if status.NeedsLogin {
		t.Fatalf("NeedsLogin = true, want false")
	}
}

func TestTSNetStateStatusUsesSurfaceSpecificProbeStatus(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      false,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: 404, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.WebReachable || status.Reachable || status.State != "down" {
		t.Fatalf("expected web 404 to be down, status=%#v", status)
	}

	opts.Web = false
	opts.MCP = true
	status, err = tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusMethodNotAllowed, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if !status.MCPReachable || !status.Reachable || status.State != "healthy" || status.WebURL != "" {
		t.Fatalf("expected MCP 405 to be healthy without web URL, status=%#v", status)
	}
}

func TestTSNetStateStatusReportsNonDefaultPortAndControlURL(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:        true,
		MCP:        true,
		MCPPath:    "/mcp",
		Hostname:   "dbrain",
		StateDir:   stateDir,
		Listen:     ":8443",
		TLS:        true,
		ControlURL: "https://control.example",
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, _ string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: true, StatusCode: http.StatusOK, EffectiveURL: rawURL, CertHealth: "ok"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.ControlURL != "https://control.example" {
		t.Fatalf("ControlURL = %q", status.ControlURL)
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net:8443/" || status.MCPURL != "https://dbrain.tailnet.ts.net:8443/mcp" {
		t.Fatalf("unexpected URLs: web=%q mcp=%q", status.WebURL, status.MCPURL)
	}
	if !strings.Contains(status.Warning, "custom tsnet control URL is experimental") {
		t.Fatalf("expected custom control warning, got %q", status.Warning)
	}
}

func TestTSNetStateStatusFallsBackToShortHostWithTLSServerName(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	var alternateProbes int
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, tlsServerName string) tsnetEndpointProbe {
			if strings.Contains(rawURL, "dbrain.tailnet.ts.net") {
				return tsnetEndpointProbe{Reachable: false, Error: "no such host", CertHealth: "ok"}
			}
			if strings.Contains(rawURL, "https://dbrain/") && tlsServerName == "dbrain.tailnet.ts.net" {
				alternateProbes++
				return tsnetEndpointProbe{Reachable: true, StatusCode: 200, EffectiveURL: rawURL, CertHealth: "ok"}
			}
			return tsnetEndpointProbe{Reachable: false, Error: "unexpected probe", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "healthy" || !status.Reachable || !status.WebReachable || !status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if status.WebURL != "https://dbrain.tailnet.ts.net/" || status.MCPURL != "https://dbrain.tailnet.ts.net/mcp" {
		t.Fatalf("unexpected URLs: web=%q mcp=%q", status.WebURL, status.MCPURL)
	}
	if alternateProbes != 2 {
		t.Fatalf("alternateProbes = %d, want 2", alternateProbes)
	}
}

func TestTSNetStateStatusFallsBackToPeerIPWithTLSServerName(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	var ipProbes int
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(_ context.Context, rawURL string, tlsServerName string) tsnetEndpointProbe {
			if strings.Contains(rawURL, "100.64.0.10") && tlsServerName == "dbrain.tailnet.ts.net" {
				ipProbes++
				return tsnetEndpointProbe{Reachable: true, StatusCode: 200, EffectiveURL: rawURL, CertHealth: "ok"}
			}
			return tsnetEndpointProbe{Reachable: false, Error: "no such host", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return []string{"100.64.0.10"}
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "healthy" || !status.Reachable || !status.WebReachable || !status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if ipProbes != 2 {
		t.Fatalf("ipProbes = %d, want 2", ipProbes)
	}
}

func TestTSNetStateStatusReportsDownWhenLockedButUnreachable(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte(`state`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(context.Context, string, string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: false, Error: "connection refused", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "ok", Domains: []string{"dbrain.tailnet.ts.net"}}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "down" || !status.Running || status.Reachable || status.WebReachable || status.MCPReachable {
		t.Fatalf("unexpected status = %#v", status)
	}
	if status.WebError == "" || status.MCPError == "" {
		t.Fatalf("expected probe errors, status = %#v", status)
	}
}

func TestTSNetStateStatusReportsNeedsLogin(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	opts := remote.Options{
		Web:      true,
		MCP:      true,
		MCPPath:  "/mcp",
		Hostname: "dbrain",
		StateDir: stateDir,
		Listen:   ":443",
		TLS:      true,
	}
	status, err := tsnetStateStatusWithDeps(context.Background(), opts, tsnetStatusDeps{
		acquireStateLock: func(string) (io.Closer, error) {
			return nil, fmt.Errorf("%w: test", remote.ErrAlreadyLocked)
		},
		probeEndpoint: func(context.Context, string, string) tsnetEndpointProbe {
			return tsnetEndpointProbe{Reachable: false, Error: "not listening", CertHealth: "unknown"}
		},
		lookupIPs: func(context.Context, string) []string {
			return nil
		},
		readCertState: func(string, bool) tsnetCertState {
			return tsnetCertState{Health: "missing"}
		},
	})
	if err != nil {
		t.Fatalf("tsnetStateStatusWithDeps: %v", err)
	}
	if status.State != "needs_login" || !status.NeedsLogin {
		t.Fatalf("unexpected status = %#v", status)
	}
}

func TestTSNetResetRequiresConfirmation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateDir := filepath.Join(root, "data", "tsnet", "dbrain")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stateFile := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(stateFile, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetIn(strings.NewReader("no\n"))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "reset"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected state file to remain after abort: %v", err)
	}
	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Fatalf("expected aborted output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestTSNetResetRefusesLockedState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateDir, err := remote.PrepareStateDir(filepath.Join(root, "data", "tsnet", "dbrain"))
	if err != nil {
		t.Fatalf("PrepareStateDir: %v", err)
	}
	stateFile := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(stateFile, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lock, err := remote.AcquireStateLock(stateDir)
	if err != nil {
		t.Fatalf("AcquireStateLock: %v", err)
	}
	defer func() {
		_ = lock.Close()
	}()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "reset", "--yes"})

	err = cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("expected locked-state error, got %v", err)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected state file to remain after locked reset: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestTSNetResetRemovesStateWithYes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateDir := filepath.Join(root, "data", "tsnet", "dbrain")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "tsnet", "reset", "--yes"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected state dir removed, stat err=%v", err)
	}
	if !strings.Contains(stdout.String(), "Removed tsnet state directory") {
		t.Fatalf("expected removal output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestEvalMCPWriteExample(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--no-caffeinate", "--no-debug", "eval", "mcp", "--write-example", "-"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !strings.Contains(stdout.String(), "expect_any_source_keys") {
		t.Fatalf("expected example eval JSON, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestSyncCommandHelpIncludesAll(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"sync"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "all") {
		t.Fatalf("expected sync help output to contain %q, got %q", "all", output)
	}
}

func TestSyncAllCommandPassesSeparateXMediaAndPhotoOCRLimits(t *testing.T) {
	root := t.TempDir()
	var captured syncjob.Options

	oldRunSyncAll := runSyncAll
	t.Cleanup(func() {
		runSyncAll = oldRunSyncAll
	})
	runSyncAll = func(_ context.Context, cfg config.Config, _ *store.Store, opts syncjob.Options) (syncjob.Stats, error) {
		if cfg.RootDir != root {
			t.Fatalf("expected root %s, got %s", root, cfg.RootDir)
		}
		captured = opts
		now := time.Unix(0, 0).UTC()
		return syncjob.Stats{StartedAt: now, CompletedAt: now}, nil
	}

	cmd := newSyncAllCommand(&rootOptions{root: root})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--json",
		"--x-limit", "7",
		"--x-media-limit", "3",
		"--x-photo-ocr-limit", "5",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if captured.XLimit != 7 {
		t.Fatalf("expected x limit 7, got %d", captured.XLimit)
	}
	if captured.XMediaLimit != 3 {
		t.Fatalf("expected x media limit 3, got %d", captured.XMediaLimit)
	}
	if captured.XPhotoOCRLimit != 5 {
		t.Fatalf("expected x photo OCR limit 5, got %d", captured.XPhotoOCRLimit)
	}
}

func TestResolveSyncAllFlagsUsesRootEnvForUnsetValues(t *testing.T) {
	root := t.TempDir()
	clearSyncEnvForTest(t)
	env := strings.Join([]string{
		"DBRAIN_AUTO_ARCHIVE_MEDIA=true",
		"DBRAIN_APPLE_NOTES_ENABLED=true",
		"DBRAIN_APPLE_NOTES_DB_PATH=/tmp/notes.sqlite",
		"DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS=Archive, Trash",
		"DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS=Work",
		"DBRAIN_APPLE_NOTES_EXCLUDE_SHARED=true",
		"DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS=false",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_OCR=false",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES=12345",
		"DBRAIN_APPLE_NOTES_TESSERACT_BINARY=/opt/bin/tesseract",
		"DBRAIN_SAFARI_TABS_ENABLED=true",
		"DBRAIN_SAFARI_TABS_DB_PATH=/tmp/cloudtabs.db",
		"DBRAIN_SAFARI_TABS_DEVICE=dfone",
		"DBRAIN_SAFARI_TABS_LIMIT=8",
		"DBRAIN_SAFARI_TABS_OLDER_THAN=2h",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if !resolved.archiveMedia || !resolved.appleNotes || !resolved.safariTabs {
		t.Fatalf("expected archive/apple-notes/safari-tabs enabled, got archive=%v apple=%v safari=%v", resolved.archiveMedia, resolved.appleNotes, resolved.safariTabs)
	}
	if resolved.appleNotesDBPath != "/tmp/notes.sqlite" {
		t.Fatalf("appleNotesDBPath = %q", resolved.appleNotesDBPath)
	}
	if got := strings.Join(resolved.appleNotesExcludeFolders, ","); got != "Archive,Trash" {
		t.Fatalf("appleNotesExcludeFolders = %q", got)
	}
	if got := strings.Join(resolved.appleNotesExcludeAccounts, ","); got != "Work" {
		t.Fatalf("appleNotesExcludeAccounts = %q", got)
	}
	if !resolved.appleNotesExcludeShared || !resolved.appleNotesSkipAttachments || !resolved.appleNotesSkipAttachmentOCR {
		t.Fatalf("expected Apple Notes exclusion/skip booleans resolved, got shared=%v skipAttachments=%v skipOCR=%v", resolved.appleNotesExcludeShared, resolved.appleNotesSkipAttachments, resolved.appleNotesSkipAttachmentOCR)
	}
	if resolved.appleNotesAttachmentMaxBytes != 12345 || resolved.appleNotesTesseractBinary != "/opt/bin/tesseract" {
		t.Fatalf("unexpected Apple Notes attachment settings: max=%d tesseract=%q", resolved.appleNotesAttachmentMaxBytes, resolved.appleNotesTesseractBinary)
	}
	if resolved.safariTabsDBPath != "/tmp/cloudtabs.db" || resolved.safariTabsDevice != "dfone" || resolved.safariTabsLimit != 8 || resolved.safariTabsOlderThan != 2*time.Hour {
		t.Fatalf("unexpected Safari tabs settings: db=%q device=%q limit=%d older=%s", resolved.safariTabsDBPath, resolved.safariTabsDevice, resolved.safariTabsLimit, resolved.safariTabsOlderThan)
	}
}

func TestResolveSyncAllFlagsKeepsExplicitValues(t *testing.T) {
	root := t.TempDir()
	clearSyncEnvForTest(t)
	env := strings.Join([]string{
		"DBRAIN_APPLE_NOTES_ENABLED=true",
		"DBRAIN_APPLE_NOTES_DB_PATH=/tmp/env-notes.sqlite",
		"DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS=Env",
		"DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS=false",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES=999",
		"DBRAIN_SAFARI_TABS_ENABLED=true",
		"DBRAIN_SAFARI_TABS_DB_PATH=/tmp/env-cloudtabs.db",
		"DBRAIN_SAFARI_TABS_DEVICE=env-device",
		"DBRAIN_SAFARI_TABS_LIMIT=20",
		"DBRAIN_SAFARI_TABS_OLDER_THAN=4h",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	resolved, err := resolveSyncAllFlags(root, syncAllFlags{
		appleNotes:                   true,
		appleNotesDBPath:             "/tmp/explicit-notes.sqlite",
		appleNotesExcludeFolders:     []string{"Explicit"},
		appleNotesAttachmentMaxBytes: 10,
		safariTabs:                   true,
		safariTabsDBPath:             "/tmp/explicit-cloudtabs.db",
		safariTabsDevice:             "explicit-device",
		safariTabsLimit:              3,
		safariTabsOlderThan:          30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("resolveSyncAllFlags: %v", err)
	}
	if resolved.appleNotesDBPath != "/tmp/explicit-notes.sqlite" || strings.Join(resolved.appleNotesExcludeFolders, ",") != "Explicit" {
		t.Fatalf("explicit Apple Notes values were not preserved: db=%q folders=%v", resolved.appleNotesDBPath, resolved.appleNotesExcludeFolders)
	}
	if resolved.appleNotesAttachmentMaxBytes != 10 {
		t.Fatalf("appleNotesAttachmentMaxBytes = %d, want 10", resolved.appleNotesAttachmentMaxBytes)
	}
	if resolved.safariTabsDBPath != "/tmp/explicit-cloudtabs.db" || resolved.safariTabsDevice != "explicit-device" || resolved.safariTabsLimit != 3 || resolved.safariTabsOlderThan != 30*time.Minute {
		t.Fatalf("explicit Safari values were not preserved: db=%q device=%q limit=%d older=%s", resolved.safariTabsDBPath, resolved.safariTabsDevice, resolved.safariTabsLimit, resolved.safariTabsOlderThan)
	}
}

func clearSyncEnvForTest(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DBRAIN_AUTO_ARCHIVE_MEDIA",
		"DBRAIN_ARCHIVE_AUTO",
		"DBRAIN_APPLE_NOTES_ENABLED",
		"DBRAIN_APPLE_NOTES_DB_PATH",
		"DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS",
		"DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS",
		"DBRAIN_APPLE_NOTES_EXCLUDE_SHARED",
		"DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS",
		"DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_OCR",
		"DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR",
		"DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES",
		"DBRAIN_APPLE_NOTES_TESSERACT_BINARY",
		"DBRAIN_SAFARI_TABS_ENABLED",
		"DBRAIN_SAFARI_TABS_DB_PATH",
		"DBRAIN_SAFARI_TABS_DEVICE",
		"DBRAIN_SAFARI_TABS_LIMIT",
		"DBRAIN_SAFARI_TABS_OLDER_THAN",
	} {
		t.Setenv(key, "")
	}
}

func TestImportCommandHelpIncludesYouTubeImporter(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"import"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"github", "x-bookmarks", "youtube"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected import help output to contain %q, got %q", value, output)
		}
	}
}

func TestServeCommandHelpIncludesSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"mcp", "web"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected serve help output to contain %q, got %q", value, output)
		}
	}
}

func TestTopicCommandHelpIncludesTopicSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"topic"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"map", "generate", "refresh", "index"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected topic help output to contain %q, got %q", value, output)
		}
	}
}

func TestEntityCommandHelpIncludesSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"entity"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"map", "generate", "index"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected entity help output to contain %q, got %q", value, output)
		}
	}
}

func TestExtractCommandHelpIncludesLinksAndSources(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"extract"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"links", "sources"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected extract help output to contain %q, got %q", value, output)
		}
	}
}

func TestTranscribeCommandHelpIncludesXMedia(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"transcribe"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "x-media") {
		t.Fatalf("expected transcribe help output to contain %q, got %q", "x-media", output)
	}
}

func TestOCRCommandHelpIncludesXPhotos(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"ocr"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "x-photos") {
		t.Fatalf("expected ocr help output to contain %q, got %q", "x-photos", output)
	}
}

func TestWorkerCommandHelpIncludesSources(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"worker"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "sources") {
		t.Fatalf("expected worker help output to contain %q, got %q", "sources", output)
	}
}

func TestLinkCommandHelpIncludesAdd(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"link"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "add") {
		t.Fatalf("expected link help output to contain add, got %q", output)
	}
}

func TestLinkAddQueuesManualSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "link", "add", "https://example.com/manual?utm_source=test"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v stderr=%s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Queued: 1") || !strings.Contains(output, "created src:") || !strings.Contains(output, "https://example.com/manual") {
		t.Fatalf("unexpected link add output %q", output)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()
	pending, err := st.ListSourcesForEnrichment(context.Background(), 10, false, true, "dbrain-v1", "summarize", "0.1.0")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(pending) != 1 || pending[0].CanonicalURL != "https://example.com/manual" {
		t.Fatalf("expected pending normalized manual source, got %+v", pending)
	}
}

func TestRepairSourcesRequiresConfirmationAndResetsDomain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	sourceResult, err := st.UpsertSource(context.Background(), model.SourceCandidate{
		SourceKey:     "src:repair-canada",
		OriginalURL:   "https://canada.ca/en/news",
		CanonicalURL:  "https://canada.ca/en/news",
		NormalizedURL: "https://canada.ca/en/news",
		SourceType:    "web",
		Domain:        "canada.ca",
		NotePath:      "sources/web/repair-canada.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), sourceResult.SourceID, model.ExtractResult{
		CanonicalURL: "https://canada.ca/en/news",
		FinalURL:     "https://canada.ca/en/news",
		Title:        "Canada source",
		Content:      "Canada source text",
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "summarize",
		ToolVersion:  "test-version",
	}, "hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), sourceResult.SourceID, model.SummaryResult{
		Text:          "Canada source summary",
		Status:        "ok",
		Model:         "test-model",
		PromptVersion: "dbrain-v1",
		Tool:          "summarize",
		ToolVersion:   "test-version",
		FetchedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "repair", "sources", "--domain", "canada.ca"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v stderr=%s", err, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "This will reset extraction and summary state for 1 sources") ||
		!strings.Contains(output, "Sources reset: 1") {
		t.Fatalf("unexpected repair sources output %q", output)
	}

	st, err = store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()
	source, err := st.GetSourceByID(context.Background(), sourceResult.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.ExtractStatus != "" || source.SummaryStatus != "" || source.ExtractedText != "" || source.SummaryText != "" {
		t.Fatalf("expected source enrichment reset, got %+v", source)
	}
}

func TestWriteSyncStatsIncludesXMediaStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.April, 24, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.April, 24, 15, 2, 0, 0, time.UTC),
		Duration:    2 * time.Minute,
		XMedia: &syncjob.XMediaStage{
			Stats: xmediatranscribe.Stats{
				ItemsProcessed:   10,
				ItemsUpdated:     6,
				ItemsSkipped:     4,
				MediaTranscribed: 6,
				Errors:           1,
				SummaryErrors:    2,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "X Media", "processed=10 transcribed=6", "summarized=0 skipped=4", "3"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestWriteSyncStatsIncludesAppleNotesStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.April, 24, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.April, 24, 15, 2, 0, 0, time.UTC),
		Duration:    2 * time.Minute,
		AppleNotes: &syncjob.AppleNotesStage{
			Stats: applenotes.Stats{
				NotesImported:        8,
				NotesRendered:        6,
				NotesSkipped:         2,
				NotesBlocked:         1,
				AttachmentsIndexed:   4,
				AttachmentsExtracted: 3,
				SummariesCreated:     5,
				Errors:               1,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "Apple Notes", "imported=8 rendered=6", "skipped=2 blocked=1 attachments=4 extracted=3 summarized=5", "1"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestWriteSyncStatsIncludesSafariTabsStage(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	stats := syncjob.Stats{
		StartedAt:   time.Date(2026, time.May, 1, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.May, 1, 15, 1, 0, 0, time.UTC),
		Duration:    time.Minute,
		SafariTabs: &syncjob.SafariTabsStage{
			Stats: safaritabs.Stats{
				DeviceName:    "dfone",
				TabsImported:  498,
				TabsCreated:   1,
				TabsUpdated:   2,
				TabsUnchanged: 495,
				TabsRendered:  498,
				TabsSkipped:   2,
				LinksFound:    492,
				Errors:        1,
			},
		},
	}

	if err := writeSyncStats(&dst, stats); err != nil {
		t.Fatalf("writeSyncStats: %v", err)
	}

	output := dst.String()
	for _, value := range []string{"Sync Summary", "Safari Tabs", "created=1 updated=2", "unchanged=495 rendered=498 skipped=2 links=492 device=dfone", "1"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync stats output to contain %q, got %q", value, output)
		}
	}
}

func TestSyncProgressUIFormatsStageLines(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	ui := newSyncProgressUI(&dst)
	_, _ = fmt.Fprintln(ui, "Sync started at 2026-04-26T21:01:56Z")
	_, _ = fmt.Fprintln(ui, "==> hydrate x")
	_, _ = fmt.Fprintln(ui, "X hydration complete: hydrated=4 missing=0 api_errors=0 media_downloaded=3 media_errors=0 rendered=4 (3s)")
	ui.Close()

	output := dst.String()
	for _, value := range []string{"Sync started at", "Hydrating X posts and media", "X hydration complete"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync progress output to contain %q, got %q", value, output)
		}
	}
	if strings.Contains(output, "==>") {
		t.Fatalf("expected raw stage marker to be formatted, got %q", output)
	}
}

func TestSyncProgressUILogWriterPreservesDebugLines(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	ui := newSyncProgressUI(&dst)
	_, _ = fmt.Fprintln(ui, "==> import youtube")
	_, _ = fmt.Fprintln(ui.LogWriter(), `time=2026-04-26T15:03:43.870-06:00 level=DEBUG msg="loading youtube feed"`)
	_, _ = fmt.Fprintln(ui, "YouTube import complete: items_processed=100 sources_summarized=0 errors=0 (3s)")
	ui.Close()

	output := dst.String()
	for _, value := range []string{"Importing YouTube feeds", "DEBUG", "15:03:43.870", "loading youtube feed", "YouTube import complete"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected sync progress output to contain %q, got %q", value, output)
		}
	}
	if strings.Contains(output, "==>") {
		t.Fatalf("expected raw stage marker to be formatted, got %q", output)
	}
}

func TestSyncProgressUIFormatsStructuredLogFields(t *testing.T) {
	t.Parallel()

	var dst bytes.Buffer
	ui := newSyncProgressUI(&dst)
	_, _ = fmt.Fprintln(ui.LogWriter(), `time=2026-04-26T15:02:01.505-06:00 level=DEBUG msg="source enrichment candidates loaded" sources=1 limit=5000 summarize=true`)
	ui.Close()

	output := dst.String()
	for _, value := range []string{"DEBUG", "15:02:01.505", "source enrichment candidates loaded", "sources=1", "limit=5000", "summarize=true"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected formatted log output to contain %q, got %q", value, output)
		}
	}
	if strings.Contains(output, `msg="source enrichment candidates loaded"`) {
		t.Fatalf("expected msg field to be promoted, got %q", output)
	}
}

func TestLoadConfigRemovesLegacyRootSummaryTempFiles(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "dbrain-summary-legacy.md")
	if err := os.WriteFile(legacy, []byte("legacy summary temp"), 0o644); err != nil {
		t.Fatalf("write legacy temp: %v", err)
	}
	preserved := filepath.Join(root, "keep-me.md")
	if err := os.WriteFile(preserved, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write preserved file: %v", err)
	}

	cfg, err := loadConfig(root)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.TempDir == "" {
		t.Fatal("expected temp dir")
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy temp file to be removed, got err=%v", err)
	}
	if _, err := os.Stat(preserved); err != nil {
		t.Fatalf("expected unrelated root file to remain: %v", err)
	}
}

func TestLoadConfigKeepsSummaryTempFilesUnderTmp(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	tmpFile := filepath.Join(cfg.TempDir, "dbrain-summary-active.md")
	if err := os.WriteFile(tmpFile, []byte("active summary temp"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	if _, err := loadConfig(root); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("expected tmp summary file to remain, got %v", err)
	}
}

func TestCaffeinateStartsByDefaultForLeafCommandWhenAvailable(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	var called int
	startKeepAwake = func(pid int) error {
		called++
		if pid <= 0 {
			t.Fatalf("expected positive pid, got %d", pid)
		}
		return nil
	}
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if called != 1 {
		t.Fatalf("expected caffeinate to start once, got %d", called)
	}
}

func TestNoCaffeinateDisablesAutomaticKeepAwake(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	var called int
	startKeepAwake = func(pid int) error {
		called++
		return nil
	}
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if called != 0 {
		t.Fatalf("expected automatic caffeinate to be disabled, got %d", called)
	}
}

func TestCaffeinateDebugLogsEnabledByDefault(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	startKeepAwake = func(int) error { return nil }
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "keep-awake: started for pid") {
		t.Fatalf("expected keep-awake debug log, got %q", stderr.String())
	}
}

func TestNoDebugSuppressesKeepAwakeDebugLogs(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	startKeepAwake = func(int) error { return nil }
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-debug", "extract", "sources", "--limit", "1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "keep-awake:") {
		t.Fatalf("expected no keep-awake debug log, got %q", stderr.String())
	}
}

func TestCaffeinateSkipsGroupingHelpCommand(t *testing.T) {
	original := startKeepAwake
	defer func() { startKeepAwake = original }()
	originalAvailable := keepAwakeAvailable
	defer func() { keepAwakeAvailable = originalAvailable }()

	var called int
	startKeepAwake = func(pid int) error {
		called++
		return nil
	}
	keepAwakeAvailable = func() bool { return true }

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"extract"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}
	if called != 0 {
		t.Fatalf("expected caffeinate to be skipped for help command, got %d", called)
	}
}

func TestExtractSourcesCommandOutputsZeroStatsForEmptyBacklog(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--limit", "5"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Sources queued: 0", "Sources summarized: 0", "Errors: 0"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected extract sources output to contain %q, got %q", value, output)
		}
	}
}

func TestExtractCommandsDefaultToParallelSourceEnrichment(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()

	for _, args := range [][]string{
		{"extract", "links"},
		{"extract", "sources"},
	} {
		target, _, err := cmd.Find(args)
		if err != nil {
			t.Fatalf("find %v: %v", args, err)
		}
		flag := target.Flags().Lookup("concurrency")
		if flag == nil {
			t.Fatalf("expected %v to define --concurrency", args)
		}
		if flag.DefValue != "4" {
			t.Fatalf("expected %v --concurrency default 4, got %q", args, flag.DefValue)
		}
	}
}

func TestExtractSourcesCommandUsesTargetedSourceLookup(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-targeted-source",
		SourceType:   "x_bookmark",
		ExternalID:   "test-targeted-source",
		CanonicalURL: "https://x.com/example/status/test-targeted-source",
		Title:        "targeted source item",
		ContentHash:  "item-hash-targeted-source",
		NotePath:     "items/test-targeted-source.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-targeted-source",
		OriginalURL:   "https://example.com/targeted-source",
		CanonicalURL:  "https://example.com/targeted-source",
		NormalizedURL: "https://example.com/targeted-source",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/test-targeted-source.md",
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	oldRunPending := runSourceEnrichPending
	oldRunSourceIDs := runSourceEnrichSourceIDs
	defer func() {
		runSourceEnrichPending = oldRunPending
		runSourceEnrichSourceIDs = oldRunSourceIDs
	}()

	runSourceEnrichPending = func(context.Context, config.Config, *store.Store, sourceenrich.Options) (sourceenrich.Stats, []int64, error) {
		t.Fatal("expected targeted source lookup to bypass backlog run")
		return sourceenrich.Stats{}, nil, nil
	}

	var capturedIDs []int64
	runSourceEnrichSourceIDs = func(_ context.Context, _ config.Config, _ *store.Store, sourceIDs []int64, _ sourceenrich.Options) (sourceenrich.Stats, []int64, error) {
		capturedIDs = append([]int64(nil), sourceIDs...)
		return sourceenrich.Stats{SourcesQueued: len(sourceIDs)}, sourceIDs, nil
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "extract", "sources", "--source", "src:test-targeted-source"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	if len(capturedIDs) != 1 || capturedIDs[0] != link.SourceID {
		t.Fatalf("unexpected targeted source ids: %v want [%d]", capturedIDs, link.SourceID)
	}
	if !strings.Contains(stdout.String(), "Sources queued: 1") {
		t.Fatalf("expected targeted output to report one queued source, got %q", stdout.String())
	}
}

func TestWorkerSourcesCommandOutputsQueueDrainedForEmptyBacklog(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "worker", "sources"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Cycles: 0", "Stopped: queue_drained", "Final source extraction pending: 0", "Final source summary pending: 0", "Duration: "} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected worker output to contain %q, got %q", value, output)
		}
	}
}

func TestTopicMapCommandJSON(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-topic-map",
		SourceType:   "x_bookmark",
		ExternalID:   "test-topic-map",
		CanonicalURL: "https://x.com/example/status/test-topic-map",
		Title:        "Agent Memory Post",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory retrieval system",
		ContentHash:  "topic-map-item-hash",
		NotePath:     "items/x/2026/test-topic-map.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-topic-map",
		OriginalURL:   "https://github.com/test/agent-memory",
		CanonicalURL:  "https://github.com/test/agent-memory",
		NormalizedURL: "https://github.com/test/agent-memory",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-topic-map.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "map", "agent memory", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, `"topic": "agent memory"`) || !strings.Contains(output, `"nodes"`) || !strings.Contains(output, `"entities"`) || !strings.Contains(output, `"pivots"`) || !strings.Contains(output, `"synthesis"`) || !strings.Contains(output, `"key": "github-repo:test/agent-memory"`) {
		t.Fatalf("unexpected topic map output: %q", output)
	}
}

func TestTopicGenerateCommandWritesNote(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-topic-generate",
		SourceType:   "x_bookmark",
		ExternalID:   "test-topic-generate",
		CanonicalURL: "https://x.com/example/status/test-topic-generate",
		Title:        "Vector Database Post",
		AuthorHandle: "vectordb",
		AuthorName:   "Vector DB",
		Text:         "vector database retrieval indexing",
		ContentHash:  "topic-generate-item-hash",
		NotePath:     "items/x/2026/test-topic-generate.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "generate", "vector database", "--source-type", "x_bookmark"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	notePath := filepath.Join(cfg.VaultDir, "topics", "vector-database.md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read topic note: %v", err)
	}
	if !strings.Contains(string(data), "# vector database") && !strings.Contains(strings.ToLower(string(data)), "# vector database") {
		t.Fatalf("unexpected topic note: %q", string(data))
	}
	if !strings.Contains(string(data), "source_types:") || !strings.Contains(string(data), `"x_bookmark"`) {
		t.Fatalf("expected topic note to persist source types, got %q", string(data))
	}
	if !strings.Contains(string(data), "## What This Topic Is") ||
		!strings.Contains(string(data), "## Main Angles") ||
		!strings.Contains(string(data), "## Key People") ||
		!strings.Contains(string(data), "## Open Questions") ||
		!strings.Contains(string(data), "## Suggested Starting Notes") ||
		!strings.Contains(string(data), "## Why It Matters") ||
		!strings.Contains(string(data), "Vector DB") {
		t.Fatalf("expected topic note to include synthesized topic sections, got %q", string(data))
	}

	indexPath := filepath.Join(cfg.VaultDir, "topics", "index.md")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read topic index: %v", err)
	}
	if !strings.Contains(string(indexData), "[[topics/vector-database|vector database]]") {
		t.Fatalf("unexpected topic index: %q", string(indexData))
	}
}

func TestTopicRefreshCommandUsesStoredFrontmatter(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-topic-refresh",
		SourceType:   "x_bookmark",
		ExternalID:   "test-topic-refresh",
		CanonicalURL: "https://x.com/example/status/test-topic-refresh",
		Title:        "Agent Memory Refresh",
		Text:         "agent memory retrieval context",
		ContentHash:  "topic-refresh-item-hash",
		NotePath:     "items/x/2026/test-topic-refresh.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	topicPath := filepath.Join(cfg.VaultDir, "topics", "agent-memory.md")
	if err := os.MkdirAll(filepath.Dir(topicPath), 0o755); err != nil {
		t.Fatalf("mkdir topic dir: %v", err)
	}
	seedNote := `---
brain_topic: "agent memory"
seed_limit: "4"
related_limit: "1"
source_types:
  - "x_bookmark"
tags:
  - "source/topic"
---

# stale
`
	if err := os.WriteFile(topicPath, []byte(seedNote), 0o644); err != nil {
		t.Fatalf("write seed topic note: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "refresh", "agent memory"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Topics refreshed: 1") {
		t.Fatalf("unexpected refresh output: %q", output)
	}

	data, err := os.ReadFile(topicPath)
	if err != nil {
		t.Fatalf("read refreshed topic note: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "# agent memory") {
		t.Fatalf("expected refreshed topic heading, got %q", body)
	}
	if !strings.Contains(body, `seed_limit: "4"`) || !strings.Contains(body, `related_limit: "1"`) {
		t.Fatalf("expected stored limits to be preserved, got %q", body)
	}
	if !strings.Contains(body, `"x_bookmark"`) {
		t.Fatalf("expected stored source types to be preserved, got %q", body)
	}
}

func TestTopicIndexCommandWritesIndex(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	topicsDir := filepath.Join(cfg.VaultDir, "topics")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatalf("mkdir topics dir: %v", err)
	}
	first := `---
brain_topic: "agent memory"
seed_limit: "6"
related_limit: "2"
source_types:
  - "github"
---
`
	second := `---
brain_topic: "vector database"
seed_limit: "5"
related_limit: "1"
source_types: []
---
`
	if err := os.WriteFile(filepath.Join(topicsDir, "agent-memory.md"), []byte(first), 0o644); err != nil {
		t.Fatalf("write first topic note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(topicsDir, "vector-database.md"), []byte(second), 0o644); err != nil {
		t.Fatalf("write second topic note: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "topic", "index"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	indexPath := filepath.Join(cfg.VaultDir, "topics", "index.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read topic index: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "[[topics/agent-memory|agent memory]]") || !strings.Contains(body, "[[topics/vector-database|vector database]]") {
		t.Fatalf("unexpected topic index body: %q", body)
	}
}

func TestEntityMapCommandJSON(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-entity-map",
		SourceType:   "x_bookmark",
		ExternalID:   "test-entity-map",
		CanonicalURL: "https://x.com/example/status/test-entity-map",
		Title:        "Entity map item",
		AuthorHandle: "entityauthor",
		AuthorName:   "Entity Author",
		Text:         "entity map body",
		ContentHash:  "entity-map-item-hash",
		NotePath:     "items/x/2026/test-entity-map.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "entity", "map", "entityauthor", "--json"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, `"key": "x-author:entityauthor"`) || !strings.Contains(output, `"kind": "person"`) {
		t.Fatalf("unexpected entity map output: %q", output)
	}
}

func TestEntityGenerateCommandWritesNote(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:test-entity-generate",
		SourceType:   "github_star",
		ExternalID:   "test-entity-generate",
		CanonicalURL: "https://github.com/example/project",
		Title:        "Entity generate item",
		ContentHash:  "entity-generate-item-hash",
		NotePath:     "items/github/test-entity-generate.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-entity-generate",
		OriginalURL:   "https://github.com/example/project",
		CanonicalURL:  "https://github.com/example/project",
		NormalizedURL: "https://github.com/example/project",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/example-project.md",
	}); err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "entity", "generate", "example/project", "--kind", "project"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	notePath := filepath.Join(cfg.VaultDir, "entities", "project", "github-repo-example-project.md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read entity note: %v", err)
	}
	if !strings.Contains(string(data), "# example/project") {
		t.Fatalf("unexpected entity note: %q", string(data))
	}

	indexPath := filepath.Join(cfg.VaultDir, "entities", "index.md")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read entity index: %v", err)
	}
	if !strings.Contains(string(indexData), "[[entities/project/github-repo-example-project|example/project]]") {
		t.Fatalf("unexpected entity index: %q", string(indexData))
	}
}

func TestEntityIndexCommandWritesAllEntityNotes(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-entity-index",
		SourceType:   "x_bookmark",
		ExternalID:   "test-entity-index",
		CanonicalURL: "https://x.com/example/status/test-entity-index",
		Title:        "Entity index item",
		AuthorHandle: "entityindexer",
		AuthorName:   "Entity Indexer",
		Text:         "entity index body",
		ContentHash:  "entity-index-item-hash",
		NotePath:     "items/x/2026/test-entity-index.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-entity-index-site",
		OriginalURL:   "https://example.com/project",
		CanonicalURL:  "https://example.com/project",
		NormalizedURL: "https://example.com/project",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-project.md",
	}); err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "entity", "index"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	for _, path := range []string{
		filepath.Join(cfg.VaultDir, "entities", "person", "x-author-entityindexer.md"),
		filepath.Join(cfg.VaultDir, "entities", "site", "site-example-com.md"),
		filepath.Join(cfg.VaultDir, "entities", "index.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected entity artifact at %s: %v", path, err)
		}
	}
}

func TestRepairNotesCommandRebuildsMissingNotes(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-repair",
		SourceType:   "x_bookmark",
		ExternalID:   "test-repair",
		CanonicalURL: "https://x.com/example/status/test-repair",
		Title:        "Repair test item",
		ContentHash:  "repair-hash",
		NotePath:     "items/x/2026/test-repair.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:repair-source",
		OriginalURL:   "https://example.com/repair",
		CanonicalURL:  "https://example.com/repair",
		NormalizedURL: "https://example.com/repair",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/repair-source.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}

	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/repair",
		FinalURL:     "https://example.com/repair",
		Title:        "Repair source",
		Content:      "source body",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "repair-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "repair", "notes"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	for _, path := range []string{
		filepath.Join(cfg.VaultDir, "items/x/2026/test-repair.md"),
		filepath.Join(cfg.VaultDir, "sources/web/repair-source.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected repaired note at %s: %v", path, err)
		}
	}

	output := stdout.String()
	for _, value := range []string{"Items written: 1", "Sources written: 1", "Errors: 0"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected repair output to contain %q, got %q", value, output)
		}
	}
}

func TestResearchCommandOutputsResearchPack(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/retrieval",
		SourceType:   "github_star",
		ExternalID:   "test/retrieval",
		CanonicalURL: "https://github.com/test/retrieval",
		Title:        "retrieval repo",
		ContentHash:  "hash-retrieval-item",
		NotePath:     "items/github/test-retrieval.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-retrieval",
		OriginalURL:   "https://github.com/test/retrieval",
		CanonicalURL:  "https://github.com/test/retrieval",
		NormalizedURL: "https://github.com/test/retrieval",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-retrieval.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/retrieval",
		FinalURL:     "https://github.com/test/retrieval",
		Title:        "retrieval repo",
		Content:      "This tool handles retrieval for internal knowledge bases.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "hash-retrieval-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "Retrieval-oriented knowledge base tooling.",
		RawJSON:       `{"summary":"Retrieval-oriented knowledge base tooling."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "test retrieval repo", "--retrieval-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Research pack: test retrieval repo", "Mode: evidence_only", "Retrieved evidence:", "[src:test-retrieval] retrieval repo", "summary: Retrieval-oriented knowledge base tooling.", "entity_matches:"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected research output to contain %q, got %q", value, output)
		}
	}
}

func TestResearchCommandSynthesizesByDefault(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "item:test-synthesis",
		SourceType:   "x_bookmark",
		ExternalID:   "test-synthesis",
		CanonicalURL: "https://x.com/example/status/test-synthesis",
		Title:        "Synthesis item",
		Text:         "synthesis retrieval local answer",
		ContentHash:  "hash-synthesis-item",
		NotePath:     "items/x/test-synthesis.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-synthesis",
		OriginalURL:   "https://example.com/synthesis",
		CanonicalURL:  "https://example.com/synthesis",
		NormalizedURL: "https://example.com/synthesis",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-synthesis.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/synthesis",
		FinalURL:     "https://example.com/synthesis",
		Title:        "Synthesis source",
		Content:      "synthesis retrieval source body",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "hash-synthesis-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "Synthesis retrieval should produce a cited local answer.",
		RawJSON:       `{"summary":"Synthesis retrieval should produce a cited local answer."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Synthesis retrieval produces a cited answer [src:test-synthesis]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "synthesis retrieval", "--model", "ollama/qwen-test", "--synthesis-timeout", "5s"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"Answer status: ok", "Model: ollama/qwen-test", "Answer:", "Synthesis retrieval produces a cited answer [src:test-synthesis].", "Citations:", "Research pack:"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected synthesized research output to contain %q, got %q", value, output)
		}
	}
}

func TestResearchCommandSourceTypeFilterLimitsEvidence(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/filter",
		SourceType:   "github_star",
		ExternalID:   "test/filter",
		CanonicalURL: "https://github.com/test/filter",
		Title:        "filter repo",
		ContentHash:  "hash-filter-item",
		NotePath:     "items/github/test-filter.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-filter",
		OriginalURL:   "https://github.com/test/filter",
		CanonicalURL:  "https://github.com/test/filter",
		NormalizedURL: "https://github.com/test/filter",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-filter.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/filter",
		FinalURL:     "https://github.com/test/filter",
		Title:        "filter repo",
		Content:      "This repo helps filter search results.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "hash-filter-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "show me github repos search results", "--source-type", "github", "--retrieval-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "source_type: github") {
		t.Fatalf("expected github source type in output, got %q", output)
	}
	if strings.Contains(output, "source_type: x_bookmark") {
		t.Fatalf("did not expect x evidence in filtered output, got %q", output)
	}
	if strings.Contains(output, "entity_matches:") {
		t.Fatalf("did not expect generic github query to create entity matches, got %q", output)
	}
}

func TestResearchCommandIncludeRelatedAddsLinkedEvidence(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-related-item",
		SourceType:   "x_bookmark",
		ExternalID:   "test-related-item",
		CanonicalURL: "https://x.com/example/status/test-related-item",
		Title:        "Parent item",
		Text:         "special retrieval phrase for related evidence",
		ContentHash:  "hash-related-item",
		NotePath:     "items/x/2026/test-related-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-related-source",
		OriginalURL:   "https://example.com/related",
		CanonicalURL:  "https://example.com/related",
		NormalizedURL: "https://example.com/related",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-related-source.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/related",
		FinalURL:     "https://example.com/related",
		Title:        "Related source",
		Content:      "related source body",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "hash-related-source"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "Linked source summary for the parent item.",
		RawJSON:       `{"summary":"Linked source summary for the parent item."}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("save source summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--no-caffeinate", "research", "special retrieval phrase", "--include-related", "--related-limit", "1", "--limit", "2", "--retrieval-only"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"[x:test-related-item] Parent item", "[src:test-related-source] Related source", "relationship: linked source (x:test-related-item)"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected related research output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsSourcesCommandOutputsSummaryStatusCounts(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/one",
		SourceType:   "github_star",
		ExternalID:   "test/one",
		CanonicalURL: "https://github.com/test/one",
		Title:        "test/one",
		ContentHash:  "hash-one",
		NotePath:     "items/github/test-one.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item one: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-one",
		OriginalURL:   "https://github.com/test/one",
		CanonicalURL:  "https://github.com/test/one",
		NormalizedURL: "https://github.com/test/one",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-one.md",
	})
	if err != nil {
		t.Fatalf("source link one: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/one",
		FinalURL:     "https://github.com/test/one",
		Title:        "test/one",
		Content:      "README one",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "source-hash-one"); err != nil {
		t.Fatalf("save extract one: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "summary one",
		RawJSON:       `{"summary":"summary one"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary one: %v", err)
	}

	item, err = st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test/two",
		SourceType:   "github_star",
		ExternalID:   "test/two",
		CanonicalURL: "https://github.com/test/two",
		Title:        "test/two",
		ContentHash:  "hash-two",
		NotePath:     "items/github/test-two.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item two: %v", err)
	}
	link, err = st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-two",
		OriginalURL:   "https://github.com/test/two",
		CanonicalURL:  "https://github.com/test/two",
		NormalizedURL: "https://github.com/test/two",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-two.md",
	})
	if err != nil {
		t.Fatalf("source link two: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/two",
		FinalURL:     "https://github.com/test/two",
		Title:        "test/two",
		Content:      "README two",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "source-hash-two"); err != nil {
		t.Fatalf("save extract two: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "sources",
		"--source-type", "github",
		"--extract-tool", "github-api",
		"--group-by", "summary-status",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{"ok: 1", "pending: 1", "Total: 2"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected stats output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsActivityCommandOutputsRecentWrites(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:activity/test",
		SourceType:   "github_star",
		ExternalID:   "activity/test",
		CanonicalURL: "https://github.com/activity/test",
		Title:        "activity/test",
		ContentHash:  "activity-hash",
		NotePath:     "items/github/activity-test.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey:     "src:activity-test",
		OriginalURL:   "https://github.com/activity/test",
		CanonicalURL:  "https://github.com/activity/test",
		NormalizedURL: "https://github.com/activity/test",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/activity-test.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/activity/test",
		FinalURL:     "https://github.com/activity/test",
		Title:        "activity/test",
		Content:      "README",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "2022-11-28",
	}, "activity-source-hash"); err != nil {
		t.Fatalf("save extract: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), link.SourceID, model.SummaryResult{
		Text:          "summary",
		RawJSON:       `{"summary":"summary"}`,
		Model:         "cli/test/model",
		PromptVersion: "dbrain-v1",
		Status:        "ok",
		FetchedAt:     now,
		Tool:          "summarize",
		ToolVersion:   "test-1.0.0",
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "activity",
		"--window", "15m",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{
		"Latest item write:",
		"Latest source write:",
		"Latest source summary:",
		"Items updated in window: 1",
		"Sources updated in window: 1",
		"Sources summarized in window: 1",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected activity output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsBacklogCommandOutputsPendingQueues(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:backlog",
		SourceType:   "x_bookmark",
		ExternalID:   "backlog",
		CanonicalURL: "https://x.com/example/status/backlog",
		Title:        "backlog",
		ContentHash:  "backlog-hash",
		LinksJSON:    `["https://example.com/post"]`,
		NotePath:     "items/x/backlog.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), item.ItemID, model.XHydration{
		Status: "error",
		Error:  "boom",
	}); err != nil {
		t.Fatalf("save hydration error: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "backlog",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	output := stdout.String()
	for _, value := range []string{
		"Queue drained: no",
		"X hydration pending: 1",
		"Link discovery pending: 1",
	} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected backlog output to contain %q, got %q", value, output)
		}
	}
}

func TestStatsPipelineCommandJSON(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	now := time.Now().UTC()
	hydratedItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:pipeline-hydrated",
		SourceType:   "x_bookmark",
		ExternalID:   "pipeline-hydrated",
		CanonicalURL: "https://x.com/example/status/pipeline-hydrated",
		Title:        "hydrated",
		ContentHash:  "pipeline-hydrated-hash",
		NotePath:     "items/x/pipeline-hydrated.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert hydrated x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), hydratedItem.ItemID, model.XHydration{
		Status:    "ok_graphql",
		FullText:  "hydrated text",
		FetchedAt: now,
	}); err != nil {
		t.Fatalf("save hydrated x item: %v", err)
	}

	videoPendingItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:pipeline-video-pending",
		SourceType:   "x_bookmark",
		ExternalID:   "pipeline-video-pending",
		CanonicalURL: "https://x.com/example/status/pipeline-video-pending",
		Title:        "video pending",
		ContentHash:  "pipeline-video-pending-hash",
		NotePath:     "items/x/pipeline-video-pending.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert pending x item: %v", err)
	}
	if _, err := st.SaveXHydration(context.Background(), videoPendingItem.ItemID, model.XHydration{
		Status:    "error",
		Error:     "boom",
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"https://cdn.example.com/video.mp4","expanded_url":"https://x.com/example/status/pipeline-video-pending/video/1","width":1280,"height":720}]}}`,
		FetchedAt: now,
	}); err != nil {
		t.Fatalf("save pending x item hydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), videoPendingItem.ItemID)
	if err != nil {
		t.Fatalf("list item media refs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %d", len(refs))
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		LocalPath:    "media/x/video/test.mp4",
		ContentHash:  "video-download-hash",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("save media download: %v", err)
	}

	webItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "manual:pipeline-web",
		SourceType:   "manual_link",
		ExternalID:   "pipeline-web",
		CanonicalURL: "https://example.com/items/pipeline-web",
		Title:        "web source item",
		ContentHash:  "pipeline-web-item-hash",
		NotePath:     "items/manual/pipeline-web.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert web item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), webItem.ItemID, model.SourceCandidate{
		SourceKey:     "src:pipeline-web",
		OriginalURL:   "https://example.com/pipeline-web",
		CanonicalURL:  "https://example.com/pipeline-web",
		NormalizedURL: "https://example.com/pipeline-web",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/pipeline-web.md",
	}); err != nil {
		t.Fatalf("upsert web source: %v", err)
	}

	youtubeItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "youtube:pipeline-current",
		SourceType:   "youtube_watch_later",
		ExternalID:   "pipeline-current",
		CanonicalURL: "https://www.youtube.com/watch?v=pipeline-current",
		Title:        "youtube source item",
		ContentHash:  "pipeline-youtube-item-hash",
		NotePath:     "items/youtube/pipeline-current.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert youtube item: %v", err)
	}
	youtubeLink, err := st.UpsertSourceLink(context.Background(), youtubeItem.ItemID, model.SourceCandidate{
		SourceKey:     "src:pipeline-youtube",
		OriginalURL:   "https://www.youtube.com/watch?v=pipeline-current",
		CanonicalURL:  "https://www.youtube.com/watch?v=pipeline-current",
		NormalizedURL: "https://www.youtube.com/watch?v=pipeline-current",
		SourceType:    "youtube",
		Domain:        "youtube.com",
		NotePath:      "sources/youtube/pipeline-current.md",
	})
	if err != nil {
		t.Fatalf("upsert youtube source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), youtubeLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://www.youtube.com/watch?v=pipeline-current",
		FinalURL:     "https://www.youtube.com/watch?v=pipeline-current",
		Title:        "pipeline current video",
		Content:      "youtube transcript",
		Status:       "ok",
		Tool:         "youtube-test",
		ToolVersion:  "1.0.0",
		FetchedAt:    now,
	}, "pipeline-youtube-source-hash"); err != nil {
		t.Fatalf("save youtube extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), youtubeLink.SourceID, model.SummaryResult{
		Text:          "youtube summary",
		Model:         "ollama/qwen3.6:35b",
		PromptVersion: sourceenrich.SummaryPromptVersion,
		Status:        "ok",
		Tool:          "ollama-direct",
		ToolVersion:   "ollama-direct-v1",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save youtube summary: %v", err)
	}

	xArticleItem, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:pipeline-article",
		SourceType:   "x_bookmark",
		ExternalID:   "pipeline-article",
		CanonicalURL: "https://x.com/example/status/pipeline-article",
		Title:        "x article source item",
		ContentHash:  "pipeline-x-article-item-hash",
		NotePath:     "items/x/pipeline-article.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert x article item: %v", err)
	}
	xArticleLink, err := st.UpsertSourceLink(context.Background(), xArticleItem.ItemID, model.SourceCandidate{
		SourceKey:     "src:pipeline-x-article",
		OriginalURL:   "https://x.com/example/article/pipeline",
		CanonicalURL:  "https://x.com/example/article/pipeline",
		NormalizedURL: "https://x.com/example/article/pipeline",
		SourceType:    "x_article",
		Domain:        "x.com",
		NotePath:      "sources/x_article/pipeline.md",
	})
	if err != nil {
		t.Fatalf("upsert x article source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), xArticleLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://x.com/example/article/pipeline",
		FinalURL:     "https://x.com/example/article/pipeline",
		Title:        "pipeline x article",
		Content:      "x article content",
		Status:       "ok",
		Tool:         "x-hydration",
		ToolVersion:  "x-hydration-test",
		FetchedAt:    now,
	}, "pipeline-x-article-source-hash"); err != nil {
		t.Fatalf("save x article extraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(context.Background(), xArticleLink.SourceID, model.SummaryResult{
		Text:          "x article summary",
		Model:         "openrouter/qwen/qwen3.5-27b",
		PromptVersion: sourceenrich.SummaryPromptVersion,
		Status:        "ok",
		Tool:          "openrouter-direct",
		ToolVersion:   "openrouter-direct-v1",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("save x article summary: %v", err)
	}

	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "safari-tab:pipeline-current",
		SourceType:   "safari_tab",
		ExternalID:   "pipeline-current",
		CanonicalURL: "https://example.com/safari-tab",
		Title:        "safari tab item",
		Text:         "Safari tab captured from iCloud Tabs.",
		ContentHash:  "pipeline-safari-tab-hash",
		NotePath:     "items/safari-tabs/2026/pipeline-current.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert safari tab item: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "pipeline",
		"--model", "ollama/qwen3.6:35b",
		"--json",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v (stderr=%q)", err, stderr.String())
	}

	var stats store.PipelineStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal pipeline stats: %v\n%s", err, stdout.String())
	}

	assertPipelineRowCounts(t, stats.Hydration, "x_bookmark", 3, 1, 2, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "web", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "x_article", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Extraction, "safari_tab", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "web", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 0, 1, 0, 0)
	assertPipelineRowCounts(t, stats.Transcription, "x_media_transcript", 1, 0, 1, 0, 0)

	cmd = NewRootCommand()
	stdout.Reset()
	stderr.Reset()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--root", root,
		"stats", "pipeline",
		"--json",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext default pipeline: %v (stderr=%q)", err, stderr.String())
	}

	stats = store.PipelineStats{}
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal default pipeline stats: %v\n%s", err, stdout.String())
	}

	assertPipelineRowCounts(t, stats.Summary, "youtube", 1, 1, 0, 0, 0)
	assertPipelineRowCounts(t, stats.Summary, "x_article", 1, 1, 0, 0, 0)
}

func assertPipelineRowCounts(t *testing.T, rows []store.PipelineStageRow, kind string, total int, current int, pending int, blocked int, failed int) {
	t.Helper()

	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		if row.Total != total || row.Current != current || row.Pending != pending || row.Blocked != blocked || row.Failed != failed {
			t.Fatalf("unexpected pipeline row for %s: %+v", kind, row)
		}
		return
	}
	t.Fatalf("missing pipeline row for %s in %+v", kind, rows)
}

func TestCLIFlagsDefaultToCodex(t *testing.T) {
	cmd := NewRootCommand()

	for _, path := range [][]string{
		{"extract", "links"},
		{"extract", "sources"},
		{"import", "github", "stars"},
		{"import", "youtube"},
		{"link", "add"},
		{"sync", "all"},
		{"worker", "sources"},
	} {
		target, _, err := cmd.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		flag := target.Flags().Lookup("cli")
		if flag == nil {
			t.Fatalf("expected --cli flag on %v", path)
		}
		if flag.DefValue != defaultCLIProvider {
			t.Fatalf("expected default cli %q for %v, got %q", defaultCLIProvider, path, flag.DefValue)
		}
	}
}
