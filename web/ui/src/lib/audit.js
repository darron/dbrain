const FINDING_ORDER = { fail: 0, unknown: 1, warn: 2 };
const DAILY_FIELDS = ["day", "created", "updated", "unchanged", "skipped", "linked", "blocked", "failed"];
const BY_KIND_FIELDS = ["kind", "total", "current", "pending", "blocked", "terminal", "failed", "unknown", "partition_valid"];
const MISSING_FIELD_NAMES = new Set(["raw_json", "model", "prompt_version", "tool", "tool_version", "input_hash", "completed_at", "summary_json", "summary_model", "summary_prompt_version", "summary_tool", "summary_tool_version", "content_hash", "summarized_at"]);
const EVIDENCE_KINDS = new Set(["item", "source", "x_bookmark", "x_quote", "x_photo", "x_video", "x_animated_gif", "apple_note", "safari_tab", "feed", "feed_entry", "github_star", "youtube_liked", "youtube_watch_later", "web", "pdf", "github", "youtube", "x_article", "x_media_transcript", "x_media_summary", "x_photo_ocr", "media_archive"]);
const INTEGER_EVIDENCE_FIELDS = new Set([
  "baseline_epoch", "minimum_epoch", "missing_table_count", "missing_column_count", "user_version", "supported_version", "applied_count", "violation_count",
  "age_seconds", "warn_after_seconds", "fail_after_seconds", "duration_allowance_seconds", "expected_stage_count", "completed_stage_count", "missing_stage_count",
  "observed_attempt_count", "gap_count", "explained_gap_count", "unexplained_gap_count", "largest_gap_seconds", "requested_seconds", "covered_seconds",
  "completed_attempt_count", "parse_error_count", "attempt_count", "success_count", "failure_count", "quiet_seconds", "total", "current", "pending", "blocked",
  "terminal", "failed", "unknown", "pending_count", "oldest_pending_age_seconds", "successful_count", "complete_count", "legacy_missing_count",
  "post_cutover_missing_count", "eligible_local_count", "uncovered_pruned_count", "orphan_count", "population_count", "checked_count", "recent_population_count",
  "recent_checked_count", "older_population_count", "older_checked_count", "missing_count", "size_mismatch_count", "invalid_timestamp_count", "archive_count",
  "latest_age_seconds", "latest_size_bytes", "document_count", "broken_link_count", "validation_error_count", "remote_only_count", "compressed_bytes",
  "decompressed_bytes", "foreign_key_violation_count", "upstream_count", "matched_local_count", "missing_local_count", "page_count"
]);
const BOOLEAN_EVIDENCE_FIELDS = new Set([
  "verified", "release_known", "commit_known", "platform_known", "expected_commit_matched", "opened_query_only", "record_complete", "latest_attempt_present",
  "latest_completed_present", "partition_valid", "inventory_complete", "capability_configured", "scheduler_enabled", "audit_required", "listing_complete", "manifest_valid",
  "traversal_complete", "cleanup_complete"
]);
const TIMESTAMP_EVIDENCE_FIELDS = new Set(["latest_attempt_at", "latest_success_at", "attempted_at", "succeeded_at", "cutover_at", "exported_at"]);
const ENUM_EVIDENCE_FIELDS = new Map([
  ["layout", new Set(["explicit_config", "explicit_root", "xdg"])], ["config_source", new Set(["flag", "environment", "default"])],
  ["git_status", new Set(["clean", "dirty", "unknown"])], ["compatibility", new Set(["current_compatible", "legacy_compatible", "incompatible"])],
  ["schema_compatibility", new Set(["current_compatible", "legacy_compatible", "incompatible"])], ["migration_compatibility", new Set(["current_compatible", "legacy_compatible", "incompatible"])],
  ["result", new Set(["ok", "violation"])], ["quick_check", new Set(["ok", "violation"])], ["duration_allowance_source", new Set(["p95", "max_observed", "none"])],
  ["baseline_id", new Set(["pre-v0.6.0", "v0.6.0-security-pass"])], ["sample_mode", new Set(["complete", "bounded_sample", "full_inventory"])],
  ["archive_authenticity", new Set(["unverified"])], ["configuration_state", new Set(["not_configured", "configured_disabled", "required_ready", "required_missing_provider", "required_missing_credential", "resolution_error"])]
]);

const SAFE_EVIDENCE_FIELDS = new Set([
  "layout", "config_source", "verified", "release_known", "commit_known", "platform_known", "git_status", "expected_commit_matched",
  "baseline_id", "baseline_epoch", "minimum_epoch", "opened_query_only", "compatibility", "schema_compatibility", "migration_compatibility",
  "missing_table_count", "missing_column_count", "user_version", "supported_version", "applied_count", "result", "quick_check", "violation_count",
  "latest_attempt_at", "latest_success_at", "age_seconds", "warn_after_seconds", "fail_after_seconds", "duration_allowance_seconds", "duration_allowance_source",
  "expected_stage_count", "completed_stage_count", "missing_stage_count", "record_complete", "observed_attempt_count", "gap_count", "explained_gap_count",
  "unexplained_gap_count", "largest_gap_seconds", "requested_seconds", "covered_seconds", "completed_attempt_count", "latest_attempt_present", "latest_completed_present",
  "parse_error_count", "attempted_at", "succeeded_at", "attempt_count", "success_count", "failure_count", "quiet_seconds", "daily",
  "total", "current", "pending", "blocked", "terminal", "failed", "unknown", "partition_valid", "by_kind", "pending_count", "oldest_pending_age_seconds",
  "successful_count", "complete_count", "legacy_missing_count", "post_cutover_missing_count", "cutover_at", "missing_by_field",
  "eligible_local_count", "uncovered_pruned_count", "orphan_count", "population_count", "checked_count", "recent_population_count", "recent_checked_count",
  "older_population_count", "older_checked_count", "missing_count", "size_mismatch_count", "invalid_timestamp_count", "sample_mode", "inventory_complete",
  "capability_configured", "scheduler_enabled", "audit_required", "configuration_state", "archive_count", "latest_age_seconds", "latest_size_bytes", "listing_complete",
  "manifest_valid", "exported_at", "document_count", "broken_link_count", "validation_error_count", "traversal_complete", "remote_only_count",
  "compressed_bytes", "decompressed_bytes", "foreign_key_violation_count", "archive_authenticity", "cleanup_complete",
  "upstream_count", "matched_local_count", "missing_local_count", "page_count"
]);

const DURABILITY = [
  ["durability.media_local_coverage", "media", "Local media"],
  ["durability.media_remote", "media", "Remote media archive"],
  ["durability.sqlite_backup_configuration", "sqlite", "SQLite archive policy"],
  ["durability.sqlite_backup_age", "sqlite", "SQLite archive age"],
  ["durability.okf_freshness", "okf", "OKF export freshness"],
  ["durability.okf_validation", "okf", "OKF validation"]
];

export function overallHealth(envelope) {
  const report = envelope?.report || null;
  if (!report) return { state: "absent", status: "unknown", reason: envelope?.freshness?.reason || "not_found", report: null };
  if (report.schema !== "dbrain.audit.v1") return { state: "unknown", status: "unknown", reason: "invalid_schema", report };
  if (report.profile !== "standard") return { state: "unknown", status: "unknown", reason: "not_standard", report };
  if (!report.scope?.whole_system || report.scope?.filtered) return { state: "unknown", status: "unknown", reason: "invalid_scope", report };
  if (envelope?.freshness?.status !== "current") {
    const reason = envelope?.freshness?.reason || "stale";
    return { state: reason === "stale" ? "stale" : "unknown", status: "unknown", reason, report };
  }
  return { state: "current", status: report.status || "unknown", reason: "", report };
}

export function auditHeadline(health, options = {}) {
  if (!options.authEnabled || options.loadState === "unavailable") return { state: "unavailable", status: "unknown", label: "UNKNOWN · UNAVAILABLE" };
  if (options.loadState === "loading" || options.loadState === "idle") return { state: "loading", status: "unknown", label: "UNKNOWN · LOADING" };
  if (options.loadState === "error") return { state: "error", status: "unknown", label: "UNKNOWN · LOAD ERROR" };
  const state = health?.state || "absent";
  const status = health?.status || "unknown";
  if (state === "stale") return { state, status: "unknown", label: "UNKNOWN · STALE" };
  if (state === "absent") return { state, status: "unknown", label: "UNKNOWN · NO REPORT" };
  return { state, status, label: status.toUpperCase() };
}

export function auditRunBlocksStart(run) {
  return run?.executionState === "running" && run?.monitoringState !== "unknown";
}

export async function applyPollResultIfCurrent(load, expectedGeneration, currentGeneration, apply) {
  const value = await load();
  if (expectedGeneration !== currentGeneration()) return false;
  apply(value);
  return true;
}

export async function runGenerationStableRead({ read, apply, currentGeneration, initialGeneration, currentRevision = null, initialRevision = null, isDisposed = () => false, maxAttempts = 2 }) {
  let generation = initialGeneration;
  const revisionGuarded = typeof currentRevision === "function";
  let revision = revisionGuarded ? (initialRevision ?? currentRevision()) : null;
  const attempts = Math.max(1, Math.min(Number(maxAttempts) || 1, 3));
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const value = await read(generation, attempt);
    if (isDisposed()) return false;
    const current = currentGeneration();
    const currentResourceRevision = revisionGuarded ? currentRevision() : null;
    if (generation === current && (!revisionGuarded || revision === currentResourceRevision)) {
      apply(value);
      return true;
    }
    generation = current;
    revision = currentResourceRevision;
  }
  return false;
}

export function applyRunStatus(state, status) {
  const profile = status?.profile === "fast" ? "fast" : "standard";
  const executionState = ["running", "completed", "failed"].includes(status?.state) ? status.state : "failed";
  const run = {
    auditID: String(status?.audit_id || ""),
    executionState,
    monitoringState: executionState === "running" ? "active" : "settled",
    errorCode: executionState === "failed" ? String(status?.error_code || "audit_run_failed") : "",
    reportStatus: executionState === "completed" ? String(status?.report?.status || "unknown") : "unknown"
  };
  const next = { ...state, runByProfile: { ...(state?.runByProfile || {}), [profile]: run } };
  if (executionState === "completed" && status?.report && status?.freshness) {
    const envelope = { report: status.report, freshness: status.freshness };
    if (profile === "fast") next.fastEnvelope = envelope;
    else next.standardEnvelope = envelope;
  }
  return next;
}

export function applyRunMonitoringUnknown(state, { auditID, profile, reason, active = true } = {}) {
  const normalizedProfile = profile === "fast" ? "fast" : "standard";
  const previous = state?.runByProfile?.[normalizedProfile] || {};
  const run = {
    ...previous,
    auditID: String(auditID || previous.auditID || ""),
    executionState: active ? "running" : "unknown",
    monitoringState: "unknown",
    monitoringReason: String(reason || "poll_unavailable"),
    errorCode: "",
    reportStatus: "unknown"
  };
  return { ...state, runByProfile: { ...(state?.runByProfile || {}), [normalizedProfile]: run } };
}

export function freshnessRefreshDelayMs(envelope, elapsedMs = 0) {
  const freshness = envelope?.freshness;
  if (freshness?.status !== "current") return 300000;
  const age = numberOrNull(freshness.age_seconds);
  const deadline = numberOrNull(freshness.deadline_seconds);
  if (age == null || deadline == null) return 300000;
  const untilStale = Math.max(deadline - age, 0) * 1000 - Math.max(0, elapsedMs);
  return Math.min(300000, Math.max(1000, untilStale));
}

export function freshnessDeadlineElapsed(envelope, elapsedMs = 0) {
  const freshness = envelope?.freshness;
  if (freshness?.status !== "current") return false;
  const age = numberOrNull(freshness.age_seconds);
  const deadline = numberOrNull(freshness.deadline_seconds);
  if (age == null || deadline == null) return false;
  return Math.max(0, elapsedMs) >= Math.max(deadline - age, 0) * 1000;
}

export function markEnvelopeStale(envelope) {
  if (!envelope?.report || envelope?.freshness?.status !== "current") return envelope;
  const deadline = numberOrNull(envelope.freshness.deadline_seconds);
  const age = numberOrNull(envelope.freshness.age_seconds);
  return {
    ...envelope,
    freshness: {
      ...envelope.freshness,
      status: "unknown",
      reason: "stale",
      age_seconds: deadline == null ? age : Math.max(age || 0, deadline + 1)
    }
  };
}

export function selectImporters(report) {
  const checks = checkMap(report);
  const sources = new Set();
  for (const id of checks.keys()) {
    const match = /^imports\.([a-z_]+)\.(poll|arrivals)$/.exec(id);
    if (match) sources.add(match[1]);
  }
  return [...sources].sort().map((source) => {
    const poll = checks.get(`imports.${source}.poll`);
    const arrivals = checks.get(`imports.${source}.arrivals`);
    if (poll?.status === "skipped" && poll?.skip_reason === "feature_disabled") return null;
    return {
      source,
      poll: poll ? { status: poll.status, skipReason: poll.skip_reason || "", summary: poll.summary, succeededAt: poll.evidence?.succeeded_at || "", ageSeconds: numberOrNull(poll.evidence?.age_seconds) } : null,
      arrivals: arrivals ? { status: arrivals.status, skipReason: arrivals.skip_reason || "", summary: arrivals.summary, quietSeconds: numberOrNull(arrivals.evidence?.quiet_seconds), daily: Array.isArray(arrivals.evidence?.daily) ? arrivals.evidence.daily : [], informational: !arrivals.required } : null
    };
  }).filter(Boolean);
}

export function selectPipeline(report) {
  const checks = checkMap(report);
  const stages = ["hydration", "extraction", "summary", "transcription", "ocr"];
  return stages.flatMap((stage) => {
    const partition = checks.get(`pipeline.${stage}.partition`);
    if (!partition || (partition.status === "skipped" && partition.skip_reason === "feature_disabled")) return [];
    const pending = checks.get(`pipeline.${stage}.pending_age`);
    const pendingStatus = pending?.status || "unknown";
    return [{
      stage, status: worstAuditStatus(partition.status, pendingStatus), partitionStatus: partition.status,
      skipReason: partition.skip_reason || "", summary: partition.summary,
      counts: pickNumbers(partition.evidence, ["total", "current", "pending", "blocked", "terminal", "failed", "unknown"]),
      partitionValid: partition.evidence?.partition_valid === true,
      oldestPendingAgeSeconds: numberOrNull(pending?.evidence?.oldest_pending_age_seconds),
      pendingStatus
    }];
  });
}

export function selectOverview(envelope) {
  const report = envelope?.report;
  if (!report || report.schema !== "dbrain.audit.v1" || report.profile !== "standard" || !report.scope?.whole_system || report.scope?.filtered) return null;
  const latestSync = checkMap(report).get("scheduler.latest_sync");
  return {
    auditID: String(report.audit_id || ""),
    profile: "standard",
    completedAt: String(report.completed_at || ""),
    build: { version: String(report.boundary?.version || ""), commit: String(report.boundary?.commit || "") },
    layout: String(report.boundary?.layout || ""),
    configVerified: report.boundary?.config_verified === true,
    lastSyncAt: String(latestSync?.evidence?.latest_success_at || "")
  };
}

export function selectDurability(report) {
  const checks = checkMap(report);
  return DURABILITY.flatMap(([id, kind, label]) => {
    const check = checks.get(id);
    return check ? [{ id, kind, label, status: check.status, skipReason: check.skip_reason || "", summary: check.summary, evidence: safeEvidence(check.evidence) }] : [];
  });
}

export function selectFindings(report) {
  return (Array.isArray(report?.checks) ? report.checks : [])
    .filter((check) => Object.hasOwn(FINDING_ORDER, check?.status))
    .map((check) => ({
      id: String(check.id || ""), category: String(check.category || ""), status: check.status,
      confidence: String(check.confidence || "unknown"), required: check.required === true,
      summary: String(check.summary || ""), remediation: String(check.remediation || ""),
      observedAt: String(check.observed_at || ""), threshold: safeThreshold(check.threshold), evidence: safeEvidence(check.evidence)
    }))
    .sort((a, b) => FINDING_ORDER[a.status] - FINDING_ORDER[b.status] || Number(b.required) - Number(a.required) || a.id.localeCompare(b.id));
}

export function selectHistory(response) {
  if (response?.profile !== "standard") return [];
  const rows = (Array.isArray(response?.history) ? response.history : [])
    .filter((entry) => entry?.profile === "standard")
    .map((entry) => ({
      auditID: String(entry.audit_id || ""), profile: "standard", status: String(entry.status || "unknown"),
      confidence: String(entry.confidence || "unknown"), completedAt: String(entry.completed_at || ""),
      summary: entry.summary || {}, freshness: entry.freshness || {}, transition: "initial"
    }));
  for (let index = 0; index < rows.length - 1; index += 1) {
    const current = rows[index];
    const prior = rows[index + 1];
    if (current.status === "pass" && prior.status !== "pass") current.transition = "recovered";
    else if (current.status !== prior.status) current.transition = "changed";
    else current.transition = "steady";
  }
  return rows;
}

export function initialAuditRequests(authEnabled) {
  if (!authEnabled) return [];
  return [
    { method: "GET", profile: "standard", resource: "latest" },
    { method: "GET", profile: "standard", resource: "history" },
    { method: "GET", profile: "fast", resource: "latest" }
  ];
}

export function pollRunDecision(status) {
  return status?.state === "running" ? { poll: true, terminal: false } : { poll: false, terminal: true };
}

export function safeEvidence(evidence) {
  const out = {};
  if (!evidence || typeof evidence !== "object" || Array.isArray(evidence)) return out;
  for (const [key, value] of Object.entries(evidence)) {
    if (!SAFE_EVIDENCE_FIELDS.has(key)) continue;
    const safe = safeEvidenceValue(key, value);
    if (safe !== undefined) out[key] = safe;
  }
  return out;
}

function safeEvidenceValue(key, value) {
  if (INTEGER_EVIDENCE_FIELDS.has(key)) return isCount(value) ? value : undefined;
  if (BOOLEAN_EVIDENCE_FIELDS.has(key)) return typeof value === "boolean" ? value : undefined;
  if (TIMESTAMP_EVIDENCE_FIELDS.has(key)) return typeof value === "string" && value.length <= 35 && value.endsWith("Z") && !Number.isNaN(Date.parse(value)) ? value : undefined;
  if (ENUM_EVIDENCE_FIELDS.has(key)) return typeof value === "string" && ENUM_EVIDENCE_FIELDS.get(key).has(value) ? value : undefined;
  if (key === "daily") return safeTypedRows(value, DAILY_FIELDS, 366, validateDailyRow);
  if (key === "by_kind") return safeTypedRows(value, BY_KIND_FIELDS, 64, validateByKindRow);
  if (key === "missing_by_field") return safeMissingFields(value);
  return undefined;
}

function safeTypedRows(value, fields, limit, validate) {
  if (!Array.isArray(value) || value.length > limit) return undefined;
  const rows = [];
  for (const row of value) {
    if (!row || typeof row !== "object" || Array.isArray(row) || Object.keys(row).length !== fields.length || fields.some((field) => !Object.hasOwn(row, field)) || !validate(row)) return undefined;
    rows.push(Object.fromEntries(fields.map((field) => [field, row[field]])));
  }
  return rows;
}

function validateDailyRow(row) {
  return /^\d{4}-\d{2}-\d{2}$/.test(row.day) && DAILY_FIELDS.slice(1).every((field) => isCount(row[field]));
}

function validateByKindRow(row) {
  return EVIDENCE_KINDS.has(row.kind) && BY_KIND_FIELDS.slice(1, -1).every((field) => isCount(row[field])) && typeof row.partition_valid === "boolean";
}

function safeMissingFields(value) {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).length > 32) return undefined;
  const out = {};
  for (const [key, count] of Object.entries(value)) {
    if (!MISSING_FIELD_NAMES.has(key) || !isCount(count)) return undefined;
    out[key] = count;
  }
  return out;
}

function isCount(value) {
  return Number.isInteger(value) && value >= 0;
}

function checkMap(report) {
  return new Map((Array.isArray(report?.checks) ? report.checks : []).map((check) => [String(check?.id || ""), check]));
}

function numberOrNull(value) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
}

function worstAuditStatus(...statuses) {
  const severity = { fail: 5, unknown: 4, warn: 3, pass: 2, skipped: 1 };
  return statuses.reduce((worst, status) => (severity[status] || severity.unknown) > (severity[worst] || 0) ? status : worst, "skipped");
}

function pickNumbers(source, keys) {
  const out = {};
  for (const key of keys) out[key] = numberOrNull(source?.[key]);
  return out;
}

function safeThreshold(value) {
  if (!value || typeof value !== "object") return null;
  return pickNumbers(value, ["warn_after_seconds", "fail_after_seconds"]);
}
