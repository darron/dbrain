package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/darron/dbrain/internal/config"
	installer "github.com/darron/dbrain/internal/install"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type installFlags struct {
	basePath          string
	yes               bool
	force             bool
	dryRun            bool
	noDetect          bool
	noKeychain        bool
	enableAppleNotes  bool
	enableSafariTabs  bool
	enableScheduler   bool
	enableTailscale   bool
	tsnetHostname     string
	enableGitHubLogin bool
	authBaseURL       string
	githubClientID    string
	localModelProfile string
	summaryModel      string
	categorizeModel   string
	ocrModel          string
	installLaunchd    bool
	startLaunchd      bool
	launchdLabel      string
	launchdBinPath    string
}

const (
	installModelProfileNone         = "none"
	installModelProfileDbrain       = "dbrain"
	installModelProfileDbrainOllama = "dbrain-ollama"
	installModelProfileDbrainOMLX   = "dbrain-omlx"
	installModelProfileSmallOllama  = "small-ollama"

	installDbrainOllamaModel    = "ollama/dbrain:2026042701"
	installDbrainOllamaAPIModel = "dbrain:2026042701"
	installDbrainOMLXModel      = "omlx/Qwen3.6-35B-A3B-MLX-4bit"
	installDbrainOMLXAPIModel   = "Qwen3.6-35B-A3B-MLX-4bit"
	installSmallOllamaModel     = "ollama/gemma4:12b-mlx"
	installSmallOllamaAPIModel  = "gemma4:12b-mlx"
)

func newInstallCommand(root *rootOptions) *cobra.Command {
	flags := installFlags{launchdLabel: defaultLaunchdLabel}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Run first-time local dbrain setup",
		Long:  "Run first-time local dbrain setup. The installer creates the resolved config/data/vault/log layout, detects local helper tools, writes a config file from the bundled sample, optionally stores supplied secrets in macOS Keychain, and can prepare scheduler config.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateInstallModelProfile(flags.localModelProfile); err != nil {
				return err
			}
			cfg, selectorArgs, err := resolveInstallConfig(root, flags.basePath)
			if err != nil {
				return err
			}

			tools := []installer.Tool(nil)
			if !flags.noDetect {
				home, _ := os.UserHomeDir()
				tools = installer.DetectTools(cmd.Context(), installer.DetectionOptions{
					GOOS:    runtime.GOOS,
					HomeDir: home,
				})
			}

			selections := defaultInstallSelections(flags, tools)
			if shouldPromptInstall(cmd, flags.yes) {
				applyDetectedPromptDefaults(&selections, tools)
				if err := promptInstallSelections(cmd, &selections, tools); err != nil {
					return err
				}
			}

			result, err := installer.Run(cmd.Context(), installer.Options{
				Config:     cfg,
				Runtime:    installer.Runtime{GOOS: runtime.GOOS},
				Selections: selections,
				Tools:      tools,
				Force:      flags.force,
				DryRun:     flags.dryRun,
			})
			if err != nil {
				return err
			}
			launchdRequested := flags.installLaunchd || flags.startLaunchd
			if launchdRequested && !selections.EnableScheduler {
				result.Warnings = append(result.Warnings, "Launchd installation was skipped because scheduler.sync_all is not enabled.")
			} else if flags.dryRun && launchdRequested {
				result.Warnings = append(result.Warnings, "Dry run skipped launchd plist installation.")
			} else if launchdRequested {
				if err := installLaunchdFromInstall(cmd.Context(), cfg, selectorArgs, flags); err != nil {
					return err
				}
			}
			printInstallResult(cmd.OutOrStdout(), result, selections, runtime.GOOS)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.basePath, "base-path", "", "Single dbrain base path to initialize; equivalent to using --root for install")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "Run non-interactively with flags and detected defaults")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite existing generated config/categories files")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "Show planned setup without writing files")
	cmd.Flags().BoolVar(&flags.noDetect, "no-detect", false, "Skip local helper and model endpoint detection")
	cmd.Flags().BoolVar(&flags.noKeychain, "no-keychain", false, "Do not store prompted secrets in macOS Keychain")
	cmd.Flags().BoolVar(&flags.enableAppleNotes, "enable-apple-notes", false, "Enable Apple Notes imports in config and scheduled sync")
	cmd.Flags().BoolVar(&flags.enableSafariTabs, "enable-safari-tabs", false, "Enable Safari tab imports in config and scheduled sync")
	cmd.Flags().BoolVar(&flags.enableScheduler, "enable-scheduler", false, "Enable scheduler.sync_all in config")
	cmd.Flags().BoolVar(&flags.enableTailscale, "enable-tailscale", false, "Configure built-in tsnet/Tailscale transport settings")
	cmd.Flags().StringVar(&flags.tsnetHostname, "tsnet-hostname", "dbrain", "Tailscale machine hostname for built-in tsnet")
	cmd.Flags().BoolVar(&flags.enableGitHubLogin, "enable-github-login", false, "Enable GitHub OAuth login for the web UI")
	cmd.Flags().StringVar(&flags.authBaseURL, "auth-base-url", "http://127.0.0.1:8742", "Public base URL for GitHub OAuth callbacks")
	cmd.Flags().StringVar(&flags.githubClientID, "github-client-id", "", "GitHub OAuth app client ID")
	cmd.Flags().StringVar(&flags.localModelProfile, "local-model-profile", "", "Curated local model profile to write: dbrain, dbrain-ollama, dbrain-omlx, small-ollama, or none")
	cmd.Flags().StringVar(&flags.summaryModel, "summary-model", "", "Default summary model to write, for example ollama/dbrain:2026042701, lmstudio/<id>, or omlx/<id>")
	cmd.Flags().StringVar(&flags.categorizeModel, "categorize-model", "", "Default categorization model to write")
	cmd.Flags().StringVar(&flags.ocrModel, "ocr-model", "", "Default OCR model to write, for example tesseract")
	cmd.Flags().BoolVar(&flags.installLaunchd, "install-launchd", false, "Write the per-user macOS launchd service plist when scheduler is enabled")
	cmd.Flags().BoolVar(&flags.startLaunchd, "start-launchd", false, "Load and start the launchd service after writing it")
	cmd.Flags().StringVar(&flags.launchdLabel, "launchd-label", defaultLaunchdLabel, "launchd label for --install-launchd")
	cmd.Flags().StringVar(&flags.launchdBinPath, "bin", "", "dbrain binary for --install-launchd; defaults to dbrain from PATH")
	return cmd
}

func resolveInstallConfig(root *rootOptions, basePath string) (config.Config, []string, error) {
	basePath = strings.TrimSpace(basePath)
	if basePath != "" {
		if strings.TrimSpace(root.root) != "" || strings.TrimSpace(root.configFile) != "" {
			return config.Config{}, nil, fmt.Errorf("install --base-path cannot be combined with --root or --config-file")
		}
		cfg, err := config.Load(basePath)
		if err != nil {
			return config.Config{}, nil, err
		}
		return cfg, []string{"--root", absOrOriginal(basePath)}, nil
	}
	if value := strings.TrimSpace(root.configFile); value != "" {
		cfg, err := installConfigFileTarget(value)
		if err != nil {
			return config.Config{}, nil, err
		}
		return cfg, []string{"--config-file", absOrOriginal(value)}, nil
	}
	if value := strings.TrimSpace(root.root); value != "" {
		cfg, err := config.Load(value)
		if err != nil {
			return config.Config{}, nil, err
		}
		return cfg, []string{"--root", absOrOriginal(value)}, nil
	}
	if value := strings.TrimSpace(os.Getenv(configFileEnvVar)); value != "" {
		cfg, err := installConfigFileTarget(value)
		if err != nil {
			return config.Config{}, nil, err
		}
		return cfg, []string{"--config-file", absOrOriginal(value)}, nil
	}
	if value := strings.TrimSpace(os.Getenv(rootEnvVar)); value != "" {
		cfg, err := config.Load(value)
		if err != nil {
			return config.Config{}, nil, err
		}
		return cfg, []string{"--root", absOrOriginal(value)}, nil
	}
	cfg, err := config.Load("")
	return cfg, nil, err
}

func installConfigFileTarget(path string) (config.Config, error) {
	if strings.TrimSpace(path) == "" {
		return config.Config{}, fmt.Errorf("config file path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return config.LoadConfigFile(path)
	}
	cfg, err := config.Load("")
	if err != nil {
		return config.Config{}, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve config file: %w", err)
	}
	cfg.RootDir = filepath.Dir(absPath)
	cfg.ConfigDir = filepath.Dir(absPath)
	cfg.ConfigPath = absPath
	cfg.CategoriesPath = filepath.Join(cfg.ConfigDir, "categories.yaml")
	return cfg, nil
}

func defaultInstallSelections(flags installFlags, tools []installer.Tool) installer.Selections {
	summaryModel := strings.TrimSpace(flags.summaryModel)
	if summaryModel == "" {
		summaryModel = installModelForProfile(flags.localModelProfile)
	}
	categorizeModel := strings.TrimSpace(flags.categorizeModel)
	if categorizeModel == "" && summaryModel != "" {
		categorizeModel = summaryModel
	}
	ocrModel := strings.TrimSpace(flags.ocrModel)
	if ocrModel == "" && toolAvailable(tools, installer.ToolTesseract) {
		ocrModel = "tesseract"
	}
	return installer.Selections{
		EnableAppleNotes:  flags.enableAppleNotes,
		EnableSafariTabs:  flags.enableSafariTabs,
		EnableScheduler:   flags.enableScheduler,
		EnableTailscale:   flags.enableTailscale,
		TSNetHostname:     strings.TrimSpace(flags.tsnetHostname),
		EnableGitHubLogin: flags.enableGitHubLogin,
		AuthBaseURL:       strings.TrimSpace(flags.authBaseURL),
		GitHubClientID:    strings.TrimSpace(flags.githubClientID),
		UseKeychain:       runtime.GOOS == "darwin" && !flags.noKeychain,
		SummaryModel:      summaryModel,
		CategorizeModel:   categorizeModel,
		OCRModel:          ocrModel,
		Secrets:           map[installer.SecretKind]string{},
	}
}

func shouldPromptInstall(cmd *cobra.Command, yes bool) bool {
	if yes {
		return false
	}
	file, ok := cmd.InOrStdin().(*os.File)
	return ok && file != nil && isatty.IsTerminal(file.Fd())
}

func promptInstallSelections(cmd *cobra.Command, selections *installer.Selections, tools []installer.Tool) error {
	features := []string{}
	if selections.EnableAppleNotes {
		features = append(features, "apple_notes")
	}
	if selections.EnableSafariTabs {
		features = append(features, "safari_tabs")
	}
	if selections.EnableScheduler {
		features = append(features, "scheduler")
	}
	if selections.EnableTailscale {
		features = append(features, "tailscale")
	}
	if selections.EnableGitHubLogin {
		features = append(features, "github_login")
	}
	githubToken := ""
	openRouterKey := ""
	tsnetKey := ""
	githubClientSecret := ""
	useKeychain := selections.UseKeychain

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("dbrain install").
				Description(installToolSummary(tools)),
			huh.NewMultiSelect[string]().
				Title("Imports and background work").
				Options(
					huh.NewOption("Apple Notes import", "apple_notes").Selected(selections.EnableAppleNotes),
					huh.NewOption("Safari tabs import", "safari_tabs").Selected(selections.EnableSafariTabs),
					huh.NewOption("Scheduled sync all", "scheduler").Selected(selections.EnableScheduler),
					huh.NewOption("Tailscale remote access", "tailscale").Selected(selections.EnableTailscale),
					huh.NewOption("GitHub web login", "github_login").Selected(selections.EnableGitHubLogin),
				).
				Value(&features),
			huh.NewInput().
				Title("Tailscale hostname").
				Placeholder("dbrain").
				Value(&selections.TSNetHostname),
			huh.NewInput().
				Title("GitHub auth base URL").
				Placeholder("https://dbrain.example.ts.net").
				Value(&selections.AuthBaseURL),
			huh.NewInput().
				Title("GitHub OAuth client ID").
				Value(&selections.GitHubClientID),
			huh.NewInput().
				Title("Summary model").
				Description("Default prefers detected dbrain profiles; use small-ollama for lower memory.").
				Placeholder(installDbrainOllamaModel).
				Value(&selections.SummaryModel),
			huh.NewInput().
				Title("Categorization model").
				Placeholder("leave empty to match summary model").
				Value(&selections.CategorizeModel),
			huh.NewInput().
				Title("OCR model").
				Placeholder("for local OCR use tesseract").
				Value(&selections.OCRModel),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Store entered secrets in macOS Keychain?").
				Description("Secret refs are written to config as keychain://dbrain/<account>. Blank secret fields are skipped.").
				Value(&useKeychain),
			huh.NewInput().
				Title("GitHub token").
				Description("Optional. Used for GitHub star imports.").
				EchoMode(huh.EchoModePassword).
				Value(&githubToken),
			huh.NewInput().
				Title("OpenRouter API key").
				Description("Optional. Used for hosted LLM/OCR/categorization.").
				EchoMode(huh.EchoModePassword).
				Value(&openRouterKey),
			huh.NewInput().
				Title("Tailscale auth key").
				Description("Optional. Used for unattended tsnet startup.").
				EchoMode(huh.EchoModePassword).
				Value(&tsnetKey),
			huh.NewInput().
				Title("GitHub OAuth client secret").
				Description("Optional. Used when GitHub web login is enabled.").
				EchoMode(huh.EchoModePassword).
				Value(&githubClientSecret),
		),
	).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr())
	if err := normalizeInstallPromptError(form.Run()); err != nil {
		return err
	}

	selections.EnableAppleNotes = containsString(features, "apple_notes")
	selections.EnableSafariTabs = containsString(features, "safari_tabs")
	selections.EnableScheduler = containsString(features, "scheduler")
	selections.EnableTailscale = containsString(features, "tailscale")
	selections.EnableGitHubLogin = containsString(features, "github_login")
	selections.UseKeychain = useKeychain && runtime.GOOS == "darwin"
	if strings.TrimSpace(selections.CategorizeModel) == "" {
		selections.CategorizeModel = selections.SummaryModel
	}
	if strings.TrimSpace(githubToken) != "" {
		selections.Secrets[installer.SecretGitHubToken] = githubToken
	}
	if strings.TrimSpace(openRouterKey) != "" {
		selections.Secrets[installer.SecretOpenRouterAPIKey] = openRouterKey
	}
	if strings.TrimSpace(tsnetKey) != "" {
		selections.Secrets[installer.SecretTSNetAuthKey] = tsnetKey
	}
	if strings.TrimSpace(githubClientSecret) != "" {
		selections.Secrets[installer.SecretGitHubClientSecret] = githubClientSecret
	}
	return nil
}

func normalizeInstallPromptError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return nil
	}
	return err
}

func applyDetectedPromptDefaults(selections *installer.Selections, tools []installer.Tool) {
	if selections == nil {
		return
	}
	if strings.TrimSpace(selections.SummaryModel) == "" {
		selections.SummaryModel = suggestedInstallModel(tools)
	}
	if strings.TrimSpace(selections.CategorizeModel) == "" {
		selections.CategorizeModel = selections.SummaryModel
	}
	if strings.TrimSpace(selections.OCRModel) == "" && toolAvailable(tools, installer.ToolTesseract) {
		selections.OCRModel = "tesseract"
	}
}

func suggestedInstallModel(tools []installer.Tool) string {
	if model := suggestedPreferredInstallModel(tools); model != "" {
		return model
	}
	for _, candidate := range []struct {
		id     installer.ToolID
		prefix string
	}{
		{installer.ToolLMStudioAPI, "lmstudio/"},
		{installer.ToolOMLXAPI, "omlx/"},
		{installer.ToolOllamaAPI, "ollama/"},
	} {
		for _, tool := range tools {
			if tool.ID == candidate.id && tool.Available && len(tool.Models) > 0 {
				return candidate.prefix + tool.Models[0]
			}
		}
	}
	return ""
}

func suggestedPreferredInstallModel(tools []installer.Tool) string {
	if model := firstAvailableModel(tools, installer.ToolOllamaAPI, func(model string) bool {
		return model == installDbrainOllamaAPIModel
	}, "ollama/"); model != "" {
		return model
	}
	if model := firstAvailableModel(tools, installer.ToolOMLXAPI, func(model string) bool {
		return model == installDbrainOMLXAPIModel
	}, "omlx/"); model != "" {
		return model
	}
	if model := firstAvailableModel(tools, installer.ToolOllamaAPI, func(model string) bool {
		return model == installSmallOllamaAPIModel
	}, "ollama/"); model != "" {
		return model
	}
	return ""
}

func firstAvailableModel(tools []installer.Tool, id installer.ToolID, match func(string) bool, prefix string) string {
	for _, tool := range tools {
		if tool.ID != id || !tool.Available {
			continue
		}
		if model := firstMatchingModel(tool.Models, func(model string) bool {
			return match(model)
		}); model != "" {
			return prefix + model
		}
	}
	return ""
}

func firstMatchingModel(models []string, match func(string) bool) string {
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" && match(model) {
			return model
		}
	}
	return ""
}

func installModelForProfile(profile string) string {
	switch strings.TrimSpace(strings.ToLower(profile)) {
	case "", installModelProfileNone:
		return ""
	case installModelProfileDbrain, installModelProfileDbrainOllama:
		return installDbrainOllamaModel
	case installModelProfileDbrainOMLX:
		return installDbrainOMLXModel
	case installModelProfileSmallOllama:
		return installSmallOllamaModel
	default:
		return ""
	}
}

func validateInstallModelProfile(profile string) error {
	switch strings.TrimSpace(strings.ToLower(profile)) {
	case "", installModelProfileNone, installModelProfileDbrain, installModelProfileDbrainOllama, installModelProfileDbrainOMLX, installModelProfileSmallOllama:
		return nil
	default:
		return fmt.Errorf("unknown local model profile %q; use %s, %s, %s, %s, or %s", profile, installModelProfileDbrain, installModelProfileDbrainOllama, installModelProfileDbrainOMLX, installModelProfileSmallOllama, installModelProfileNone)
	}
}

func toolAvailable(tools []installer.Tool, id installer.ToolID) bool {
	for _, tool := range tools {
		if tool.ID == id && tool.Available {
			return true
		}
	}
	return false
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func installToolSummary(tools []installer.Tool) string {
	available := []string{}
	for _, tool := range tools {
		if tool.Available {
			if len(tool.Models) > 0 {
				available = append(available, fmt.Sprintf("%s (%d models)", tool.Name, len(tool.Models)))
			} else {
				available = append(available, tool.Name)
			}
		}
	}
	if len(available) == 0 {
		return "No local helper tools were detected yet. You can still write the base config now."
	}
	return "Detected: " + strings.Join(available, ", ")
}

func printInstallResult(w io.Writer, result installer.Result, selections installer.Selections, goos string) {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	_, _ = fmt.Fprintln(w, titleStyle.Render("dbrain install complete"))
	_, _ = fmt.Fprintf(w, "Config: %s\n", result.ConfigPath)
	_, _ = fmt.Fprintf(w, "Categories: %s\n\n", result.CategoriesPath)

	if len(result.Changes) > 0 {
		changes := table.New().
			Headers("Change", "Path").
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240")))
		for _, change := range result.Changes {
			changes.Row(string(change.Kind), change.Path)
		}
		_, _ = fmt.Fprintln(w, changes.Render())
	}

	if len(result.Tools) > 0 {
		tools := table.New().
			Headers("Tool", "Status", "Detail").
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240")))
		for _, tool := range result.Tools {
			status := "missing"
			detail := tool.Detail
			if tool.Available {
				status = "found"
				detail = firstNonEmptyInstall(tool.Path, tool.Endpoint, strings.Join(tool.Models, ", "))
			} else if tool.Error != "" {
				detail = tool.Error
			}
			tools.Row(tool.Name, status, detail)
		}
		_, _ = fmt.Fprintln(w, tools.Render())
	}

	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", warning)
	}
	if selections.EnableScheduler {
		if goos == "darwin" {
			_, _ = fmt.Fprintln(w, "Scheduler enabled in config. Use dbrain launchd install or dbrain install --install-launchd to install the per-user macOS service.")
		} else {
			_, _ = fmt.Fprintln(w, "Scheduler enabled in config. Configure your OS service manager to run dbrain serve remote or dbrain sync all.")
		}
	}
	if selections.EnableTailscale {
		_, _ = fmt.Fprintln(w, "Tailscale enabled in config under tsnet.*; login remains configured separately under auth.*.")
	}
	if selections.EnableGitHubLogin {
		_, _ = fmt.Fprintln(w, "GitHub login enabled in config under auth.*.")
	}
}

func firstNonEmptyInstall(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func installLaunchdFromInstall(ctx context.Context, cfg config.Config, selectorArgs []string, flags installFlags) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("--install-launchd is currently supported only on macOS")
	}
	binPath, err := resolveLaunchdBinPath(flags.launchdBinPath)
	if err != nil {
		return err
	}
	plistPath, err := launchdPlistPath(flags.launchdLabel)
	if err != nil {
		return err
	}
	plist, err := renderLaunchdPlist(launchdPlistOptions{
		Label:   flags.launchdLabel,
		BinPath: binPath,
		Args:    selectorArgs,
		OutPath: filepath.Join(cfg.LogDir, "launchd.out.log"),
		ErrPath: filepath.Join(cfg.LogDir, "launchd.err.log"),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create launch agents dir: %w", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	if err := os.WriteFile(plistPath, plist, 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", plistPath, err)
	}
	if !flags.startLaunchd {
		return nil
	}
	domain := launchdDomain()
	target := domain + "/" + flags.launchdLabel
	_ = runLaunchctl(ctx, "bootout", domain, plistPath)
	if err := runLaunchctl(ctx, "bootstrap", domain, plistPath); err != nil {
		return err
	}
	if err := runLaunchctl(ctx, "enable", target); err != nil {
		return err
	}
	return runLaunchctl(ctx, "kickstart", "-k", target)
}
