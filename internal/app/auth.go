package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newAuthCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication approvals and tokens",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthGitHubCommand(root), newAuthMCPCommand(root))
	return cmd
}

func newAuthGitHubCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Manage approved GitHub web login users",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newAuthGitHubApproveCommand(root))
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
			st, err := store.Open(cfg.DBPath)
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
	cmd.AddCommand(newAuthMCPTokenAddCommand(root))
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
			st, err := store.Open(cfg.DBPath)
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
