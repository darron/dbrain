package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
)

func newServeWebCommand(root *rootOptions) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve the local brain over HTTP",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			startuplog.WriteVersion(cmd.ErrOrStderr())
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Web UI: http://%s\n", addr)
			handlerOptions := web.HandlerOptions{}
			auditEnabled, err := web.AuditAPIEnabled(cmd.Context(), cfg)
			if err != nil {
				return fmt.Errorf("resolve web audit authentication: %w", err)
			}
			if auditEnabled {
				dependencies, auditErr := buildWebAuditHandlerDependencies(cmd.Context(), cfg, auditMetaForInvocation(root))
				if auditErr != nil {
					return auditErr
				}
				handlerOptions.AuditReports = dependencies.Reports
				handlerOptions.AuditRuns = dependencies.Runs
				handlerOptions.AuditSyncInterval = dependencies.SyncInterval
				handlerOptions.AuditStandardInterval = dependencies.StandardInterval
			}
			storeOpenOptions := interactiveStoreOpenOptions()
			storeOpenOptions.MigrationReporter = startuplog.MigrationReporter(cmd.ErrOrStderr())
			return web.ServeWithOptions(cmd.Context(), cfg, addr, web.ServeOptions{
				StoreOpenOptions: storeOpenOptions,
				HandlerOptions:   handlerOptions,
			})
		},
	}

	cmd.Flags().StringVar(&addr, "addr", web.DefaultAddr(), "HTTP listen address")

	return cmd
}

func interactiveStoreOpenOptions() store.OpenOptions {
	return store.OpenOptions{
		MaxOpenConns: store.InteractivePoolSize,
		MaxIdleConns: store.InteractivePoolSize,
	}
}
