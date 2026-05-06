package mcpserver

func countOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"group_by": scalarSchema("string", "Grouping applied to the count buckets."),
		"total":    scalarSchema("integer", "Total count across buckets."),
		"buckets":  arraySchema(countBucketSchema()),
	}, "group_by", "total", "buckets")
}

func activityOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"now":                          scalarSchema("string", "Current UTC time in RFC3339 format."),
		"window":                       scalarSchema("string", "Lookback window duration string."),
		"latest_item_updated_at":       scalarSchema("string", "Latest item update timestamp."),
		"latest_source_updated_at":     scalarSchema("string", "Latest source update timestamp."),
		"latest_source_summary_at":     scalarSchema("string", "Latest source summary timestamp."),
		"items_updated_in_window":      scalarSchema("integer", "Number of items updated in the window."),
		"sources_updated_in_window":    scalarSchema("integer", "Number of sources updated in the window."),
		"sources_summarized_in_window": scalarSchema("integer", "Number of sources summarized in the window."),
	}, "now", "window", "items_updated_in_window", "sources_updated_in_window", "sources_summarized_in_window")
}

func backlogOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"x_hydration_pending":               scalarSchema("integer", "Pending X hydration items."),
		"link_discovery_pending":            scalarSchema("integer", "Pending link discovery items."),
		"source_extraction_pending":         scalarSchema("integer", "Pending source extraction rows."),
		"source_summary_pending":            scalarSchema("integer", "Pending source summary rows."),
		"source_extraction_pending_by_type": arraySchema(countBucketSchema()),
		"source_summary_pending_by_type":    arraySchema(countBucketSchema()),
		"drained":                           scalarSchema("boolean", "Whether all current queues are empty."),
	}, "x_hydration_pending", "link_discovery_pending", "source_extraction_pending", "source_summary_pending", "drained")
}

func countBucketSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":   scalarSchema("string", "Bucket key."),
		"count": scalarSchema("integer", "Bucket count."),
	}, "key", "count")
}
