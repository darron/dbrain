package audit

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	MaxDailyEvidence  = 366
	MaxByKindEvidence = 64
	MaxMissingFields  = 32
)

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

var enumValues = map[string]map[string]bool{
	"layout":                    {"explicit_config": true, "explicit_root": true, "xdg": true},
	"config_source":             {"flag": true, "environment": true, "default": true},
	"git_status":                {"clean": true, "dirty": true, "unknown": true},
	"compatibility":             {"current_compatible": true, "legacy_compatible": true, "incompatible": true},
	"schema_compatibility":      {"current_compatible": true, "legacy_compatible": true, "incompatible": true},
	"migration_compatibility":   {"current_compatible": true, "legacy_compatible": true, "incompatible": true},
	"duration_allowance_source": {"p95": true, "max_observed": true, "none": true},
	"result":                    {"ok": true, "violation": true},
	"quick_check":               {"ok": true, "violation": true},
	"sample_mode":               {"complete": true, "bounded_sample": true, "full_inventory": true},
	"archive_authenticity":      {"unverified": true},
	"configuration_state":       {"not_configured": true, "configured_disabled": true, "required_ready": true, "required_missing_provider": true, "required_missing_credential": true, "resolution_error": true},
	"baseline_id":               {"pre-v0.6.0": true, "v0.6.0-security-pass": true},
	"kind":                      {"item": true, "source": true, "x_bookmark": true, "x_quote": true, "x_photo": true, "x_video": true, "x_animated_gif": true, "apple_note": true, "safari_tab": true, "feed": true, "github_star": true, "youtube_liked": true, "youtube_watch_later": true},
}

var missingFieldNames = map[string]bool{
	"raw_json": true, "model": true, "prompt_version": true, "tool": true, "tool_version": true,
	"input_hash": true, "completed_at": true, "summary_json": true, "summary_model": true,
	"summary_prompt_version": true, "summary_tool": true, "summary_tool_version": true,
	"content_hash": true, "summarized_at": true,
}

func ValidateEvidence(id CheckID, evidence Evidence) error {
	entry, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("unknown check id %q", id)
	}
	if evidence == nil {
		return fmt.Errorf("evidence must not be null")
	}
	for key, value := range evidence {
		kind, ok := entry.EvidenceFields[key]
		if !ok {
			return fmt.Errorf("evidence key %q is not declared for %s", key, id)
		}
		if value == nil {
			return fmt.Errorf("evidence key %q must be omitted rather than null", key)
		}
		if err := validateEvidenceValue(key, kind, value); err != nil {
			return fmt.Errorf("evidence key %q: %w", key, err)
		}
	}
	return nil
}

func validateEvidenceValue(key string, kind EvidenceKind, value any) error {
	switch kind {
	case EvidenceInteger:
		if !nonnegativeInteger(value) {
			return fmt.Errorf("must be a non-negative integer")
		}
	case EvidenceBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be boolean")
		}
	case EvidenceTimestamp:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be UTC RFC3339 string")
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil || parsed.Location() != time.UTC || !strings.HasSuffix(text, "Z") {
			return fmt.Errorf("must be UTC RFC3339 string")
		}
	case EvidenceEnum:
		text, ok := value.(string)
		if !ok || !enumValues[key][text] {
			return fmt.Errorf("must be a closed enum")
		}
	case EvidenceDaily:
		return validateDaily(value)
	case EvidenceByKind:
		return validateByKind(value)
	case EvidenceMissingByField:
		return validateMissingByField(value)
	default:
		return fmt.Errorf("unsupported evidence kind %q", kind)
	}
	return nil
}

func nonnegativeInteger(value any) bool {
	switch n := value.(type) {
	case int:
		return n >= 0
	case int8:
		return n >= 0
	case int16:
		return n >= 0
	case int32:
		return n >= 0
	case int64:
		return n >= 0
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return n >= 0 && !math.IsInf(n, 0) && !math.IsNaN(n) && n == math.Trunc(n)
	case json.Number:
		parsed, err := n.Int64()
		return err == nil && parsed >= 0
	default:
		return false
	}
}

func objectSlice(value any) ([]map[string]any, bool) {
	switch rows := value.(type) {
	case []map[string]any:
		return rows, true
	case []any:
		out := make([]map[string]any, len(rows))
		for i, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				return nil, false
			}
			out[i] = row
		}
		return out, true
	default:
		return nil, false
	}
}

func validateDaily(value any) error {
	rows, ok := objectSlice(value)
	if !ok || len(rows) > MaxDailyEvidence {
		return fmt.Errorf("must be at most %d declared daily aggregates", MaxDailyEvidence)
	}
	allowed := map[string]bool{"day": true, "created": true, "updated": true, "unchanged": true, "skipped": true, "linked": true, "blocked": true, "failed": true}
	for _, row := range rows {
		if len(row) != len(allowed) {
			return fmt.Errorf("daily aggregate must use exact declared fields")
		}
		for key, item := range row {
			if !allowed[key] {
				return fmt.Errorf("undeclared daily field %q", key)
			}
			if key == "day" {
				text, ok := item.(string)
				if !ok || !dayPattern.MatchString(text) {
					return fmt.Errorf("invalid UTC day")
				}
				continue
			}
			if !nonnegativeInteger(item) {
				return fmt.Errorf("daily count %q must be a non-negative integer", key)
			}
		}
	}
	return nil
}

func validateByKind(value any) error {
	rows, ok := objectSlice(value)
	if !ok || len(rows) > MaxByKindEvidence {
		return fmt.Errorf("must be at most %d declared by-kind aggregates", MaxByKindEvidence)
	}
	allowed := map[string]bool{"kind": true, "total": true, "current": true, "pending": true, "blocked": true, "terminal": true, "failed": true, "unknown": true, "partition_valid": true}
	for _, row := range rows {
		if len(row) != len(allowed) {
			return fmt.Errorf("by-kind aggregate must use exact declared fields")
		}
		for key, item := range row {
			if !allowed[key] {
				return fmt.Errorf("undeclared by-kind field %q", key)
			}
			if key == "kind" {
				if err := validateEvidenceValue("kind", EvidenceEnum, item); err != nil {
					return err
				}
				continue
			}
			if key == "partition_valid" {
				if _, ok := item.(bool); !ok {
					return fmt.Errorf("partition_valid must be boolean")
				}
				continue
			}
			if !nonnegativeInteger(item) {
				return fmt.Errorf("by-kind count %q must be non-negative integer", key)
			}
		}
	}
	return nil
}

func validateMissingByField(value any) error {
	row, ok := value.(map[string]int)
	if ok {
		converted := make(map[string]any, len(row))
		for key, count := range row {
			converted[key] = count
		}
		return validateMissingMap(converted)
	}
	generic, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("must be a declared field-count object")
	}
	return validateMissingMap(generic)
}

func validateMissingMap(row map[string]any) error {
	if len(row) > MaxMissingFields {
		return fmt.Errorf("too many missing-field counts")
	}
	for key, count := range row {
		if !missingFieldNames[key] {
			return fmt.Errorf("undeclared provenance field %q", key)
		}
		if !nonnegativeInteger(count) {
			return fmt.Errorf("missing count must be non-negative integer")
		}
	}
	return nil
}
