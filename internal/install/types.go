package install

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/zalando/go-keyring"
)

type ToolID string

const (
	ToolMacWhisper    ToolID = "macwhisper_cli"
	ToolWhisperCPP    ToolID = "whisper_cpp_cli"
	ToolMacWhisperApp ToolID = "macwhisper_app"
	ToolOllama        ToolID = "ollama_cli"
	ToolOllamaAPI     ToolID = "ollama_api"
	ToolLMStudio      ToolID = "lmstudio_cli"
	ToolLMStudioAPI   ToolID = "lmstudio_api"
	ToolOMLX          ToolID = "omlx_cli"
	ToolOMLXAPI       ToolID = "omlx_api"
	ToolTesseract     ToolID = "tesseract"
	ToolFFprobe       ToolID = "ffprobe"
	ToolYTDLP         ToolID = "yt_dlp"
	ToolSummarize     ToolID = "summarize"
	ToolOnePassword   ToolID = "onepassword"
	ToolSecurity      ToolID = "security"
)

type Tool struct {
	ID        ToolID
	Name      string
	Available bool
	Path      string
	Endpoint  string
	Models    []string
	Detail    string
	Error     string
}

type Runtime struct {
	GOOS    string
	HomeDir string
}

type SecretKind string

const (
	SecretGitHubToken        SecretKind = "github_token"
	SecretOpenRouterAPIKey   SecretKind = "openrouter_api_key"
	SecretTSNetAuthKey       SecretKind = "tsnet_auth_key"
	SecretAuthSessionKey     SecretKind = "auth_session_key"
	SecretGitHubClientSecret SecretKind = "github_client_secret"
)

type Selections struct {
	ImportXBookmarks        bool
	ImportGitHubStars       bool
	ImportYouTubeWatchLater bool
	ImportYouTubeLiked      bool
	ImportFeeds             bool
	ImportBlueskyBookmarks  bool
	EnableAppleNotes        bool
	EnableSafariTabs        bool
	SafariTabsDevice        string
	SyncBrowser             string
	SyncProfile             string
	EnableScheduler         bool
	EnableTailscale         bool
	TSNetHostname           string
	EnableGitHubLogin       bool
	AuthBaseURL             string
	GitHubClientID          string
	UseKeychain             bool
	SummaryModel            string
	CategorizeModel         string
	OCRModel                string
	SkipXPhotoOCR           bool
	SkipCategorize          bool
	TranscriptionBackend    string
	TranscriptionLanguage   string
	WhisperModelPath        string
	WhisperVADModelPath     string
	GitHubTokenConfigured   bool
	Secrets                 map[SecretKind]string
}

type Options struct {
	Config                config.Config
	ConfigTemplate        []byte
	CategoriesTemplate    []byte
	Runtime               Runtime
	Selections            Selections
	OllamaModels          []OllamaModelSetup
	Tools                 []Tool
	FS                    FileSystem
	CommandRunner         CommandRunner
	SecretStore           SecretStore
	Force                 bool
	DryRun                bool
	DownloadWhisperModels bool
	DownloadFile          func(context.Context, string, string, string) error
	DownloadProgress      func(DownloadProgress)
	CommandOutput         io.Writer
}

type DownloadProgress struct {
	Kind    DownloadProgressKind
	Path    string
	Current int64
	Total   int64
}

type DownloadProgressKind string

const (
	DownloadProgressStart  DownloadProgressKind = "start"
	DownloadProgressUpdate DownloadProgressKind = "update"
	DownloadProgressDone   DownloadProgressKind = "done"
)

type OllamaModelSetup struct {
	Model         string
	PullModel     string
	Modelfile     []byte
	ModelfileName string
}

type Result struct {
	ConfigPath     string
	CategoriesPath string
	Tools          []Tool
	Changes        []Change
	Warnings       []string
	FS             FileSystem
}

type ChangeKind string

const (
	ChangeCreated  ChangeKind = "created"
	ChangeUpdated  ChangeKind = "updated"
	ChangeSkipped  ChangeKind = "skipped"
	ChangePrepared ChangeKind = "prepared"
)

type Change struct {
	Kind    ChangeKind
	Path    string
	Message string
}

type FileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(path string, data []byte, perm os.FileMode) error
	ReadFile(path string) ([]byte, error)
	Stat(path string) (os.FileInfo, error)
}

type CommandRunner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

type StreamingCommandRunner interface {
	Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error
}

type OSFS struct{}

func (OSFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}
func (OSFS) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (OSFS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

type SecretStore interface {
	PutSecret(ctx context.Context, service string, account string, value string) error
	SecretExists(ctx context.Context, service string, account string) (bool, error)
}

type KeychainSecretStore struct{}

func (KeychainSecretStore) PutSecret(_ context.Context, service string, account string, value string) error {
	return keyring.Set(service, account, value)
}

func (KeychainSecretStore) SecretExists(_ context.Context, service string, account string) (bool, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(value) != "", nil
}

type secretSpec struct {
	Kind    SecretKind
	Service string
	Account string
	Path    []string
}

var secretSpecs = []secretSpec{
	{Kind: SecretGitHubToken, Service: "dbrain", Account: "github-token", Path: []string{"github", "token"}},
	{Kind: SecretOpenRouterAPIKey, Service: "dbrain", Account: "openrouter-api-key", Path: []string{"openrouter", "api_key"}},
	{Kind: SecretTSNetAuthKey, Service: "dbrain", Account: "tsnet-auth-key", Path: []string{"tsnet", "auth_key_ref"}},
	{Kind: SecretAuthSessionKey, Service: "dbrain", Account: "auth-session-key", Path: []string{"auth", "session_key"}},
	{Kind: SecretGitHubClientSecret, Service: "dbrain", Account: "github-client-secret", Path: []string{"auth", "github", "client_secret"}},
}
