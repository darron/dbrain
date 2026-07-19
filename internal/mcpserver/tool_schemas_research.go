package mcpserver

func researchPackOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"schema_version":     scalarSchema("string", "Version of the research pack schema."),
		"question":           scalarSchema("string", "Original research question."),
		"mode":               scalarSchema("string", "Whether this pack contains evidence only or a topic brief plus evidence."),
		"query_plan":         researchQueryPlanSchema(),
		"coverage":           researchCoverageSchema(),
		"topic":              scalarSchema("string", "Inferred topic phrase when a topic brief was attached."),
		"used_topic_brief":   scalarSchema("boolean", "Whether a topic brief was inferred and attached."),
		"evidence":           arraySchema(evidenceSchema()),
		"exact_tag_evidence": arraySchema(evidenceSchema()),
		"topic_brief":        topicBriefOutputSchema(),
		"next_steps":         arraySchema(researchNextStepSchema()),
	}, "schema_version", "question", "mode", "query_plan", "coverage", "used_topic_brief", "evidence")
}

func researchQueryPlanSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"text_query":          scalarSchema("string", "Text query submitted to corpus search after stopword removal."),
		"query_terms":         arraySchema(scalarSchema("string", "Normalized query term.")),
		"tag_queries":         arraySchema(scalarSchema("string", "Hyphenated user_tag aliases searched in addition to text search.")),
		"query_variants":      arraySchema(researchQueryVariantSchema()),
		"concepts":            arraySchema(researchQueryConceptSchema()),
		"planner":             scalarSchema("string", "Planner path used for retrieval, such as deterministic or model_assisted."),
		"planner_model":       scalarSchema("string", "Model used for query planning when model-assisted planning ran."),
		"planner_error":       scalarSchema("string", "Non-fatal planner error when deterministic fallback was used."),
		"source_types":        arraySchema(scalarSchema("string", "Optional source type filters.")),
		"retrieval_lanes":     arraySchema(retrievalLaneSchema()),
		"limit":               scalarSchema("integer", "Maximum evidence documents requested."),
		"max_chars_per_doc":   scalarSchema("integer", "Maximum summary/excerpt characters per evidence document."),
		"include_related":     scalarSchema("boolean", "Whether graph-related evidence was requested."),
		"related_limit":       scalarSchema("integer", "Maximum related evidence documents."),
		"topic":               scalarSchema("string", "Topic used for the topic brief when present."),
		"topic_source":        scalarSchema("string", "How the topic was selected: explicit, inferred, or normalized_question."),
		"include_topic_brief": scalarSchema("boolean", "Whether a topic brief was requested for this pack."),
		"semantic_mode":       enumSchema("Effective semantic retrieval mode.", "off", "shadow", "on"),
		"shadow_comparison":   shadowComparisonSchema(),
	}, "text_query", "query_terms", "tag_queries", "limit", "max_chars_per_doc", "include_related", "include_topic_brief")
}

func retrievalLaneSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"name":         scalarSchema("string", "Retrieval lane name, such as lexical, exact_tag, graph_related, entity, or semantic."),
		"status":       scalarSchema("string", "Lane status, such as used or disabled."),
		"reason":       scalarSchema("string", "Why the lane was disabled or included."),
		"provider":     scalarSchema("string", "Provider used by the lane when applicable."),
		"store":        scalarSchema("string", "Vector or index store used by the lane when applicable."),
		"rank":         scalarSchema("integer", "One-based lane rank."),
		"raw_distance": scalarSchema("number", "Raw semantic distance."),
		"raw_score":    scalarSchema("number", "Raw lexical score."),
		"contribution": scalarSchema("number", "Fusion contribution."),
		"profile":      scalarSchema("string", "Embedding profile identifier."),
		"backend":      scalarSchema("string", "Retrieval backend."),
		"generation":   scalarSchema("string", "Index generation identifier."),
	}, "name")
}

func shadowComparisonSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"status":        scalarSchema("string", "Effective bounded comparison status."),
		"reason":        scalarSchema("string", "Stable content-free reason code."),
		"lexical_count": scalarSchema("integer", "Full lexical candidate count."),
		"hybrid_count":  scalarSchema("integer", "Full hybrid candidate count."),
		"lexical":       arraySchema(shadowRankedReferenceSchema()),
		"hybrid":        arraySchema(shadowRankedReferenceSchema()),
		"added":         arraySchema(shadowRankedReferenceSchema()),
		"removed":       arraySchema(shadowRankedReferenceSchema()),
		"reordered":     arraySchema(shadowRankedReferenceSchema()),
	}, "status", "lexical_count", "hybrid_count", "lexical", "hybrid", "added", "removed", "reordered")
}

func shadowRankedReferenceSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key": scalarSchema("string", "Stable parent source key."),
		"chunk_id":   scalarSchema("string", "Optional stable primary chunk identifier."),
		"rank":       scalarSchema("integer", "One-based rank."),
	}, "source_key", "rank")
}

func researchQueryVariantSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"query":  scalarSchema("string", "Bounded keyword query variant used for retrieval."),
		"reason": scalarSchema("string", "Why this query variant was added."),
	}, "query")
}

func researchQueryConceptSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":       scalarSchema("string", "Canonical concept key used for evidence scoring."),
		"preferred": scalarSchema("string", "Preferred search term for this concept."),
		"terms":     arraySchema(scalarSchema("string", "Alias or alternate phrase that can satisfy the concept.")),
		"required":  scalarSchema("boolean", "Whether missing this concept should penalize evidence."),
	}, "key", "terms", "required")
}

func researchCoverageSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"evidence_count":      scalarSchema("integer", "Number of evidence rows returned."),
		"by_kind":             arraySchema(researchBucketSchema()),
		"by_source_type":      arraySchema(researchBucketSchema()),
		"top_user_tags":       arraySchema(researchBucketSchema()),
		"exact_tag_matches":   arraySchema(researchBucketSchema()),
		"item_text_matches":   scalarSchema("integer", "Total item rows matching the topic/text phrase."),
		"source_text_matches": scalarSchema("integer", "Total source rows matching the topic/text phrase."),
		"topic_node_count":    scalarSchema("integer", "Number of nodes included in the attached topic brief."),
		"topic_edge_count":    scalarSchema("integer", "Number of edges included in the attached topic brief."),
		"displayed_limit":     scalarSchema("integer", "Requested evidence limit for this pack."),
		"related_limit":       scalarSchema("integer", "Requested related-evidence limit for this pack."),
		"recall_note":         scalarSchema("string", "Human-readable reminder that returned evidence is capped and how much matching corpus exists."),
	}, "evidence_count", "by_kind", "by_source_type")
}

func researchBucketSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":   scalarSchema("string", "Bucket key."),
		"count": scalarSchema("integer", "Bucket count."),
	}, "key", "count")
}

func researchNextStepSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"action": scalarSchema("string", "Semantic action identifier, such as inspect_top_evidence or expand_related."),
		"label":  scalarSchema("string", "Human-readable action label."),
		"reason": scalarSchema("string", "Why this follow-up helps."),
		"params": genericObjectSchema("Suggested action parameters."),
	}, "action", "label")
}
