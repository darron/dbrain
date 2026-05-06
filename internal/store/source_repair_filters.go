package store

import "strings"

func resetSourceEnrichmentWhere(opts ResetSourceEnrichmentOptions) (string, []any) {
	parts := make([]string, 0, 7)
	args := make([]any, 0, len(opts.Domains)*2+len(opts.SourceIDs)+len(opts.SourceTypes)+len(opts.ExtractStatuses)+len(opts.SummaryStatuses)+len(opts.FailureKinds)+1)

	domains := uniqueSourceResetDomains(opts.Domains)
	if len(domains) > 0 {
		domainParts := make([]string, 0, len(domains))
		for _, domain := range domains {
			domainParts = append(domainParts, `(lower(domain) = ? OR lower(domain) LIKE ?)`)
			args = append(args, domain, "%."+domain)
		}
		parts = append(parts, "("+strings.Join(domainParts, " OR ")+")")
	}

	sourceIDs := uniquePositiveInt64s(opts.SourceIDs)
	if len(sourceIDs) > 0 {
		placeholders := make([]string, 0, len(sourceIDs))
		for _, sourceID := range sourceIDs {
			placeholders = append(placeholders, "?")
			args = append(args, sourceID)
		}
		parts = append(parts, "id IN ("+strings.Join(placeholders, ",")+")")
	}

	if sourceTypes := uniqueLowerNonEmptyStrings(opts.SourceTypes); len(sourceTypes) > 0 {
		clause, clauseArgs := stringInClause("source_type", sourceTypes)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if extractStatuses := uniqueLowerNonEmptyStrings(opts.ExtractStatuses); len(extractStatuses) > 0 {
		clause, clauseArgs := stringInClause("extract_status", extractStatuses)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if summaryStatuses := uniqueLowerNonEmptyStrings(opts.SummaryStatuses); len(summaryStatuses) > 0 {
		clause, clauseArgs := stringInClause("summary_status", summaryStatuses)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if failureKinds := uniqueLowerNonEmptyStrings(opts.FailureKinds); len(failureKinds) > 0 {
		clause, clauseArgs := stringInClause("extract_failure_kind", failureKinds)
		parts = append(parts, clause)
		args = append(args, clauseArgs...)
	}

	if opts.MinFailures > 0 {
		parts = append(parts, "extract_failure_count >= ?")
		args = append(args, opts.MinFailures)
	}

	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

func uniqueSourceResetDomains(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "www.")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniquePositiveInt64s(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueLowerNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func stringInClause(column string, values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return "lower(" + column + ") IN (" + strings.Join(placeholders, ",") + ")", args
}
