package researchrun

import (
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

type JudgeOptions struct {
	MinEvidenceForEnough int
	AllowRetry           bool
}

func Judge(pack brainresearch.Pack, opts JudgeOptions) JudgeResult {
	minEvidence := opts.MinEvidenceForEnough
	if minEvidence <= 0 {
		minEvidence = 1
	}
	totalEvidence := len(pack.Evidence) + len(pack.ExactTagEvidence)
	if totalEvidence == 0 {
		return JudgeResult{
			Verdict:     JudgeNoEvidence,
			Reason:      "pack has no direct or exact-tag evidence",
			RetryAction: RetryNone,
		}
	}

	directRows := directEvidence(pack.Evidence)
	if len(directRows) == 0 && len(pack.ExactTagEvidence) == 0 {
		return JudgeResult{
			Verdict:     JudgeWeakEvidence,
			Reason:      "pack contains only related evidence",
			RetryAction: RetryNone,
			WeakRows:    weakRows(pack.Evidence, "related_only"),
		}
	}

	top := firstEvidence(directRows, pack.ExactTagEvidence)
	missing := missingConcepts(top)
	if len(missing) > 0 {
		result := JudgeResult{
			Verdict:         JudgeWeakEvidence,
			Reason:          "top evidence is missing required concepts",
			MissingConcepts: missing,
			RetryAction:     RetryNone,
			WeakRows:        []WeakEvidenceRow{weakRow(top, "missing_required_concepts", missing)},
		}
		if opts.AllowRetry {
			result.RetryAction = RetryFocusedVariant
			result.RetryVariant = strings.Join(missing, " ")
		}
		return result
	}

	if len(directRows)+len(pack.ExactTagEvidence) < minEvidence {
		result := JudgeResult{
			Verdict:     JudgeWeakEvidence,
			Reason:      "direct evidence below minimum",
			RetryAction: RetryNone,
			WeakRows:    []WeakEvidenceRow{weakRow(top, "too_few_direct_rows", nil)},
		}
		if opts.AllowRetry && strings.TrimSpace(top.SourceKey) != "" {
			result.RetryAction = RetryRelatedExpansion
			result.ExpansionLookup = top.SourceKey
		}
		return result
	}

	return JudgeResult{
		Verdict:     JudgeEnoughEvidence,
		Reason:      "direct evidence satisfies the minimum evidence threshold",
		RetryAction: RetryNone,
	}
}

func directEvidence(rows []ask.Evidence) []ask.Evidence {
	out := make([]ask.Evidence, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.Relationship) != "" || strings.TrimSpace(row.RelatedTo) != "" {
			continue
		}
		out = append(out, row)
	}
	return out
}

func firstEvidence(primary []ask.Evidence, fallback []ask.Evidence) ask.Evidence {
	if len(primary) > 0 {
		return primary[0]
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return ask.Evidence{}
}

func missingConcepts(row ask.Evidence) []string {
	if row.Retrieval == nil {
		return nil
	}
	return append([]string(nil), row.Retrieval.MissingTerms...)
}

func weakRows(rows []ask.Evidence, reason string) []WeakEvidenceRow {
	out := make([]WeakEvidenceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, weakRow(row, reason, missingConcepts(row)))
	}
	return out
}

func weakRow(row ask.Evidence, reason string, missing []string) WeakEvidenceRow {
	return WeakEvidenceRow{
		SourceKey:       row.SourceKey,
		Reason:          reason,
		Relationship:    strings.TrimSpace(strings.Join([]string{row.Relationship, row.RelatedTo}, " ")),
		MissingConcepts: append([]string(nil), missing...),
	}
}
