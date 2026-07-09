package install

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
)

const testConfigTemplate = `github:
  token: ""
auth:
  enabled: false
  providers: ["github"]
  base_url: "http://127.0.0.1:8742"
  session_key: ""
  github:
    client_id: ""
    client_secret: ""
summary:
  model: ""
categorize:
  model: ""
ocr:
  model: "openrouter/google/gemini-3.1-flash-lite-preview"
apple_notes:
  enabled: false
  attachment_ocr: true
  tesseract_binary: "tesseract"
safari_tabs:
  enabled: false
scheduler:
  sync_all:
    enabled: false
    interval: "1h"
    run_on_start: false
    apple_notes: false
    safari_tabs: false
    skip_apple_notes: false
    skip_safari_tabs: false
openrouter:
  api_key: ""
tsnet:
  web: true
  mcp: true
  hostname: "dbrain"
  auth_key_ref: ""
`

func TestRunCreatesExplicitBaseLayoutAndConfigFromSelections(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	secrets := &recordingSecretStore{values: map[string]string{}}

	result, err := Run(context.Background(), Options{
		Config:             cfg,
		ConfigTemplate:     []byte(testConfigTemplate),
		CategoriesTemplate: []byte("topics: []\n"),
		Runtime:            Runtime{GOOS: "darwin"},
		Force:              true,
		Selections: Selections{
			EnableAppleNotes:  true,
			EnableSafariTabs:  true,
			EnableScheduler:   true,
			EnableTailscale:   true,
			TSNetHostname:     "dbrain-alice",
			EnableGitHubLogin: true,
			AuthBaseURL:       "https://dbrain-alice.example.ts.net",
			GitHubClientID:    "Iv1.example",
			UseKeychain:       true,
			SummaryModel:      "lmstudio/qwen/qwen3.6-35b-a3b",
			CategorizeModel:   "lmstudio/qwen/qwen3.6-35b-a3b",
			OCRModel:          "tesseract",
			Secrets: map[SecretKind]string{
				SecretGitHubToken:        "ghp_test_secret",
				SecretOpenRouterAPIKey:   "sk-or-test-secret",
				SecretGitHubClientSecret: "github-oauth-secret",
				SecretTSNetAuthKey:       "tskey-auth-secret",
			},
		},
		Tools: []Tool{
			{ID: ToolTesseract, Name: "Tesseract", Available: true, Path: "/opt/homebrew/bin/tesseract"},
			{ID: ToolLMStudioAPI, Name: "LM Studio API", Available: true, Models: []string{"qwen/qwen3.6-35b-a3b"}},
		},
		SecretStore: secrets,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{
		cfg.ConfigDir,
		cfg.DataDir,
		cfg.TempDir,
		cfg.CacheDir,
		cfg.LogDir,
		cfg.VaultDir,
		cfg.MediaDir,
		cfg.OKFDir,
		filepath.Join(cfg.VaultDir, "items"),
	} {
		if info, err := result.FS.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s to exist, info=%v err=%v", path, info, err)
		}
	}

	configText := string(mustReadFile(t, result.FS, cfg.ConfigPath))
	for _, expected := range []string{
		`token: "keychain://dbrain/github-token"`,
		`model: "lmstudio/qwen/qwen3.6-35b-a3b"`,
		`model: "tesseract"`,
		`enabled: true`,
		`tesseract_binary: "/opt/homebrew/bin/tesseract"`,
		`api_key: "keychain://dbrain/openrouter-api-key"`,
		`apple_notes: true`,
		`safari_tabs: true`,
		`base_url: "https://dbrain-alice.example.ts.net"`,
		`client_id: "Iv1.example"`,
		`client_secret: "keychain://dbrain/github-client-secret"`,
		`session_key: "keychain://dbrain/auth-session-key"`,
		`hostname: "dbrain-alice"`,
		`auth_key_ref: "keychain://dbrain/tsnet-auth-key"`,
	} {
		if !strings.Contains(configText, expected) {
			t.Fatalf("expected generated config to contain %q:\n%s", expected, configText)
		}
	}
	for _, forbidden := range []string{"ghp_test_secret", "sk-or-test-secret", "github-oauth-secret", "tskey-auth-secret"} {
		if strings.Contains(configText, forbidden) {
			t.Fatalf("raw secret leaked into config: %q\n%s", forbidden, configText)
		}
	}

	wantSecrets := map[string]string{
		"dbrain/github-token":         "ghp_test_secret",
		"dbrain/openrouter-api-key":   "sk-or-test-secret",
		"dbrain/github-client-secret": "github-oauth-secret",
		"dbrain/tsnet-auth-key":       "tskey-auth-secret",
	}
	for key, want := range wantSecrets {
		if secrets.values[key] != want {
			t.Fatalf("stored secret %s = %q, want %q in %#v", key, secrets.values[key], want, secrets.values)
		}
	}
	if got := secrets.values["dbrain/auth-session-key"]; len(got) < 32 {
		t.Fatalf("generated auth session key was not stored, got %q in %#v", got, secrets.values)
	}
	if string(mustReadFile(t, result.FS, cfg.CategoriesPath)) != "topics: []\n" {
		t.Fatalf("categories template was not written")
	}
	if result.ConfigPath != cfg.ConfigPath {
		t.Fatalf("ConfigPath = %q, want %q", result.ConfigPath, cfg.ConfigPath)
	}
}

func TestRunDoesNotOverwriteExistingConfigWithoutForce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fs := OSFS{}
	if err := fs.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fs.WriteFile(cfg.ConfigPath, []byte("github:\n  token: keep-me\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Config:             cfg,
		ConfigTemplate:     []byte(testConfigTemplate),
		CategoriesTemplate: []byte("topics: []\n"),
		FS:                 fs,
		Selections:         Selections{EnableAppleNotes: true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	configText := string(mustReadFile(t, result.FS, cfg.ConfigPath))
	if configText != "github:\n  token: keep-me\n" {
		t.Fatalf("config overwritten without force:\n%s", configText)
	}
	if !containsChange(result.Changes, ChangeSkipped, cfg.ConfigPath) {
		t.Fatalf("expected skipped change for existing config, got %#v", result.Changes)
	}
}

func TestRunForceTightensExistingConfigPermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not portable on Windows")
	}

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fs := OSFS{}
	if err := fs.WriteFile(cfg.ConfigPath, []byte("github:\n  token: keep-me\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := fs.Stat(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("stat precondition config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("precondition mode = %o, want 0644", got)
	}

	result, err := Run(context.Background(), Options{
		Config:             cfg,
		ConfigTemplate:     []byte(testConfigTemplate),
		CategoriesTemplate: []byte("topics: []\n"),
		FS:                 fs,
		Force:              true,
		Selections:         Selections{EnableAppleNotes: true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err = result.FS.Stat(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("stat generated config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode after force overwrite = %o, want 0600", got)
	}
}

func TestRunDryRunForceReportsExistingFilesAsUpdated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fs := OSFS{}
	if err := fs.WriteFile(cfg.ConfigPath, []byte("github:\n  token: keep-me\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Config:             cfg,
		ConfigTemplate:     []byte(testConfigTemplate),
		CategoriesTemplate: []byte("topics: []\n"),
		FS:                 fs,
		Force:              true,
		DryRun:             true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsChange(result.Changes, ChangeUpdated, cfg.ConfigPath) {
		t.Fatalf("expected dry-run force to report config update, got %#v", result.Changes)
	}
	if !containsChange(result.Changes, ChangeCreated, cfg.CategoriesPath) {
		t.Fatalf("expected dry-run force to report categories creation, got %#v", result.Changes)
	}
}

func TestRunDryRunDoesNotCreateLayoutOrFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	secrets := &recordingSecretStore{values: map[string]string{}}

	result, err := Run(context.Background(), Options{
		Config:             cfg,
		ConfigTemplate:     []byte(testConfigTemplate),
		CategoriesTemplate: []byte("topics: []\n"),
		Runtime:            Runtime{GOOS: "darwin"},
		DryRun:             true,
		SecretStore:        secrets,
		Selections: Selections{
			EnableAppleNotes: true,
			UseKeychain:      true,
			Secrets: map[SecretKind]string{
				SecretGitHubToken: "ghp_dry_run_secret",
			},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{
		cfg.ConfigDir,
		cfg.DataDir,
		cfg.TempDir,
		cfg.CacheDir,
		cfg.LogDir,
		cfg.VaultDir,
		cfg.MediaDir,
		cfg.OKFDir,
		cfg.ConfigPath,
		cfg.CategoriesPath,
		filepath.Join(cfg.VaultDir, "items"),
	} {
		if path == root {
			continue
		}
		if _, err := result.FS.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("dry run unexpectedly created %s, err=%v", path, err)
		}
	}
	if !containsChange(result.Changes, ChangeCreated, cfg.ConfigPath) {
		t.Fatalf("expected dry-run created change for config, got %#v", result.Changes)
	}
	if !containsChange(result.Changes, ChangeCreated, cfg.CategoriesPath) {
		t.Fatalf("expected dry-run created change for categories, got %#v", result.Changes)
	}
	if len(secrets.values) != 0 {
		t.Fatalf("dry run unexpectedly wrote secrets: %#v", secrets.values)
	}
}

func TestRunWarnsWhenAuthSessionKeyIsWrittenWithoutKeychain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Config:         cfg,
		ConfigTemplate: []byte(testConfigTemplate),
		Runtime:        Runtime{GOOS: "linux"},
		Selections: Selections{
			EnableGitHubLogin: true,
			AuthBaseURL:       "https://dbrain.example.test",
			GitHubClientID:    "Iv1.example",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	configText := string(mustReadFile(t, result.FS, cfg.ConfigPath))
	sessionKeyLine := ""
	for _, line := range strings.Split(configText, "\n") {
		if strings.Contains(line, "session_key:") {
			sessionKeyLine = line
			break
		}
	}
	if sessionKeyLine == "" || strings.Contains(sessionKeyLine, `session_key: ""`) || strings.Contains(sessionKeyLine, "keychain://") {
		t.Fatalf("expected plaintext session_key in config, got %q in:\n%s", sessionKeyLine, configText)
	}
	if !containsWarning(result.Warnings, "auth.session_key was written directly") {
		t.Fatalf("expected plaintext session key warning, got %#v", result.Warnings)
	}
}

func TestRunWarnsWhenGitHubLoginUsesLocalhostBaseURL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	result, err := Run(context.Background(), Options{
		Config:         cfg,
		ConfigTemplate: []byte(testConfigTemplate),
		Runtime:        Runtime{GOOS: "linux"},
		Selections: Selections{
			EnableGitHubLogin: true,
			AuthBaseURL:       "http://127.0.0.1:8742",
			GitHubClientID:    "Iv1.example",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsWarning(result.Warnings, "auth.base_url is a localhost HTTP URL") {
		t.Fatalf("expected localhost auth.base_url warning, got %#v", result.Warnings)
	}
}

func TestDetectToolsFindsCommandsAppsAndLocalEndpoints(t *testing.T) {
	t.Parallel()

	probe := fakeProbe{
		paths: map[string]string{
			"mw":        "/opt/homebrew/bin/mw",
			"ollama":    "/opt/homebrew/bin/ollama",
			"lms":       "/Users/alice/.lmstudio/bin/lms",
			"omlx":      "/opt/homebrew/bin/omlx",
			"tesseract": "/opt/homebrew/bin/tesseract",
			"ffprobe":   "/opt/homebrew/bin/ffprobe",
			"yt-dlp":    "/opt/homebrew/bin/yt-dlp",
		},
		exists: map[string]bool{
			"/Applications/MacWhisper.app": true,
		},
		responses: map[string]string{
			"http://127.0.0.1:11434/api/tags": `{"models":[{"name":"dbrain:latest"}]}`,
			"http://127.0.0.1:1234/v1/models": `{"data":[{"id":"qwen/qwen3.6-35b-a3b"}]}`,
			"http://127.0.0.1:8000/v1/models": `{"data":[{"id":"Qwen3.6-35B-A3B-MLX-4bit"}]}`,
		},
	}

	tools := DetectTools(context.Background(), DetectionOptions{
		GOOS:    "darwin",
		HomeDir: "/Users/alice",
		Prober:  probe,
		Timeout: 50 * time.Millisecond,
	})

	assertTool(t, tools, ToolMacWhisper, true, "/opt/homebrew/bin/mw", nil)
	assertTool(t, tools, ToolMacWhisperApp, true, "/Applications/MacWhisper.app", nil)
	assertTool(t, tools, ToolOllamaAPI, true, "", []string{"dbrain:latest"})
	assertTool(t, tools, ToolLMStudioAPI, true, "", []string{"qwen/qwen3.6-35b-a3b"})
	assertTool(t, tools, ToolOMLXAPI, true, "", []string{"Qwen3.6-35B-A3B-MLX-4bit"})
	assertTool(t, tools, ToolTesseract, true, "/opt/homebrew/bin/tesseract", nil)
	assertTool(t, tools, ToolFFprobe, true, "/opt/homebrew/bin/ffprobe", nil)
	assertTool(t, tools, ToolYTDLP, true, "/opt/homebrew/bin/yt-dlp", nil)
}

func TestDetectToolsUsesLMStudioHomeFallbackWithoutDuplicate(t *testing.T) {
	t.Parallel()

	probe := fakeProbe{
		exists: map[string]bool{
			"/Users/alice/.lmstudio/bin/lms": true,
		},
	}

	tools := DetectTools(context.Background(), DetectionOptions{
		GOOS:    "darwin",
		HomeDir: "/Users/alice",
		Prober:  probe,
		Timeout: 50 * time.Millisecond,
	})

	assertTool(t, tools, ToolLMStudio, true, "/Users/alice/.lmstudio/bin/lms", nil)
	count := 0
	for _, tool := range tools {
		if tool.ID == ToolLMStudio {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one LM Studio CLI row, got %d in %#v", count, tools)
	}
}

type recordingSecretStore struct {
	values map[string]string
}

func (s *recordingSecretStore) PutSecret(_ context.Context, service string, account string, value string) error {
	s.values[service+"/"+account] = value
	return nil
}

type fakeProbe struct {
	paths     map[string]string
	exists    map[string]bool
	responses map[string]string
}

func (p fakeProbe) LookPath(name string) (string, error) {
	if path := p.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}

func (p fakeProbe) PathExists(path string) bool {
	return p.exists[path]
}

func (p fakeProbe) Get(_ context.Context, url string) ([]byte, error) {
	if response, ok := p.responses[url]; ok {
		return []byte(response), nil
	}
	return nil, errors.New("connection refused")
}

func mustReadFile(t *testing.T, fsys FileSystem, path string) []byte {
	t.Helper()
	data, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return data
}

func containsChange(changes []Change, kind ChangeKind, path string) bool {
	for _, change := range changes {
		if change.Kind == kind && change.Path == path {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}

func assertTool(t *testing.T, tools []Tool, id ToolID, available bool, path string, models []string) {
	t.Helper()
	for _, tool := range tools {
		if tool.ID != id {
			continue
		}
		if tool.Available != available {
			t.Fatalf("%s available = %t, want %t", id, tool.Available, available)
		}
		if path != "" && tool.Path != path {
			t.Fatalf("%s path = %q, want %q", id, tool.Path, path)
		}
		if models != nil && !reflect.DeepEqual(tool.Models, models) {
			t.Fatalf("%s models = %#v, want %#v", id, tool.Models, models)
		}
		return
	}
	t.Fatalf("tool %s not found in %#v", id, tools)
}

var _ fs.FileInfo
