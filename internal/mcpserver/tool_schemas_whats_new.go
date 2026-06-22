package mcpserver

func whatsNewOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"view":           scalarSchema("string", "Output view: events or entities."),
		"cursor":         reviewCursorOutputSchema(),
		"next_cursor":    scalarSchema("string", "Continuation cursor for the next request."),
		"high_watermark": scalarSchema("string", "Timestamp of the newest event returned."),
		"events":         arraySchema(reviewEventOutputSchema()),
		"entities":       arraySchema(reviewEntityOutputSchema()),
		"truncated":      scalarSchema("boolean", "Whether more events remain after this page."),
		"counts":         arraySchema(countBucketSchema()),
	}, "view", "cursor", "next_cursor", "high_watermark", "events", "entities", "truncated", "counts")
}

func reviewCursorOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"event_at":    scalarSchema("string", "Cursor timestamp."),
		"event_kind":  scalarSchema("string", "Cursor event kind."),
		"entity_kind": scalarSchema("string", "Cursor entity kind."),
		"entity_id":   scalarSchema("integer", "Cursor entity id."),
		"event_stage": scalarSchema("string", "Cursor event stage."),
	}, "event_at", "event_kind", "entity_kind", "entity_id", "event_stage")
}

func reviewEventOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"event_id":      scalarSchema("string", "Stable event identifier."),
		"event_kind":    scalarSchema("string", "Event kind."),
		"event_at":      scalarSchema("string", "UTC event timestamp."),
		"entity_kind":   scalarSchema("string", "item or source."),
		"entity_id":     scalarSchema("integer", "Local entity id."),
		"entity_key":    scalarSchema("string", "Local source key."),
		"event_stage":   scalarSchema("string", "Pipeline stage represented by the event."),
		"source_type":   scalarSchema("string", "Item or source type."),
		"title":         scalarSchema("string", "Best available title."),
		"url":           scalarSchema("string", "Best available URL."),
		"note_path":     scalarSchema("string", "Rendered note path relative to the vault."),
		"summary":       scalarSchema("string", "Short event summary."),
		"tags":          arraySchema(scalarSchema("string", "Tag.")),
		"status":        scalarSchema("string", "Current pipeline status."),
		"message":       scalarSchema("string", "Failure or blocked diagnostic, when present."),
		"actionability": scalarSchema("string", "review, background, blocked, or failure."),
		"importance":    scalarSchema("integer", "Review importance score."),
		"reasons":       arraySchema(scalarSchema("string", "Reason contributing to actionability or importance.")),
	}, "event_id", "event_kind", "event_at", "entity_kind", "entity_id", "entity_key", "event_stage", "source_type", "title", "url", "note_path", "summary", "tags", "status", "actionability", "importance", "reasons")
}

func reviewEntityOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"entity_kind":        scalarSchema("string", "item or source."),
		"entity_id":          scalarSchema("integer", "Local entity id."),
		"entity_key":         scalarSchema("string", "Local source key."),
		"source_type":        scalarSchema("string", "Item or source type."),
		"title":              scalarSchema("string", "Best available title."),
		"url":                scalarSchema("string", "Best available URL."),
		"note_path":          scalarSchema("string", "Rendered note path relative to the vault."),
		"first_event_at":     scalarSchema("string", "Earliest event timestamp in this page for the entity."),
		"latest_event_at":    scalarSchema("string", "Latest event timestamp in this page for the entity."),
		"event_count":        scalarSchema("integer", "Number of review events collapsed into this entity group."),
		"event_kinds":        arraySchema(scalarSchema("string", "Collapsed event kind.")),
		"summary":            scalarSchema("string", "Preferred compact summary/excerpt, choosing item/source summaries before raw transcripts or OCR."),
		"summary_event_id":   scalarSchema("string", "Event id that supplied the preferred summary."),
		"summary_event_kind": scalarSchema("string", "Event kind that supplied the preferred summary."),
		"tags":               arraySchema(scalarSchema("string", "Tag.")),
		"status":             scalarSchema("string", "Latest pipeline status in this page."),
		"message":            scalarSchema("string", "Failure or blocked diagnostic, when present."),
		"actionability":      scalarSchema("string", "review, background, blocked, or failure."),
		"importance":         scalarSchema("integer", "Maximum review importance score across collapsed events."),
		"reasons":            arraySchema(scalarSchema("string", "Aggregated reason contributing to actionability or importance.")),
		"events":             arraySchema(reviewEntityEventOutputSchema()),
	}, "entity_kind", "entity_id", "entity_key", "source_type", "title", "url", "note_path", "first_event_at", "latest_event_at", "event_count", "event_kinds", "summary", "summary_event_id", "summary_event_kind", "tags", "status", "actionability", "importance", "reasons", "events")
}

func reviewEntityEventOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"event_id":      scalarSchema("string", "Stable event identifier."),
		"event_kind":    scalarSchema("string", "Event kind."),
		"event_at":      scalarSchema("string", "UTC event timestamp."),
		"event_stage":   scalarSchema("string", "Pipeline stage represented by the event."),
		"status":        scalarSchema("string", "Current pipeline status."),
		"actionability": scalarSchema("string", "review, background, blocked, or failure."),
		"importance":    scalarSchema("integer", "Review importance score."),
		"message":       scalarSchema("string", "Failure or blocked diagnostic, when present."),
		"reasons":       arraySchema(scalarSchema("string", "Reason contributing to actionability or importance.")),
	}, "event_id", "event_kind", "event_at", "event_stage", "status", "actionability", "importance", "reasons")
}
