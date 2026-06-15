package mcpserver

func okfSearchOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"bundle":  scalarSchema("string", "Configured OKF bundle directory searched."),
		"query":   scalarSchema("string", "Search query used."),
		"count":   scalarSchema("integer", "Number of OKF concepts returned."),
		"results": arraySchema(okfSearchResultSchema()),
	}, "bundle", "count", "results")
}

func okfGetOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"bundle":             scalarSchema("string", "Configured OKF bundle directory read."),
		"include_markdown":   scalarSchema("boolean", "Whether markdown was included in the response."),
		"max_chars":          scalarSchema("integer", "Maximum body/markdown characters requested after caps."),
		"body_truncated":     scalarSchema("boolean", "Whether concept body was truncated."),
		"markdown_truncated": scalarSchema("boolean", "Whether concept markdown was truncated."),
		"concept":            okfConceptSchema(),
	}, "bundle", "concept")
}

func okfSearchResultSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"path":               scalarSchema("string", "Relative OKF concept path."),
		"type":               scalarSchema("string", "OKF concept type."),
		"title":              scalarSchema("string", "Concept title."),
		"description":        scalarSchema("string", "Concept description."),
		"dbrain_concept_id":  scalarSchema("string", "Stable dbrain producer extension id."),
		"dbrain_source_key":  scalarSchema("string", "Underlying dbrain source key when applicable."),
		"dbrain_source_type": scalarSchema("string", "Underlying dbrain source type or derived kind."),
		"snippet":            scalarSchema("string", "Search snippet from the concept document."),
	}, "path", "type", "title", "dbrain_concept_id")
}

func okfConceptSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"path":               scalarSchema("string", "Relative OKF concept path."),
		"type":               scalarSchema("string", "OKF concept type."),
		"title":              scalarSchema("string", "Concept title."),
		"description":        scalarSchema("string", "Concept description."),
		"dbrain_concept_id":  scalarSchema("string", "Stable dbrain producer extension id."),
		"dbrain_source_key":  scalarSchema("string", "Underlying dbrain source key when applicable."),
		"dbrain_source_type": scalarSchema("string", "Underlying dbrain source type or derived kind."),
		"frontmatter":        genericObjectSchema("Parsed OKF YAML frontmatter."),
		"body":               scalarSchema("string", "Markdown body without frontmatter, possibly truncated."),
		"markdown":           scalarSchema("string", "Full rendered Markdown when requested, possibly truncated."),
	}, "path", "type", "title", "dbrain_concept_id", "frontmatter", "body")
}
