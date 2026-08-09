package app

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newAuthCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication approvals and tokens",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthGitHubCommand(root), newAuthMCPCommand(root), newAuthMastodonCommand(root))
	return cmd
}

func newAuthGitHubCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Manage approved GitHub web login users",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthGitHubApproveCommand(root), newAuthGitHubListCommand(root), newAuthGitHubRemoveCommand(root))
	return cmd
}

func newAuthGitHubApproveCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "approve USERNAME",
		Short: "Approve a GitHub username for web UI login",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			user, created, err := st.ApproveGitHubAuthUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"created": created,
					"user":    user,
				})
			}
			status := "already approved"
			if created {
				status = "approved"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s github user %s\n", status, user.GitHubUsernameNormalized)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print approval as JSON")
	return cmd
}

func newAuthGitHubListCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List approved GitHub web login users",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			users, err := st.ListGitHubAuthUsers(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), users)
			}
			if len(users) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no approved github users")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tUSERNAME\tGITHUB_ID\tEMAIL\tLAST_LOGIN\tAPPROVED")
			for _, user := range users {
				_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n",
					user.ID,
					user.GitHubUsernameNormalized,
					user.GitHubID,
					user.Email,
					user.LastLoginAt,
					user.ApprovedAt,
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print approved users as JSON")
	return cmd
}

func newAuthGitHubRemoveCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "remove USERNAME",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a GitHub username from web UI login approval",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			user, removed, err := st.RemoveGitHubAuthUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"removed": removed,
					"user":    user,
				})
			}
			if !removed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "github user %s was not approved\n", store.NormalizeGitHubUsername(args[0]))
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed github user %s\n", user.GitHubUsernameNormalized)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print removal as JSON")
	return cmd
}

func newAuthMCPCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP bearer tokens",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthMCPTokenCommand(root))
	return cmd
}

func newAuthMCPTokenCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage MCP bearer tokens",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthMCPTokenAddCommand(root), newAuthMCPTokenListCommand(root), newAuthMCPTokenRevokeCommand(root))
	return cmd
}

func newAuthMCPTokenAddCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Create an MCP bearer token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			result, err := st.CreateMCPBearerToken(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created mcp bearer token %s\n", result.Record.Name)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "token (shown once): %s\n", result.Token)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "use header: Authorization: Bearer <token>")
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print created token as JSON")
	return cmd
}

func newAuthMCPTokenListCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List MCP bearer token records without revealing token secrets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			tokens, err := st.ListMCPBearerTokens(cmd.Context(), all)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), tokens)
			}
			if len(tokens) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no mcp bearer tokens")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tNAME\tFINGERPRINT\tSTATUS\tCREATED\tUPDATED")
			for _, token := range tokens {
				_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n",
					token.ID,
					token.Name,
					token.TokenFingerprint,
					mcpBearerTokenStatus(token),
					token.CreatedAt,
					token.UpdatedAt,
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print token records as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "Include revoked token records")
	return cmd
}

func newAuthMCPTokenRevokeCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "revoke ID_OR_NAME_OR_FINGERPRINT",
		Aliases: []string{"remove", "rm", "delete"},
		Short:   "Revoke an MCP bearer token by id, unique name, or fingerprint",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			token, revoked, err := st.RevokeMCPBearerToken(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"revoked": revoked,
					"token":   token,
				})
			}
			if token.ID == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp bearer token %s was not found\n", args[0])
				return nil
			}
			if !revoked {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp bearer token %s was already revoked\n", token.TokenFingerprint)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "revoked mcp bearer token %s (%s)\n", token.Name, token.TokenFingerprint)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print revocation as JSON")
	return cmd
}

func mcpBearerTokenStatus(token store.MCPBearerToken) string {
	if token.RevokedAt != "" {
		return "revoked"
	}
	return "active"
}
