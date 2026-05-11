package mcpserver

func whatsNewOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"cursor":               scalarSchema("string", "Requested lower-bound cursor."),
		"next_cursor":          scalarSchema("string", "Cursor to use for the next poll."),
		"high_watermark":       scalarSchema("string", "Maximum event timestamp included."),
		"high_watermark_local": scalarSchema("string", "Local display form of the high watermark."),
		"high_watermark_age":   scalarSchema("string", "Relative display form of the high watermark."),
		"truncated":            scalarSchema("boolean", "Whether more events remain after the returned page."),
		"counts":               genericObjectSchema("Counts by event kind, source type, and status."),
		"events":               arraySchema(whatsNewEventSchema()),
	})
}

func whatsNewEventSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"event_id":       scalarSchema("string", "Stable event id."),
		"event_kind":     scalarSchema("string", "Normalized event kind."),
		"event_at":       scalarSchema("string", "UTC event timestamp."),
		"event_at_local": scalarSchema("string", "Local display form of event timestamp."),
		"event_age":      scalarSchema("string", "Relative event age."),
		"entity_kind":    scalarSchema("string", "item or source."),
		"entity_id":      scalarSchema("integer", "Internal row id."),
		"entity_key":     scalarSchema("string", "Stable source key."),
		"event_stage":    scalarSchema("string", "Pipeline stage that produced the event."),
		"source_type":    scalarSchema("string", "Underlying source type."),
		"title":          scalarSchema("string", "Best available title."),
		"url":            scalarSchema("string", "Canonical URL."),
		"note_path":      scalarSchema("string", "Rendered note path."),
		"summary":        scalarSchema("string", "Derived summary text when available."),
		"tags":           arraySchema(scalarSchema("string", "User tag.")),
		"status":         scalarSchema("string", "Pipeline status."),
		"actionability":  scalarSchema("string", "review, background, blocked, or failure."),
		"importance":     scalarSchema("integer", "Deterministic priority hint."),
		"reason":         scalarSchema("string", "Short reason the event is reviewable."),
		"message":        scalarSchema("string", "Failure or blocked diagnostic when available."),
	})
}
