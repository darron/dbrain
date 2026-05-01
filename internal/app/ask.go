package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/store"
)

func newAskCommand(root *rootOptions) *cobra.Command {
	var limit int
	var retrieveOnly bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var maxCharsPerDoc int
	var sourceTypes []string
	var includeRelated bool
	var relatedLimit int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Answer a question from the local brain with retrieved evidence",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root)
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

			response, err := ask.Run(cmd.Context(), cfg, st, strings.Join(args, " "), ask.Options{
				Limit:          limit,
				RetrieveOnly:   retrieveOnly,
				Model:          model,
				CLI:            cliProvider,
				Length:         length,
				Timeout:        timeout,
				MaxCharsPerDoc: maxCharsPerDoc,
				SourceTypes:    sourceTypes,
				IncludeRelated: includeRelated,
				RelatedLimit:   relatedLimit,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), response)
			}

			if strings.TrimSpace(response.Answer) != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), response.Answer)
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
			}

			if len(response.Evidence) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No matching evidence found.")
				return nil
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Retrieved evidence:")
			for _, doc := range response.Evidence {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- [%s] %s\n", doc.SourceKey, doc.Title)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  kind: %s\n", doc.Kind)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  url: %s\n", doc.URL)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  note: %s\n", doc.NotePath)
				if doc.SourceType != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  source_type: %s\n", doc.SourceType)
				}
				if len(doc.EntityMatches) > 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  entity_matches: %s\n", strings.Join(doc.EntityMatches, ", "))
				}
				if doc.Relationship != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  relationship: %s", doc.Relationship)
					if doc.RelatedTo != "" {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), " (%s)", doc.RelatedTo)
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout())
				}
				if doc.Summary != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  summary: %s\n", singleLine(doc.Summary))
				} else if doc.Excerpt != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  excerpt: %s\n", singleLine(doc.Excerpt))
				}
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 8, "Maximum pieces of evidence to retrieve")
	cmd.Flags().BoolVar(&retrieveOnly, "retrieve-only", false, "Skip synthesized answering and only return retrieved evidence")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Answer length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for summarize.sh answer synthesis")
	cmd.Flags().IntVar(&maxCharsPerDoc, "max-chars-per-doc", 1800, "Maximum summary/excerpt characters per retrieved document")
	cmd.Flags().StringSliceVar(&sourceTypes, "source-type", nil, "Optional source type filter; repeat or comma-separate values like github, web, x_bookmark, apple_note")
	cmd.Flags().BoolVar(&includeRelated, "include-related", false, "Include related evidence from linked items or sources")
	cmd.Flags().IntVar(&relatedLimit, "related-limit", 2, "Maximum number of related evidence documents to append when --include-related is set")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the answer and evidence as JSON")

	return cmd
}

func singleLine(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) <= 220 {
		return value
	}
	return string(runes[:220]) + "..."
}
