package install

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/config"
)

func Run(ctx context.Context, opts Options) (Result, error) {
	fsys := opts.FS
	if fsys == nil {
		fsys = OSFS{}
	}
	cfg := opts.Config
	if strings.TrimSpace(cfg.ConfigPath) == "" {
		return Result{}, fmt.Errorf("install config path is required")
	}
	if len(opts.ConfigTemplate) == 0 {
		opts.ConfigTemplate = DefaultConfigTemplate
	}
	if len(opts.CategoriesTemplate) == 0 {
		opts.CategoriesTemplate = DefaultCategoriesTemplate
	}
	if strings.TrimSpace(opts.Runtime.GOOS) == "" {
		opts.Runtime.GOOS = runtime.GOOS
	}
	if opts.SecretStore == nil && opts.Runtime.GOOS == "darwin" {
		opts.SecretStore = KeychainSecretStore{}
	}

	result := Result{
		ConfigPath:     cfg.ConfigPath,
		CategoriesPath: cfg.CategoriesPath,
		Tools:          append([]Tool(nil), opts.Tools...),
		FS:             fsys,
	}
	result.Warnings = append(result.Warnings, selectionWarnings(opts.Selections)...)

	if !opts.DryRun {
		if err := ensureDirs(fsys, cfg); err != nil {
			return result, err
		}
	}

	modelChanges, err := prepareOllamaModels(ctx, fsys, cfg, opts)
	if err != nil {
		return result, err
	}
	result.Changes = append(result.Changes, modelChanges...)

	secretRefs, warnings, err := storeSecretRefs(ctx, opts)
	result.Warnings = append(result.Warnings, warnings...)
	if err != nil {
		return result, err
	}

	configData, err := buildConfig(opts.ConfigTemplate, opts.Selections, opts.Tools, secretRefs)
	if err != nil {
		return result, err
	}

	if change, err := writeManagedFile(fsys, cfg.ConfigPath, configData, 0o600, opts.Force, opts.DryRun); err != nil {
		return result, err
	} else {
		result.Changes = append(result.Changes, change)
	}
	if len(opts.CategoriesTemplate) > 0 {
		if change, err := writeManagedFile(fsys, cfg.CategoriesPath, opts.CategoriesTemplate, 0o644, opts.Force, opts.DryRun); err != nil {
			return result, err
		} else {
			result.Changes = append(result.Changes, change)
		}
	}

	return result, nil
}

func ensureDirs(fsys FileSystem, cfg config.Config) error {
	for _, dir := range []string{
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
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

func writeManagedFile(fsys FileSystem, path string, data []byte, perm os.FileMode, force bool, dryRun bool) (Change, error) {
	if strings.TrimSpace(path) == "" {
		return Change{}, fmt.Errorf("managed file path is required")
	}
	_, statErr := fsys.Stat(path)
	if statErr == nil && !force {
		return Change{Kind: ChangeSkipped, Path: path, Message: "already exists"}, nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Change{}, fmt.Errorf("stat %s: %w", path, statErr)
	}
	if dryRun {
		kind := ChangeCreated
		if statErr == nil {
			kind = ChangeUpdated
		}
		return Change{Kind: kind, Path: path, Message: "dry run"}, nil
	}
	if err := fsys.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Change{}, fmt.Errorf("create parent dir for %s: %w", path, err)
	}
	kind := ChangeCreated
	if _, err := fsys.Stat(path); err == nil {
		kind = ChangeUpdated
	}
	if err := fsys.WriteFile(path, data, perm); err != nil {
		return Change{}, fmt.Errorf("write %s: %w", path, err)
	}
	return Change{Kind: kind, Path: path}, nil
}

func storeSecretRefs(ctx context.Context, opts Options) (map[SecretKind]string, []string, error) {
	refs := map[SecretKind]string{}
	var warnings []string
	if opts.Selections.EnableGitHubLogin && strings.TrimSpace(opts.Selections.Secrets[SecretAuthSessionKey]) == "" {
		if opts.Selections.Secrets == nil {
			opts.Selections.Secrets = map[SecretKind]string{}
		}
		sessionKey, err := generateSessionKey()
		if err != nil {
			return refs, warnings, err
		}
		opts.Selections.Secrets[SecretAuthSessionKey] = sessionKey
	}
	if len(opts.Selections.Secrets) == 0 {
		return refs, warnings, nil
	}
	if !opts.Selections.UseKeychain || opts.Runtime.GOOS != "darwin" {
		if sessionKey := strings.TrimSpace(opts.Selections.Secrets[SecretAuthSessionKey]); sessionKey != "" {
			refs[SecretAuthSessionKey] = sessionKey
			warnings = append(warnings, "auth.session_key was written directly into the 0600 config because Keychain storage is disabled or unavailable.")
		}
		dropped := []string{}
		for kind, value := range opts.Selections.Secrets {
			if kind != SecretAuthSessionKey && strings.TrimSpace(value) != "" {
				dropped = append(dropped, string(kind))
			}
		}
		if len(dropped) > 0 {
			sort.Strings(dropped)
			warnings = append(warnings, "Secrets were provided but not written because Keychain storage is disabled or unavailable: "+strings.Join(dropped, ", ")+".")
		}
		return refs, warnings, nil
	}
	if opts.SecretStore == nil {
		return refs, warnings, fmt.Errorf("keychain secret store is not configured")
	}

	specs := make(map[SecretKind]secretSpec, len(secretSpecs))
	for _, spec := range secretSpecs {
		specs[spec.Kind] = spec
	}
	for kind, value := range opts.Selections.Secrets {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		spec, ok := specs[kind]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("Unknown secret kind %q was ignored.", kind))
			continue
		}
		if opts.DryRun {
			refs[kind] = "keychain://" + spec.Service + "/" + spec.Account
			continue
		}
		if err := opts.SecretStore.PutSecret(ctx, spec.Service, spec.Account, value); err != nil {
			return refs, warnings, fmt.Errorf("store %s in keychain: %w", kind, err)
		}
		refs[kind] = "keychain://" + spec.Service + "/" + spec.Account
	}
	return refs, warnings, nil
}

func selectionWarnings(selections Selections) []string {
	warnings := []string{}
	if selections.SkipXPhotoOCR {
		warnings = append(warnings, "X photo OCR is disabled because no OCR model or OpenRouter API key was configured.")
	}
	if selections.SkipCategorize {
		warnings = append(warnings, "Categorization is disabled because no categorization model or OpenRouter API key was configured.")
	}
	if !selections.EnableGitHubLogin {
		return warnings
	}
	baseURL := strings.TrimSpace(selections.AuthBaseURL)
	if baseURL == "" || strings.HasPrefix(baseURL, "http://127.0.0.1") || strings.HasPrefix(baseURL, "http://localhost") {
		return append(warnings, "auth.base_url is a localhost HTTP URL; GitHub OAuth callbacks will only work from the same machine, not remote/Tailscale browsers.")
	}
	if !strings.HasPrefix(baseURL, "https://") {
		return append(warnings, "auth.base_url is not HTTPS; GitHub OAuth callbacks for remote access should use an HTTPS URL.")
	}
	return warnings
}

func generateSessionKey() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate auth session key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
