package app

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/runtimeenv"
)

func newAuthMastodonCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mastodon",
		Short: "Manage Mastodon instance accounts and read-only bookmark access",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthMastodonLoginCommand(root), newAuthMastodonStatusCommand(root), newAuthMastodonLogoutCommand(root))
	return cmd
}

func newAuthMastodonLoginCommand(root *rootOptions) *cobra.Command {
	var instance string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "login ACCOUNT_KEY",
		Short: "Authorize a configured Mastodon account with read-only OAuth",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			account, err := loadMastodonAccount(root, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(instance) != "" {
				canonical, err := mastodonapi.CanonicalOrigin(instance)
				if err != nil {
					return fmt.Errorf("validate --instance: %w", err)
				}
				if canonical != account.Origin {
					return fmt.Errorf("--instance %s does not match configured origin %s", canonical, account.Origin)
				}
			}
			result, err := mastodonapi.Login(cmd.Context(), account, mastodonapi.LoginOptions{
				SecretStore: mastodonapi.KeychainSecretStore{},
				OnAuthorizationURL: func(rawURL string) {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "authorize Mastodon in a browser: %s\n", rawURL)
				},
				OpenBrowser: openMastodonAuthorizationURL,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "authorized Mastodon account %s on %s\n", result.AccountHandle, result.Origin)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "token fingerprint: %s\n", result.TokenFingerprint)
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "instance", "", "Verify the configured canonical HTTPS instance origin")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print authorization result as JSON")
	return cmd
}

func newAuthMastodonStatusCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status ACCOUNT_KEY",
		Short: "Verify a configured Mastodon account and show redacted status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			account, err := loadMastodonAccount(root, args[0])
			if err != nil {
				return err
			}
			metadata := account.RedactedMetadata()
			verified, err := mastodonapi.Status(cmd.Context(), account, mastodonapi.StatusOptions{})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), struct {
					Key                    string   `json:"key"`
					Enabled                bool     `json:"enabled"`
					Origin                 string   `json:"origin"`
					AccessTokenRefPresent  bool     `json:"access_token_ref_present"`
					ClientIDRefPresent     bool     `json:"client_id_ref_present"`
					ClientSecretRefPresent bool     `json:"client_secret_ref_present"`
					AccountID              string   `json:"account_id"`
					AccountHandle          string   `json:"account_handle"`
					EffectiveScopes        []string `json:"effective_scopes"`
					TokenFingerprint       string   `json:"token_fingerprint"`
				}{
					Key:                    metadata.Key,
					Enabled:                metadata.Enabled,
					Origin:                 verified.Origin,
					AccessTokenRefPresent:  metadata.AccessTokenRefPresent,
					ClientIDRefPresent:     metadata.ClientIDRefPresent,
					ClientSecretRefPresent: metadata.ClientSecretRefPresent,
					AccountID:              verified.AccountID,
					AccountHandle:          verified.AccountHandle,
					EffectiveScopes:        verified.EffectiveScopes,
					TokenFingerprint:       verified.TokenFingerprint,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mastodon account %s\n", metadata.Key)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "origin: %s\n", verified.Origin)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "enabled: %t\n", metadata.Enabled)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "verified account: %s (%s)\n", verified.AccountHandle, verified.AccountID)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "effective scopes: %s\n", strings.Join(verified.EffectiveScopes, " "))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "token fingerprint: %s\n", verified.TokenFingerprint)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "access token configured: %t\n", metadata.AccessTokenRefPresent)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "client configured: %t\n", metadata.ClientIDRefPresent && metadata.ClientSecretRefPresent)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print redacted status as JSON")
	return cmd
}

func newAuthMastodonLogoutCommand(root *rootOptions) *cobra.Command {
	var forgetClient bool
	var localOnly bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "logout ACCOUNT_KEY",
		Short: "Revoke and clear a configured Mastodon access token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			account, err := loadMastodonAccount(root, args[0])
			if err != nil {
				return err
			}
			result, err := mastodonapi.Logout(cmd.Context(), account, mastodonapi.LogoutOptions{
				SecretStore:  mastodonapi.KeychainSecretStore{},
				ForgetClient: forgetClient,
				LocalOnly:    localOnly,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cleared Mastodon access token for %s\n", account.Key)
			return nil
		},
	}
	cmd.Flags().BoolVar(&forgetClient, "forget-client", false, "Also delete the stored client credentials")
	cmd.Flags().BoolVar(&localOnly, "local-only", false, "Skip remote revocation and delete local secrets explicitly")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print logout result as JSON")
	return cmd
}

func loadMastodonAccount(root *rootOptions, key string) (mastodonapi.AccountConfig, error) {
	cfg, err := loadConfig(root.root, root.configFile)
	if err != nil {
		return mastodonapi.AccountConfig{}, err
	}
	raw, ok := runtimeenv.ConfigMap(cfg.RootDir, "mastodon")
	if !ok {
		return mastodonapi.AccountConfig{}, fmt.Errorf("mastodon configuration is missing")
	}
	mastodonConfig, err := mastodonapi.ParseConfig(raw)
	if err != nil {
		return mastodonapi.AccountConfig{}, err
	}
	for _, account := range mastodonConfig.Accounts {
		if account.Key == key {
			return account, nil
		}
	}
	return mastodonapi.AccountConfig{}, fmt.Errorf("mastodon account %q is not configured", key)
}

func openMastodonAuthorizationURL(rawURL string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{rawURL}
	case "linux":
		command, args = "xdg-open", []string{rawURL}
	default:
		return fmt.Errorf("automatic browser opening is unsupported on %s", runtime.GOOS)
	}
	return exec.Command(command, args...).Run()
}
