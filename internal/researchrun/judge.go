package researchrun

import (
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

type JudgeOptions struct {
	MinEvidenceForEnough int
	AllowRetry           bool
	FocusQuestion        string
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

	anchorSupport := protectedAnchorSupport(pack.QueryPlan.ProtectedAnchors, appendEvidenceRows(directRows, pack.ExactTagEvidence))
	judgeRows := evidenceRowsForJudge(pack, directRows)
	if len(pack.QueryPlan.ProtectedAnchors) > 0 && len(judgeRows) == 0 {
		missing := protectedAnchorConcepts(pack.QueryPlan)
		result := JudgeResult{
			Verdict:         JudgeWeakEvidence,
			Reason:          "no direct evidence matched protected anchors",
			MissingConcepts: missing,
			RetryAction:     RetryNone,
			WeakRows:        weakRowsWithMissing(limitEvidenceRows(appendEvidenceRows(directRows, pack.ExactTagEvidence), 3), "missing_protected_anchor", missing),
			AnchorSupport:   anchorSupport,
		}
		if opts.AllowRetry && len(missing) > 0 {
			result.RetryAction = RetryFocusedVariant
			result.RetryVariant = strings.Join(missing, " ")
		}
		return result
	}

	top := firstEvidence(judgeRows, pack.ExactTagEvidence)
	missing := missingConceptsAcrossRows(judgeRows, pack.QueryPlan, opts.FocusQuestion)
	if len(missing) > 0 {
		result := JudgeResult{
			Verdict:         JudgeWeakEvidence,
			Reason:          "top evidence is missing required concepts",
			MissingConcepts: missing,
			RetryAction:     RetryNone,
			WeakRows:        weakRowsWithMissing(judgeRows, "missing_required_concepts", missing),
			AnchorSupport:   anchorSupport,
		}
		if opts.AllowRetry {
			result.RetryAction = RetryFocusedVariant
			result.RetryVariant = strings.Join(missing, " ")
		}
		return result
	}

	if len(directRows)+len(pack.ExactTagEvidence) < minEvidence {
		result := JudgeResult{
			Verdict:       JudgeWeakEvidence,
			Reason:        "direct evidence below minimum",
			RetryAction:   RetryNone,
			WeakRows:      []WeakEvidenceRow{weakRow(top, "too_few_direct_rows", nil)},
			AnchorSupport: anchorSupport,
		}
		if opts.AllowRetry && strings.TrimSpace(top.SourceKey) != "" {
			result.RetryAction = RetryRelatedExpansion
			result.ExpansionLookup = top.SourceKey
		}
		return result
	}

	return JudgeResult{
		Verdict:       JudgeEnoughEvidence,
		Reason:        "direct evidence satisfies the minimum evidence threshold",
		RetryAction:   RetryNone,
		AnchorSupport: anchorSupport,
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

func evidenceRowsForJudge(pack brainresearch.Pack, directRows []ask.Evidence) []ask.Evidence {
	allDirectEvidence := appendEvidenceRows(directRows, pack.ExactTagEvidence)
	if len(pack.QueryPlan.ProtectedAnchors) > 0 {
		anchored := make([]ask.Evidence, 0, len(allDirectEvidence))
		for _, row := range allDirectEvidence {
			if brainresearch.EvidenceMatchesAnyProtectedAnchor(row, pack.QueryPlan.ProtectedAnchors) {
				anchored = append(anchored, row)
			}
		}
		return limitEvidenceRows(anchored, 3)
	}
	rows := limitEvidenceRows(directRows, 3)
	rows = append(rows, limitEvidenceRows(pack.ExactTagEvidence, 3)...)
	return rows
}

func appendEvidenceRows(a []ask.Evidence, b []ask.Evidence) []ask.Evidence {
	out := make([]ask.Evidence, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func limitEvidenceRows(rows []ask.Evidence, limit int) []ask.Evidence {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

type conceptJudgeInfo struct {
	Key      string
	Role     string
	Required bool
}

func missingConceptsAcrossRows(rows []ask.Evidence, plan brainresearch.QueryPlan, focusQuestion string) []string {
	if len(rows) == 0 {
		return nil
	}
	lookup := conceptJudgeLookup(plan.Concepts)
	if len(lookup) == 0 {
		return focusMissingConcepts(missingConcepts(rows[0]), focusQuestion)
	}
	var order []string
	intersection := map[string]struct{}{}
	for i, row := range rows {
		filtered := filterMissingConceptsForPlan(missingConcepts(row), lookup, focusQuestion)
		rowSet := stringSet(filtered)
		if i == 0 {
			order = append([]string(nil), filtered...)
			intersection = rowSet
			continue
		}
		for key := range intersection {
			if _, ok := rowSet[key]; !ok {
				delete(intersection, key)
			}
		}
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		if _, ok := intersection[key]; ok {
			out = append(out, key)
		}
	}
	return out
}

func filterMissingConceptsForPlan(missing []string, lookup map[string]conceptJudgeInfo, focusQuestion string) []string {
	out := make([]string, 0, len(missing))
	seen := map[string]struct{}{}
	for _, term := range missing {
		info, ok := lookup[normalizeJudgeTerm(term)]
		if !ok || !info.Required || !judgeRoleCanRequireRetry(info.Role) {
			continue
		}
		key := strings.TrimSpace(info.Key)
		if key == "" {
			key = strings.TrimSpace(term)
		}
		if key == "" {
			continue
		}
		normalizedKey := strings.ToLower(key)
		if _, exists := seen[normalizedKey]; exists {
			continue
		}
		seen[normalizedKey] = struct{}{}
		out = append(out, key)
	}
	return focusMissingConcepts(out, focusQuestion)
}

func conceptJudgeLookup(concepts []brainresearch.QueryConcept) map[string]conceptJudgeInfo {
	lookup := map[string]conceptJudgeInfo{}
	for _, concept := range concepts {
		info := conceptJudgeInfo{
			Key:      strings.TrimSpace(concept.Key),
			Role:     strings.ToLower(strings.TrimSpace(concept.Role)),
			Required: concept.Required,
		}
		if info.Role == "" {
			info.Role = "content"
		}
		for _, value := range append([]string{concept.Key, concept.Preferred}, concept.Terms...) {
			value = normalizeJudgeTerm(value)
			if value == "" {
				continue
			}
			if existing, ok := lookup[value]; ok && conceptJudgeRank(existing) >= conceptJudgeRank(info) {
				continue
			}
			lookup[value] = info
		}
	}
	return lookup
}

func conceptJudgeRank(info conceptJudgeInfo) int {
	rank := 0
	switch info.Role {
	case "anchor":
		rank = 40
	case "content":
		rank = 30
	case "intent":
		rank = 20
	case "frame":
		rank = 10
	}
	if info.Required {
		rank += 1
	}
	return rank
}

func judgeRoleCanRequireRetry(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "content", "anchor":
		return true
	default:
		return false
	}
}

func normalizeJudgeTerm(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}

func focusMissingConcepts(missing []string, focusQuestion string) []string {
	focusTerms := ask.Hints(focusQuestion).Terms
	if len(missing) == 0 || len(focusTerms) == 0 {
		return missing
	}
	focusSet := make(map[string]struct{}, len(focusTerms))
	for _, term := range focusTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		focusSet[term] = struct{}{}
	}
	out := missing[:0]
	for _, term := range missing {
		if _, ok := focusSet[strings.ToLower(strings.TrimSpace(term))]; !ok {
			continue
		}
		out = append(out, term)
	}
	return out
}

func weakRows(rows []ask.Evidence, reason string) []WeakEvidenceRow {
	out := make([]WeakEvidenceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, weakRow(row, reason, missingConcepts(row)))
	}
	return out
}

func weakRowsWithMissing(rows []ask.Evidence, reason string, missing []string) []WeakEvidenceRow {
	out := make([]WeakEvidenceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, weakRow(row, reason, missing))
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

func protectedAnchorSupport(anchors []brainresearch.ProtectedAnchor, rows []ask.Evidence) map[string]int {
	if len(anchors) == 0 || len(rows) == 0 {
		return nil
	}
	out := map[string]int{}
	for _, anchor := range anchors {
		key := protectedAnchorSupportKey(anchor)
		if key == "" {
			continue
		}
		for _, row := range rows {
			if brainresearch.EvidenceMatchesProtectedAnchor(row, anchor) {
				out[key]++
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func protectedAnchorSupportKey(anchor brainresearch.ProtectedAnchor) string {
	for _, value := range []string{anchor.ResolvedID, anchor.Canonical, anchor.Raw} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func protectedAnchorConcepts(plan brainresearch.QueryPlan) []string {
	out := make([]string, 0, len(plan.ProtectedAnchors))
	seen := map[string]struct{}{}
	for _, concept := range plan.Concepts {
		if strings.ToLower(strings.TrimSpace(concept.Role)) != "anchor" || !concept.Required {
			continue
		}
		key := strings.TrimSpace(concept.Key)
		if key == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(key)]; ok {
			continue
		}
		seen[strings.ToLower(key)] = struct{}{}
		out = append(out, key)
	}
	if len(out) > 0 {
		return out
	}
	for _, anchor := range plan.ProtectedAnchors {
		key := protectedAnchorSupportKey(anchor)
		if key == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(key)]; ok {
			continue
		}
		seen[strings.ToLower(key)] = struct{}{}
		out = append(out, key)
	}
	return out
}
