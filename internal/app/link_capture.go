package app

import (
	"fmt"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newLinkCaptureCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Inspect and recover deferred link captures",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newLinkCaptureDeadLettersCommand(root), newLinkCaptureRequeueCommand(root))
	return cmd
}

func newLinkCaptureDeadLettersCommand(root *rootOptions) *cobra.Command {
	var limit int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "dead-letters",
		Short: "List deferred link captures parked after bounded retries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			captures, err := st.ListDeadLetteredLinkCaptures(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), captures)
			}
			if len(captures) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no dead-lettered link captures")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tURL\tATTEMPTS\tFAILURE_KIND\tDEAD_LETTERED\tUPDATED")
			for _, capture := range captures {
				_, _ = fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\n",
					capture.ID,
					redactURLUserInfo(capture.Candidate.NormalizedURL),
					capture.AttemptCount,
					capture.LastError,
					capture.DeadLetteredAt,
					capture.UpdatedAt,
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum parked captures to list")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print parked captures as JSON")
	return cmd
}

type linkCaptureRequeueResult struct {
	Capture  store.LinkCapture `json:"capture"`
	Reopened bool              `json:"reopened"`
}

func newLinkCaptureRequeueCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "requeue ID [ID...]",
		Short: "Reopen parked deferred link captures",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			results := make([]linkCaptureRequeueResult, 0, len(args))
			for _, rawID := range args {
				id, err := strconv.ParseInt(rawID, 10, 64)
				if err != nil || id <= 0 {
					return fmt.Errorf("invalid link capture ID %q", rawID)
				}
				capture, err := st.GetLinkCapture(cmd.Context(), id)
				if err != nil {
					return err
				}
				if capture.DeadLetteredAt.IsZero() {
					return fmt.Errorf("link capture %d is not dead-lettered", id)
				}
				reopened, err := st.EnqueueLinkCapture(cmd.Context(), capture.Candidate, time.Now().UTC())
				if err != nil {
					return err
				}
				results = append(results, linkCaptureRequeueResult{Capture: reopened.Capture, Reopened: reopened.Reopened})
			}
			if jsonOut {
				if len(results) == 1 {
					return writeJSON(cmd.OutOrStdout(), results[0])
				}
				return writeJSON(cmd.OutOrStdout(), results)
			}
			for _, result := range results {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "requeued link capture %d %s\n", result.Capture.ID, redactURLUserInfo(result.Capture.Candidate.NormalizedURL))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print requeued captures as JSON")
	return cmd
}
