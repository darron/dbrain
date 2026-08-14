package mcpeval

import (
	"context"
	"errors"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (report Report, err error) {
	started := time.Now().UTC()
	report = Report{
		StartedAt: started.Format(time.RFC3339),
		Cases:     make([]CaseResult, 0, len(opts.Cases)),
	}
	server := mcpserver.New(cfg, st)
	defer func() {
		err = errors.Join(err, server.Close())
	}()

	for _, tc := range opts.Cases {
		result, caseErr := runCase(ctx, cfg, st, server, tc)
		if caseErr != nil {
			return Report{}, caseErr
		}
		if result.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Cases = append(report.Cases, result)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report, nil
}
