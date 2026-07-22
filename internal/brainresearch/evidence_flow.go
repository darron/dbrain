package brainresearch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

const EvidenceFlowSchemaVersion = "evidence_flow.v1"

type EvidenceFlow struct {
	SchemaVersion                   string                  `json:"schema_version"`
	RetrievalStatus                 string                  `json:"retrieval_status"`
	InspectionStatus                string                  `json:"inspection_status"`
	PreparationStatus               string                  `json:"preparation_status"`
	SynthesisStatus                 string                  `json:"synthesis_status"`
	SynthesisSkippedReason          string                  `json:"synthesis_skipped_reason,omitempty"`
	Retried                         bool                    `json:"retried,omitempty"`
	RetrievedSourceKeys             []string                `json:"retrieved_source_keys,omitempty"`
	RelevanceAdmittedSourceKeys     []string                `json:"relevance_admitted_source_keys,omitempty"`
	RelevanceExcludedSourceKeys     []string                `json:"relevance_excluded_source_keys,omitempty"`
	PromptAdmittedSourceKeys        []string                `json:"prompt_admitted_source_keys,omitempty"`
	BudgetDroppedSourceKeys         []string                `json:"budget_dropped_source_keys,omitempty"`
	PartiallyTrimmedSourceKeys      []string                `json:"partially_trimmed_source_keys,omitempty"`
	AnswerCitedSourceKeys           []string                `json:"answer_cited_source_keys,omitempty"`
	InvalidAnswerCitationSourceKeys []string                `json:"invalid_answer_citation_source_keys,omitempty"`
	RetrievedRows                   []EvidenceFlowRow       `json:"retrieved_rows,omitempty"`
	Inspection                      *EvidenceFlowInspection `json:"inspection,omitempty"`
	InvariantErrors                 []string                `json:"invariant_errors,omitempty"`
}

type EvidenceFlowRow struct {
	SourceKey       string `json:"source_key"`
	Lane            string `json:"lane"`
	Rank            int    `json:"rank"`
	ChunkIndex      int    `json:"chunk_index,omitempty"`
	ParentSourceKey string `json:"parent_source_key,omitempty"`
	ChunkRole       string `json:"chunk_role,omitempty"`
}

type EvidenceFlowInspection struct {
	InspectedSourceKeys       []string `json:"inspected_source_keys,omitempty"`
	HydrationFailedSourceKeys []string `json:"hydration_failed_source_keys,omitempty"`
	PromotedSourceKeys        []string `json:"promoted_source_keys,omitempty"`
	DemotedSourceKeys         []string `json:"demoted_source_keys,omitempty"`
	RawSupportSourceKeys      []string `json:"raw_support_source_keys,omitempty"`
}

func BuildEvidenceFlow(pack *Pack, prepared *PreparedSynthesis, synthesis *SynthesisResult, retried bool) EvidenceFlow {
	flow := EvidenceFlow{
		SchemaVersion:     EvidenceFlowSchemaVersion,
		RetrievalStatus:   "not_run",
		InspectionStatus:  "not_run",
		PreparationStatus: "not_run",
		SynthesisStatus:   "not_run",
		Retried:           retried,
	}
	if pack != nil {
		flow.RetrievalStatus = "ran"
		flow.RetrievedSourceKeys = sourceKeysFromEvidence(pack.Evidence, pack.ExactTagEvidence)
		flow.RetrievedRows = evidenceFlowRows(pack.Evidence, pack.ExactTagEvidence)
		if pack.Inspection != nil {
			flow.InspectionStatus = "ran"
			if len(pack.Inspection.Errors) > 0 {
				flow.InspectionStatus = "partial"
			}
			flow.Inspection = evidenceFlowInspection(pack.Inspection)
		}
	}
	if prepared != nil {
		flow.PreparationStatus = "ran"
		flow.RelevanceAdmittedSourceKeys = append([]string(nil), flow.RetrievedSourceKeys...)
		if prepared.Relevance != nil && prepared.Relevance.Applied {
			flow.RelevanceAdmittedSourceKeys = sortedUniqueStrings(prepared.Relevance.SelectedSourceKeys)
			flow.RelevanceExcludedSourceKeys = sortedUniqueStrings(prepared.Relevance.ExcludedSourceKeys)
		}
		flow.PromptAdmittedSourceKeys = citationKeys(prepared.Citations)
		// A source can appear in both primary and exact-tag lanes. Treat it as
		// source-key dropped only when no occurrence survived into the prompt.
		flow.BudgetDroppedSourceKeys = difference(sourceKeyDrops(prepared.Truncation.DroppedSourceKeys), flow.PromptAdmittedSourceKeys)
		if key := strings.TrimSpace(prepared.Truncation.PartiallyTrimmedSourceKey); key != "" {
			flow.PartiallyTrimmedSourceKeys = []string{key}
		}
	}
	if synthesis != nil {
		flow.SynthesisStatus = "ran"
		flow.AnswerCitedSourceKeys = citationKeys(synthesis.Citations)
	} else if prepared != nil {
		flow.SynthesisSkippedReason = "not_run_after_preparation"
	}
	flow.InvalidAnswerCitationSourceKeys = difference(flow.AnswerCitedSourceKeys, flow.PromptAdmittedSourceKeys)
	flow.InvariantErrors = ValidateEvidenceFlow(flow)
	return flow
}

func ValidateEvidenceFlow(flow EvidenceFlow) []string {
	var errors []string
	if flow.PreparationStatus == "ran" {
		errors = append(errors, subsetErrors("relevance_admitted", flow.RelevanceAdmittedSourceKeys, "retrieved", flow.RetrievedSourceKeys)...)
		errors = append(errors, subsetErrors("relevance_excluded", flow.RelevanceExcludedSourceKeys, "retrieved", flow.RetrievedSourceKeys)...)
		errors = append(errors, intersectionErrors("relevance_admitted", flow.RelevanceAdmittedSourceKeys, "relevance_excluded", flow.RelevanceExcludedSourceKeys)...)
		errors = append(errors, subsetErrors("prompt_admitted", flow.PromptAdmittedSourceKeys, "relevance_admitted", flow.RelevanceAdmittedSourceKeys)...)
		errors = append(errors, subsetErrors("budget_dropped", flow.BudgetDroppedSourceKeys, "relevance_admitted", flow.RelevanceAdmittedSourceKeys)...)
		errors = append(errors, intersectionErrors("budget_dropped", flow.BudgetDroppedSourceKeys, "prompt_admitted", flow.PromptAdmittedSourceKeys)...)
		errors = append(errors, subsetErrors("partially_trimmed", flow.PartiallyTrimmedSourceKeys, "prompt_admitted", flow.PromptAdmittedSourceKeys)...)
	}
	if flow.SynthesisStatus == "ran" {
		errors = append(errors, subsetErrors("answer_cited", flow.AnswerCitedSourceKeys, "prompt_admitted", flow.PromptAdmittedSourceKeys)...)
	}
	return sortedUniqueStrings(errors)
}

func evidenceFlowRows(primary []ask.Evidence, exact []ask.Evidence) []EvidenceFlowRow {
	rows := make([]EvidenceFlowRow, 0, len(primary)+len(exact))
	appendRows := func(lane string, evidence []ask.Evidence) {
		for i, row := range evidence {
			flowRow := EvidenceFlowRow{SourceKey: row.SourceKey, Lane: lane, Rank: i + 1}
			if row.Chunk != nil {
				flowRow.ChunkIndex = row.Chunk.Index
				flowRow.ParentSourceKey = row.Chunk.ParentSourceKey
				flowRow.ChunkRole = row.Chunk.Role
			}
			rows = append(rows, flowRow)
		}
	}
	appendRows("primary", primary)
	appendRows("exact_tag", exact)
	return rows
}

func evidenceFlowInspection(inspection *InspectionResult) *EvidenceFlowInspection {
	if inspection == nil {
		return nil
	}
	result := &EvidenceFlowInspection{InspectedSourceKeys: sortedUniqueStrings(inspection.InspectedSourceKeys)}
	for _, row := range inspection.Rows {
		if row.HydrationStatus == "hydration_failed" {
			result.HydrationFailedSourceKeys = append(result.HydrationFailedSourceKeys, row.SourceKey)
		}
		if row.RawSupport {
			result.RawSupportSourceKeys = append(result.RawSupportSourceKeys, row.SourceKey)
		}
		if row.RankAfter > 0 && row.RankBefore > 0 {
			switch {
			case row.RankAfter < row.RankBefore:
				result.PromotedSourceKeys = append(result.PromotedSourceKeys, row.SourceKey)
			case row.RankAfter > row.RankBefore:
				result.DemotedSourceKeys = append(result.DemotedSourceKeys, row.SourceKey)
			}
		}
	}
	result.HydrationFailedSourceKeys = sortedUniqueStrings(result.HydrationFailedSourceKeys)
	result.PromotedSourceKeys = sortedUniqueStrings(result.PromotedSourceKeys)
	result.DemotedSourceKeys = sortedUniqueStrings(result.DemotedSourceKeys)
	result.RawSupportSourceKeys = sortedUniqueStrings(result.RawSupportSourceKeys)
	return result
}

func sourceKeysFromEvidence(groups ...[]ask.Evidence) []string {
	var keys []string
	for _, rows := range groups {
		for _, row := range rows {
			keys = append(keys, row.SourceKey)
		}
	}
	return sortedUniqueStrings(keys)
}

func citationKeys(citations []Citation) []string {
	keys := make([]string, 0, len(citations))
	for _, citation := range citations {
		keys = append(keys, citation.SourceKey)
	}
	return sortedUniqueStrings(keys)
}

func sourceKeyDrops(keys []string) []string {
	var out []string
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || key == "topic_brief" {
			continue
		}
		out = append(out, key)
	}
	return sortedUniqueStrings(out)
}

func subsetErrors(childName string, child []string, parentName string, parent []string) []string {
	missing := difference(child, parent)
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s contains keys outside %s: %s", childName, parentName, strings.Join(missing, ", "))}
}

func intersectionErrors(leftName string, left []string, rightName string, right []string) []string {
	intersection := intersect(left, right)
	if len(intersection) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s overlaps %s: %s", leftName, rightName, strings.Join(intersection, ", "))}
}

func difference(left []string, right []string) []string {
	rightSet := stringSliceSet(right)
	var out []string
	for _, value := range left {
		if _, ok := rightSet[value]; !ok {
			out = append(out, value)
		}
	}
	return sortedUniqueStrings(out)
}

func intersect(left []string, right []string) []string {
	rightSet := stringSliceSet(right)
	var out []string
	for _, value := range left {
		if _, ok := rightSet[value]; ok {
			out = append(out, value)
		}
	}
	return sortedUniqueStrings(out)
}

func sortedUniqueStrings(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
