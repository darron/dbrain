package mcpserver

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"

	"github.com/darron/dbrain/internal/audit"
)

func auditInputSchema() map[string]interface{} {
	profile := enumSchema("Audit profile. fast runs bounded local checks; standard reads the newest persisted exact-profile report.", "fast", "standard")
	profile["default"] = "fast"
	return map[string]interface{}{
		"type":                 "object",
		"properties":           map[string]interface{}{"profile": profile},
		"additionalProperties": false,
	}
}

func auditOutputSchema() map[string]interface{} {
	return closedObjectSchema(map[string]interface{}{
		"report": map[string]interface{}{"type": []interface{}{"object", "null"}, "properties": auditReportProperties(), "required": auditReportRequired(), "additionalProperties": false},
		"freshness": closedObjectSchema(map[string]interface{}{
			"status":           enumSchema("Freshness status.", "current", "unknown"),
			"reason":           enumSchema("Reason freshness is unknown.", "not_found", "stale"),
			"age_seconds":      nonnegativeIntegerSchema(),
			"deadline_seconds": nonnegativeIntegerSchema(),
		}, "status", "deadline_seconds"),
	}, "report", "freshness")
}

func auditReportProperties() map[string]interface{} {
	return map[string]interface{}{
		"schema":       enumSchema("Stable audit schema.", audit.SchemaV1, audit.SchemaV2),
		"audit_id":     scalarSchema("string", "Opaque audit run identifier."),
		"profile":      enumSchema("Exact audit profile.", "fast", "standard"),
		"scope":        auditScopeSchema(),
		"started_at":   scalarSchema("string", "UTC RFC3339 timestamp."),
		"completed_at": scalarSchema("string", "UTC RFC3339 timestamp."),
		"status":       auditStatusSchema(false),
		"confidence":   auditConfidenceSchema(),
		"boundary":     auditBoundarySchema(),
		"summary":      auditSummarySchema(),
		"checks":       arraySchema(auditCheckSchema()),
	}
}

func auditReportRequired() []string {
	return []string{"schema", "audit_id", "profile", "scope", "started_at", "completed_at", "status", "confidence", "boundary", "summary", "checks"}
}

func auditScopeSchema() map[string]interface{} {
	return closedObjectSchema(map[string]interface{}{
		"categories":   arraySchema(enumSchema("Audit category.", "boundary", "scheduler", "imports", "pipeline", "semantic", "durability")),
		"sources":      arraySchema(enumSchema("Configured import source.", "apple-notes", "feeds", "github-stars", "safari-tabs", "x-bookmarks", "youtube-liked", "youtube-watch-later")),
		"check_ids":    arraySchema(scalarSchema("string", "Stable check identifier.")),
		"filtered":     scalarSchema("boolean", "Whether report scope was filtered."),
		"whole_system": scalarSchema("boolean", "Whether report covers the complete registry."),
	}, "categories", "sources", "check_ids", "filtered", "whole_system")
}

func auditBoundarySchema() map[string]interface{} {
	return closedObjectSchema(map[string]interface{}{
		"layout":                  enumSchema("Verified runtime layout.", "explicit_config", "explicit_root", "xdg"),
		"config_verified":         scalarSchema("boolean", "Whether configuration was verified."),
		"database_verified":       scalarSchema("boolean", "Whether database access was query-only."),
		"version":                 scalarSchema("string", "Content-free build version."),
		"commit":                  scalarSchema("string", "Content-free build commit."),
		"git_status":              enumSchema("Build tree status.", "clean", "dirty", "unknown"),
		"platform":                scalarSchema("string", "Build platform."),
		"security_baseline":       scalarSchema("string", "Security baseline identifier."),
		"security_baseline_epoch": nonnegativeIntegerSchema(),
		"schema_version":          nonnegativeIntegerSchema(),
		"schema_compatibility":    scalarSchema("string", "Database schema compatibility."),
	}, "layout", "config_verified", "database_verified", "version", "commit", "git_status", "platform", "security_baseline", "security_baseline_epoch", "schema_version", "schema_compatibility")
}

func auditSummarySchema() map[string]interface{} {
	all := closedObjectSchema(map[string]interface{}{
		"pass": nonnegativeIntegerSchema(), "warn": nonnegativeIntegerSchema(), "fail": nonnegativeIntegerSchema(),
		"unknown": nonnegativeIntegerSchema(), "skipped": nonnegativeIntegerSchema(),
	}, "pass", "warn", "fail", "unknown", "skipped")
	required := closedObjectSchema(map[string]interface{}{
		"pass": nonnegativeIntegerSchema(), "warn": nonnegativeIntegerSchema(), "fail": nonnegativeIntegerSchema(), "unknown": nonnegativeIntegerSchema(),
	}, "pass", "warn", "fail", "unknown")
	return closedObjectSchema(map[string]interface{}{"all": all, "required": required}, "all", "required")
}

func auditCheckSchema() map[string]interface{} {
	return closedObjectSchema(map[string]interface{}{
		"id":          scalarSchema("string", "Stable registry check identifier."),
		"category":    enumSchema("Audit category.", "boundary", "scheduler", "imports", "pipeline", "semantic", "durability"),
		"status":      auditStatusSchema(true),
		"confidence":  auditConfidenceSchema(),
		"required":    scalarSchema("boolean", "Whether check contributes to required health."),
		"summary":     scalarSchema("string", "Fixed content-free registry summary."),
		"observed_at": scalarSchema("string", "UTC RFC3339 timestamp."),
		"threshold": closedObjectSchema(map[string]interface{}{
			"warn_after_seconds": nonnegativeIntegerSchema(),
			"fail_after_seconds": nonnegativeIntegerSchema(),
		}),
		"evidence":    auditEvidenceSchema(),
		"remediation": scalarSchema("string", "Fixed content-free remediation."),
		"skip_reason": enumSchema("Why the check was skipped.", "profile_excluded", "feature_disabled"),
		"error_code": enumSchema("Sanitized check error.",
			"unavailable", "timeout", "canceled", "interrupted", "read_error", "parse_error", "budget_exhausted",
			"configuration_error", "credential_resolution_error", "destination_rejected", "listing_incomplete", "manifest_error", "database_error"),
	}, "id", "category", "status", "confidence", "required", "summary", "observed_at", "evidence")
}

func auditEvidenceSchema() map[string]interface{} {
	properties := map[string]interface{}{}
	for _, entry := range audit.Registry() {
		for name, kind := range entry.EvidenceFields {
			if _, exists := properties[name]; exists {
				continue
			}
			properties[name] = auditEvidenceValueSchema(name, kind)
		}
	}
	return closedObjectSchema(properties)
}

func auditEvidenceValueSchema(name string, kind audit.EvidenceKind) map[string]interface{} {
	switch kind {
	case audit.EvidenceInteger:
		return nonnegativeIntegerSchema()
	case audit.EvidenceBoolean:
		return scalarSchema("boolean", "")
	case audit.EvidenceTimestamp:
		return scalarSchema("string", "")
	case audit.EvidenceEnum:
		if values, ok := semanticEvidenceEnums[name]; ok {
			return enumSchema("Closed semantic audit value.", values...)
		}
		return scalarSchema("string", "")
	case audit.EvidenceIdentifier:
		if name == "profile_id" {
			return map[string]interface{}{"type": "string", "maxLength": 85, "pattern": `^embedding-profile-v1:[0-9a-f]{64}$`}
		}
		return map[string]interface{}{"type": "string", "maxLength": 128, "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`}
	case audit.EvidenceSemanticStages:
		stages := []string{"projection", "embedding", "flush", "compaction", "verification", "readiness"}
		prefix := make([]interface{}, 0, len(stages))
		for _, stage := range stages {
			prefix = append(prefix, closedObjectSchema(map[string]interface{}{
				"stage":            enumSchema("Fixed semantic refresh stage.", stage),
				"status":           enumSchema("Closed semantic stage outcome.", "succeeded", "failed", "canceled", "skipped", "unknown"),
				"duration_seconds": nonnegativeIntegerSchema(),
			}, "stage", "status", "duration_seconds"))
		}
		return map[string]interface{}{"type": "array", "minItems": len(stages), "maxItems": len(stages), "prefixItems": prefix, "items": false}
	case audit.EvidenceDaily:
		return arraySchema(closedObjectSchema(map[string]interface{}{
			"day": scalarSchema("string", "UTC day."), "created": nonnegativeIntegerSchema(), "updated": nonnegativeIntegerSchema(),
			"unchanged": nonnegativeIntegerSchema(), "skipped": nonnegativeIntegerSchema(), "linked": nonnegativeIntegerSchema(),
			"blocked": nonnegativeIntegerSchema(), "failed": nonnegativeIntegerSchema(),
		}, "day", "created", "updated", "unchanged", "skipped", "linked", "blocked", "failed"))
	case audit.EvidenceByKind:
		return arraySchema(closedObjectSchema(map[string]interface{}{
			"kind": scalarSchema("string", ""), "total": nonnegativeIntegerSchema(), "current": nonnegativeIntegerSchema(),
			"pending": nonnegativeIntegerSchema(), "blocked": nonnegativeIntegerSchema(), "terminal": nonnegativeIntegerSchema(),
			"failed": nonnegativeIntegerSchema(), "unknown": nonnegativeIntegerSchema(), "partition_valid": scalarSchema("boolean", ""),
		}, "kind", "total", "current", "pending", "blocked", "terminal", "failed", "unknown", "partition_valid"))
	case audit.EvidenceMissingByField:
		return map[string]interface{}{"type": "object", "additionalProperties": nonnegativeIntegerSchema()}
	default:
		return map[string]interface{}{"type": "null"}
	}
}

var semanticEvidenceEnums = map[string][]string{
	"capability":          {"available", "disabled", "unsupported", "unavailable"},
	"backend":             {"ollama", "none", "unsupported"},
	"readiness":           {"ready", "catching_up", "needs_projection", "needs_embeddings", "needs_index", "retry_scheduled", "building", "stale", "degraded_blocked", "corrupt", "disabled", "unavailable"},
	"refresh_state":       {"succeeded", "failed", "canceled", "running", "skipped", "unsupported", "unknown"},
	"semantic_error_code": {"semantic_backend_broken", "semantic_run_conflict", "semantic_projection_failed", "semantic_embedding_failed", "semantic_embedding_circuit_open", "semantic_flush_failed", "semantic_compaction_failed", "semantic_verify_failed", "semantic_native_root_failed", "semantic_readiness_not_ready", "semantic_lock_unavailable", "semantic_refresh_cancelled", "semantic_refresh_failed"},
}

func auditStatusSchema(includeSkipped bool) map[string]interface{} {
	values := []string{"pass", "warn", "fail", "unknown"}
	if includeSkipped {
		values = append(values, "skipped")
	}
	return enumSchema("Audit status.", values...)
}

func auditConfidenceSchema() map[string]interface{} {
	return enumSchema("Audit confidence.", "high", "moderate", "low", "unknown")
}

func nonnegativeIntegerSchema() map[string]interface{} {
	return map[string]interface{}{"type": "integer", "minimum": 0}
}

func closedObjectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	schema := objectSchema(properties, required...)
	schema["additionalProperties"] = false
	return schema
}

func validateJSONSchemaValue(schema map[string]interface{}, value interface{}) error {
	if !schemaTypeMatches(schema["type"], value) {
		return fmt.Errorf("type %T does not match %v", value, schema["type"])
	}
	if value == nil {
		return nil
	}
	if text, ok := value.(string); ok {
		if max, ok := schema["maxLength"].(int); ok && len(text) > max {
			return fmt.Errorf("string exceeds max length")
		}
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(text) {
			return fmt.Errorf("string does not match pattern")
		}
	}
	if values, ok := schema["enum"].([]interface{}); ok {
		matched := false
		for _, candidate := range values {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("value %v is not in enum", value)
		}
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		properties, _ := schema["properties"].(map[string]interface{})
		required := schemaStringSet(schema["required"])
		for name := range required {
			if _, ok := typed[name]; !ok {
				return fmt.Errorf("required property %s is missing", name)
			}
		}
		for name, child := range typed {
			childSchema, declared := properties[name].(map[string]interface{})
			if !declared {
				switch additional := schema["additionalProperties"].(type) {
				case bool:
					if !additional {
						return fmt.Errorf("additional property %s is forbidden", name)
					}
					continue
				case map[string]interface{}:
					childSchema = additional
				default:
					continue
				}
			}
			if err := validateJSONSchemaValue(childSchema, child); err != nil {
				return fmt.Errorf("property %s: %w", name, err)
			}
		}
	case []interface{}:
		if min, ok := schema["minItems"].(int); ok && len(typed) < min {
			return fmt.Errorf("array has fewer than %d items", min)
		}
		if max, ok := schema["maxItems"].(int); ok && len(typed) > max {
			return fmt.Errorf("array has more than %d items", max)
		}
		if prefix, ok := schema["prefixItems"].([]interface{}); ok {
			for index, child := range typed {
				if index >= len(prefix) {
					return fmt.Errorf("item %d is forbidden", index)
				}
				if err := validateJSONSchemaValue(prefix[index].(map[string]interface{}), child); err != nil {
					return fmt.Errorf("item %d: %w", index, err)
				}
			}
			return nil
		}
		items, _ := schema["items"].(map[string]interface{})
		for index, child := range typed {
			if err := validateJSONSchemaValue(items, child); err != nil {
				return fmt.Errorf("item %d: %w", index, err)
			}
		}
	}
	return nil
}

func schemaTypeMatches(raw interface{}, value interface{}) bool {
	if values, ok := raw.([]interface{}); ok {
		for _, candidate := range values {
			if schemaTypeMatches(candidate, value) {
				return true
			}
		}
		return false
	}
	typeName, _ := raw.(string)
	switch typeName {
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number >= 0 && number == math.Trunc(number)
	case "number":
		_, ok := value.(float64)
		return ok
	case "null":
		return value == nil
	case "":
		return true
	default:
		return false
	}
}

func schemaStringSet(raw interface{}) map[string]bool {
	out := map[string]bool{}
	switch values := raw.(type) {
	case []string:
		for _, value := range values {
			out[value] = true
		}
	case []interface{}:
		for _, value := range values {
			out[strings.TrimSpace(fmt.Sprint(value))] = true
		}
	}
	return out
}
