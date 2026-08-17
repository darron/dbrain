package notify

import "strings"

type FailureDefinition struct {
	Type      FailureType
	ErrorCode string
	Title     string
	Action    string
}

const (
	FailureConfigResolution     FailureType = "sync.config_resolution.failed"
	FailureMetricsOpen          FailureType = "sync.metrics.open.failed"
	FailureMetricsClose         FailureType = "sync.metrics.close.failed"
	FailureStoreOpen            FailureType = "sync.store.open.failed"
	FailureStoreClose           FailureType = "sync.store.close.failed"
	FailureOptions              FailureType = "sync.options.failed"
	FailureOutput               FailureType = "sync.output.failed"
	FailureAppleNotesPermission FailureType = "sync.apple_notes.permission_denied"
	FailureUnknown              FailureType = "sync.unknown"
)

var knownSyncStages = []string{
	"apple_notes",
	"safari_tabs",
	"bluesky_bookmarks",
	"mastodon_bookmarks",
	"x_frontier",
	"x_media",
	"x_photo_ocr",
	"github",
	"youtube",
	"feeds",
	"sources",
	"categorize",
	"media_archive",
	"okf_export",
}

var semanticDefinitions = []FailureDefinition{
	semanticDefinition("semantic_backend_broken", "Semantic index backend is unavailable", "Repair or reinstall the configured semantic index backend, then rerun the sync."),
	semanticDefinition("semantic_run_conflict", "Semantic refresh run conflict", "Wait for the active semantic refresh to settle, then rerun the sync."),
	semanticDefinition("semantic_projection_failed", "Semantic projection failed", "Inspect the local semantic refresh status, repair the projection failure, then rerun the sync."),
	semanticDefinition("semantic_embedding_failed", "Semantic embedding failed", "Restore the configured embedding provider, then rerun the sync."),
	semanticDefinition("semantic_embedding_circuit_open", "Semantic embedding circuit is open", "Restore the configured embedding provider and wait for the circuit to rearm, then rerun the sync."),
	semanticDefinition("semantic_flush_failed", "Semantic index flush failed", "Check local storage and semantic index health, then rerun the sync."),
	semanticDefinition("semantic_compaction_failed", "Semantic index compaction failed", "Check local storage and semantic index health, then rerun the sync."),
	semanticDefinition("semantic_verify_failed", "Semantic index verification failed", "Inspect semantic readiness and repair the invalid index state before rerunning the sync."),
	semanticDefinition("semantic_native_root_failed", "Semantic native index root failed", "Repair the configured native index root, then rerun the sync."),
	semanticDefinition("semantic_readiness_not_ready", "Semantic index is not ready", "Inspect semantic readiness debt and repair blocked work before rerunning the sync."),
	semanticDefinition("semantic_lock_unavailable", "Semantic refresh lock is unavailable", "Wait for the current semantic writer to finish, then rerun the sync."),
}

var failureCatalog = buildFailureCatalog()
var orderedFailureTypes = buildOrderedFailureTypes()

func buildFailureCatalog() map[FailureType]FailureDefinition {
	definitions := []FailureDefinition{
		{FailureConfigResolution, "sync_config_resolution_failed", "Scheduled sync configuration is invalid", "Correct the scheduled sync configuration, then restart the service."},
		{FailureMetricsOpen, "sync_metrics_open_failed", "Scheduled sync metrics could not be opened", "Check the private log directory permissions and available storage, then restart the service."},
		{FailureMetricsClose, "sync_metrics_close_failed", "Scheduled sync metrics could not be finalized", "Check the private log directory and storage health, then rerun the sync."},
		{FailureStoreOpen, "sync_store_open_failed", "dbrain store could not be opened", "Check the configured database path, permissions, and database health, then restart the service."},
		{FailureStoreClose, "sync_store_close_failed", "dbrain store could not be finalized", "Check database and storage health before rerunning the sync."},
		{FailureOptions, "sync_options_failed", "Scheduled sync preflight failed", "Correct the reported local provider or credential configuration, then restart the service."},
		{FailureOutput, "sync_output_failed", "Scheduled sync output failed", "Check the service log destination and local storage, then restart the service."},
		{FailureAppleNotesPermission, "apple_notes_permission_denied", "Apple Notes permission denied", "Grant Full Disk Access to the installed dbrain service binary, then restart the service."},
	}
	for _, stage := range knownSyncStages {
		definitions = append(definitions, FailureDefinition{
			Type:      FailureType("sync.stage." + stage + ".failed"),
			ErrorCode: "sync_stage_" + stage + "_failed",
			Title:     stageTitle(stage) + " sync stage failed",
			Action:    "Inspect the local dbrain service logs for this stage, correct the local failure, then rerun the sync.",
		})
	}
	definitions = append(definitions, semanticDefinitions...)
	definitions = append(definitions, FailureDefinition{
		Type:      FailureUnknown,
		ErrorCode: "sync_unknown",
		Title:     "Scheduled sync failed",
		Action:    "Inspect the local dbrain service logs, correct the failure, then rerun the sync.",
	})
	catalog := make(map[FailureType]FailureDefinition, len(definitions))
	for _, definition := range definitions {
		catalog[definition.Type] = definition
	}
	return catalog
}

func buildOrderedFailureTypes() []FailureType {
	ordered := []FailureType{
		FailureConfigResolution,
		FailureMetricsOpen,
		FailureMetricsClose,
		FailureStoreOpen,
		FailureStoreClose,
		FailureOptions,
		FailureOutput,
		FailureAppleNotesPermission,
	}
	for _, stage := range knownSyncStages {
		ordered = append(ordered, FailureType("sync.stage."+stage+".failed"))
	}
	for _, definition := range semanticDefinitions {
		ordered = append(ordered, definition.Type)
	}
	return append(ordered, FailureUnknown)
}

func semanticDefinition(code string, title string, action string) FailureDefinition {
	return FailureDefinition{Type: FailureType("sync.semantic." + code), ErrorCode: code, Title: title, Action: action}
}

func stageTitle(stage string) string {
	words := strings.Split(stage, "_")
	for index, word := range words {
		if word != "" {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func LookupFailure(failureType FailureType) (FailureDefinition, bool) {
	definition, ok := failureCatalog[failureType]
	return definition, ok
}

func KnownFailureTypes() []FailureType {
	return append([]FailureType(nil), orderedFailureTypes...)
}

func LookupStageFailure(stage string) (FailureType, bool) {
	for _, known := range knownSyncStages {
		if stage == known {
			return FailureType("sync.stage." + stage + ".failed"), true
		}
	}
	return "", false
}

func LookupSemanticFailure(code string) (FailureType, bool) {
	failureType := FailureType("sync.semantic." + code)
	_, ok := failureCatalog[failureType]
	return failureType, ok
}
