package remote

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const (
	DefaultHostname       = "dbrain"
	DefaultMCPPath        = "/mcp"
	DefaultStartupTimeout = 45 * time.Second
)

type Options struct {
	Web                bool
	MCP                bool
	MCPPath            string
	Hostname           string
	StateDir           string
	Listen             string
	TLS                bool
	StartupTimeout     time.Duration
	AuthKey            string
	AuthKeyRef         string
	AuthKeyCommand     []string
	AllowSecretCommand bool
	AdvertiseTags      []string
	ControlURL         string
	Verbose            bool
}

func OptionsFromRuntime(cfg config.Config) (Options, error) {
	hostname := firstRuntimeString(cfg.RootDir, DefaultHostname, "DBRAIN_TSNET_HOSTNAME")
	tls := runtimeenv.FirstBoolDefault(cfg.RootDir, true, "DBRAIN_TSNET_TLS")

	stateDir := firstRuntimeString(cfg.RootDir, filepath.Join(cfg.DataDir, "tsnet", hostname), "DBRAIN_TSNET_STATE_DIR")
	listen := firstRuntimeString(cfg.RootDir, defaultListen(tls), "DBRAIN_TSNET_LISTEN")
	startupTimeout := DefaultStartupTimeout
	if raw, ok := runtimeenv.Lookup(cfg.RootDir, "DBRAIN_TSNET_STARTUP_TIMEOUT"); ok {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return Options{}, fmt.Errorf("parse DBRAIN_TSNET_STARTUP_TIMEOUT: %w", err)
		}
		startupTimeout = parsed
	}

	opts := Options{
		Web:                runtimeenv.FirstBoolDefault(cfg.RootDir, true, "DBRAIN_TSNET_WEB"),
		MCP:                runtimeenv.FirstBoolDefault(cfg.RootDir, true, "DBRAIN_TSNET_MCP"),
		MCPPath:            firstRuntimeString(cfg.RootDir, DefaultMCPPath, "DBRAIN_TSNET_MCP_PATH"),
		Hostname:           hostname,
		StateDir:           stateDir,
		Listen:             listen,
		TLS:                tls,
		StartupTimeout:     startupTimeout,
		AuthKey:            runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TSNET_AUTH_KEY"),
		AuthKeyRef:         runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TSNET_AUTH_KEY_REF"),
		AuthKeyCommand:     runtimeenv.ConfigList(cfg.RootDir, "TSNET_AUTH_KEY_COMMAND"),
		AllowSecretCommand: runtimeenv.FirstBoolDefault(cfg.RootDir, false, "DBRAIN_TSNET_ALLOW_SECRET_COMMAND"),
		AdvertiseTags:      runtimeenv.FirstList(cfg.RootDir, "DBRAIN_TSNET_ADVERTISE_TAGS"),
		ControlURL:         runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_TSNET_CONTROL_URL"),
		Verbose:            runtimeenv.FirstBoolDefault(cfg.RootDir, false, "DBRAIN_TSNET_VERBOSE"),
	}
	if opts.MCPPath == "" {
		opts.MCPPath = DefaultMCPPath
	}
	if opts.StartupTimeout <= 0 {
		return Options{}, fmt.Errorf("tsnet startup timeout must be positive")
	}
	cleaned, err := ValidateMCPPath(opts.MCPPath)
	if err != nil {
		return Options{}, err
	}
	opts.MCPPath = cleaned
	return opts, nil
}

func (o *Options) Validate() error {
	if strings.TrimSpace(o.Hostname) == "" {
		return fmt.Errorf("tsnet hostname is required")
	}
	if strings.TrimSpace(o.StateDir) == "" {
		return fmt.Errorf("tsnet state dir is required")
	}
	if !o.Web && !o.MCP {
		return fmt.Errorf("serve remote requires at least one surface: --web or --mcp")
	}
	if strings.TrimSpace(o.Listen) == "" {
		return fmt.Errorf("tsnet listen address is required")
	}
	cleaned, err := ValidateMCPPath(o.MCPPath)
	if err != nil {
		return err
	}
	o.MCPPath = cleaned
	return nil
}

func ValidateMCPPath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = DefaultMCPPath
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("mcp path must start with /")
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", fmt.Errorf("mcp path must not contain whitespace")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return "", fmt.Errorf("mcp path must not be /")
	}
	if cleaned != trimmed && strings.TrimRight(trimmed, "/") != cleaned {
		return "", fmt.Errorf("mcp path must be clean")
	}
	if strings.Contains(cleaned, "/../") || strings.HasSuffix(cleaned, "/..") || strings.Contains(cleaned, "../") {
		return "", fmt.Errorf("mcp path must not contain parent path segments")
	}
	return cleaned, nil
}

func firstRuntimeString(rootDir string, fallback string, keys ...string) string {
	if value := runtimeenv.FirstNonEmpty(rootDir, keys...); value != "" {
		return value
	}
	return fallback
}

func defaultListen(tls bool) string {
	if tls {
		return ":443"
	}
	return ":80"
}
