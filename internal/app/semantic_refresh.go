package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/semanticrefresh"
)

func newSemanticRefreshCommand(root *rootOptions, deps semanticRefreshDeps) *cobra.Command {
	var maxDuration time.Duration
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Run or resume configured semantic maintenance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxDuration < 0 {
				return fmt.Errorf("max-duration must not be negative")
			}
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			cancel := func() {}
			if maxDuration > 0 {
				timeout := deps.withTimeout
				if timeout == nil {
					timeout = context.WithTimeout
				}
				ctx, cancel = timeout(ctx, maxDuration)
			}
			defer cancel()

			started := time.Now()
			result, err := runConfiguredSemanticRefresh(
				ctx,
				cfg,
				deps,
				func(progress semanticrefresh.Progress) error {
					return writeSemanticRefreshProgress(cmd.ErrOrStderr(), progress)
				},
			)
			elapsed := time.Since(started)
			if err != nil {
				var refreshErr *semanticrefresh.RefreshError
				if !errors.As(err, &refreshErr) {
					return err
				}
				if jsonOut {
					if writeErr := writeJSON(cmd.OutOrStdout(), refreshErr); writeErr != nil {
						return writeErr
					}
					return &ExitError{Code: 1, Err: err, Silent: true}
				}
				return &ExitError{
					Code:   1,
					Err:    semanticRefreshHumanError{refreshErr: refreshErr, elapsed: elapsed},
					Silent: false,
				}
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), newSemanticRefreshResultOutput(result))
			}
			return writeSemanticRefreshResult(cmd.OutOrStdout(), result, elapsed)
		},
	}
	cmd.Flags().DurationVar(
		&maxDuration,
		"max-duration",
		0,
		"Maximum command runtime before cancellation (0 is unlimited)",
	)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the final result as JSON")
	return cmd
}

type semanticRefreshHumanError struct {
	refreshErr *semanticrefresh.RefreshError
	elapsed    time.Duration
}

func (e semanticRefreshHumanError) Error() string {
	var output bytes.Buffer
	if err := writeSemanticRefreshError(&output, e.refreshErr, e.elapsed); err != nil {
		return semanticrefresh.ErrorBackendBroken
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func (e semanticRefreshHumanError) Unwrap() error {
	return e.refreshErr
}
