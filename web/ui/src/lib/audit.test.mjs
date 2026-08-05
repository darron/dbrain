import assert from "node:assert/strict";
import test from "node:test";

import {
  applyRunStatus,
  applyRunMonitoringUnknown,
  applyPollResultIfCurrent,
  auditHeadline,
  auditRunBlocksStart,
  freshnessDeadlineElapsed,
  freshnessRefreshDelayMs,
  initialAuditRequests,
  overallHealth,
  markEnvelopeStale,
  pollRunDecision,
  runGenerationStableRead,
  safeEvidence,
  selectDurability,
  selectFindings,
  selectHistory,
  selectImporters,
  selectOverview,
  selectPipeline,
  selectSemantic
} from "./audit.js";

function report(profile = "standard") {
  return {
    schema: "dbrain.audit.v1",
    audit_id: `report-${profile}`,
    profile,
    scope: { whole_system: true, filtered: false, categories: [], sources: [], check_ids: [] },
    started_at: "2026-07-14T01:00:00Z",
    completed_at: "2026-07-14T01:01:00Z",
    status: "pass",
    confidence: "high",
    boundary: { layout: "xdg", config_verified: true, version: "v0.7.0", commit: "abcdef123456" },
    summary: { all: { pass: 8, warn: 0, fail: 0, unknown: 0, skipped: 0 }, required: { pass: 8, warn: 0, fail: 0, unknown: 0 } },
    checks: [
      check("scheduler.latest_sync", "scheduler", "pass", { latest_success_at: "2026-07-14T00:55:00Z", age_seconds: 360 }),
      check("imports.apple_notes.poll", "imports", "pass", { succeeded_at: "2026-07-14T00:58:00Z", age_seconds: 180, success_count: 4, failure_count: 0 }),
      check("imports.apple_notes.arrivals", "imports", "pass", { quiet_seconds: 86400, daily: [{ day: "2026-07-14", created: 0, updated: 0, unchanged: 4, skipped: 0, linked: 0, blocked: 0, failed: 0 }] }, false),
      check("pipeline.ocr.partition", "pipeline", "warn", { total: 15, current: 8, pending: 2, blocked: 1, terminal: 3, failed: 1, unknown: 0, partition_valid: true, by_kind: [] }),
      check("pipeline.ocr.pending_age", "pipeline", "warn", { pending_count: 2, oldest_pending_age_seconds: 5400, warn_after_seconds: 3600, fail_after_seconds: 7200 }),
      check("durability.media_local_coverage", "durability", "pass", { eligible_local_count: 9, uncovered_pruned_count: 0, orphan_count: 0 }),
      check("durability.media_remote", "durability", "pass", { population_count: 9, checked_count: 9, missing_count: 0, inventory_complete: true }),
      check("durability.sqlite_backup_configuration", "durability", "pass", { capability_configured: true, scheduler_enabled: true, audit_required: true, configuration_state: "required_ready" }),
      check("durability.sqlite_backup_age", "durability", "fail", { archive_count: 2, latest_age_seconds: 99000, latest_size_bytes: 1234, listing_complete: true }, true, "Back up SQLite now", { host_path: "/secret/db", url: "https://example.invalid/private", source_key: "secret:1", object_key: "private/archive", title: "secret title", raw_error: "provider token leaked" }),
      check("durability.okf_freshness", "durability", "pass", { manifest_valid: true, exported_at: "2026-07-14T00:30:00Z", age_seconds: 1800 }),
      check("durability.okf_validation", "durability", "unknown", { manifest_valid: true, document_count: 44, broken_link_count: 0, validation_error_count: 1, traversal_complete: false }, true)
    ]
  };
}

function check(id, category, status, evidence, required = true, remediation = "", extraEvidence = {}) {
  return {
    id, category, status, confidence: status === "unknown" ? "unknown" : "high", required,
    summary: `${id} ${status}`,
    observed_at: "2026-07-14T01:01:00Z",
    evidence: { ...evidence, ...extraEvidence }, remediation
  };
}

function envelope(profile = "standard", freshness = { status: "current", age_seconds: 60, deadline_seconds: 43200 }) {
  return { report: report(profile), freshness };
}

test("only a current unfiltered whole-system standard report establishes overall health", () => {
  assert.deepEqual(overallHealth(envelope()), { state: "current", status: "pass", reason: "", report: envelope().report });

  const fast = envelope("fast");
  assert.equal(overallHealth(fast).status, "unknown");
  assert.equal(overallHealth(fast).reason, "not_standard");

  const filtered = envelope();
  filtered.report.scope.filtered = true;
  filtered.report.scope.whole_system = false;
  assert.equal(overallHealth(filtered).reason, "invalid_scope");

  const wrongSchema = envelope();
  wrongSchema.report.schema = "future.audit.v2";
  assert.equal(overallHealth(wrongSchema).reason, "invalid_schema");
});

test("standard overview and health accept the v1 and v2 audit envelopes only", () => {
  const v1 = envelope();
  const v2 = envelope();
  v2.report.schema = "dbrain.audit.v2";
  const future = envelope();
  future.report.schema = "dbrain.audit.v3";

  assert.equal(overallHealth(v1).state, "current");
  assert.equal(overallHealth(v2).state, "current");
  assert.equal(selectOverview(v1)?.auditID, "report-standard");
  assert.equal(selectOverview(v2)?.auditID, "report-standard");
  assert.equal(overallHealth(future).reason, "invalid_schema");
  assert.equal(selectOverview(future), null);
});

test("absent and stale standard reports remain visibly unknown at freshness boundaries", () => {
  assert.deepEqual(overallHealth({ report: null, freshness: { status: "unknown", reason: "not_found", deadline_seconds: 43200 } }), {
    state: "absent", status: "unknown", reason: "not_found", report: null
  });
  const stale = overallHealth(envelope("standard", { status: "unknown", reason: "stale", age_seconds: 43201, deadline_seconds: 43200 }));
  assert.equal(stale.state, "stale");
  assert.equal(stale.status, "unknown");
  assert.equal(stale.report.status, "pass", "historical report detail remains immutable");
  assert.equal(overallHealth(envelope("standard", { status: "current", age_seconds: 43200, deadline_seconds: 43200 })).status, "pass");
});

test("headline distinguishes loading API error auth unavailable and absent report", () => {
  const absent = overallHealth({ report: null, freshness: { status: "unknown", reason: "not_found" } });
  assert.equal(auditHeadline(absent, { authEnabled: true, loadState: "loading" }).state, "loading");
  assert.equal(auditHeadline(absent, { authEnabled: true, loadState: "error" }).label, "UNKNOWN · LOAD ERROR");
  assert.equal(auditHeadline(absent, { authEnabled: false, loadState: "unavailable" }).label, "UNKNOWN · UNAVAILABLE");
  assert.equal(auditHeadline(absent, { authEnabled: true, loadState: "ready" }).label, "UNKNOWN · NO REPORT");
});

test("a newer fast completion updates only fast state and never recovers standard health", () => {
  const state = {
    standardEnvelope: envelope("standard", { status: "unknown", reason: "stale", age_seconds: 50000, deadline_seconds: 43200 }),
    fastEnvelope: null,
    runByProfile: { fast: { state: "running" }, standard: null }
  };
  const fastReport = report("fast");
  fastReport.status = "pass";
  const next = applyRunStatus(state, { audit_id: "run_fast_1", profile: "fast", state: "completed", report: fastReport, freshness: { status: "current", age_seconds: 0, deadline_seconds: 7200 } });
  assert.equal(next.standardEnvelope, state.standardEnvelope);
  assert.equal(next.fastEnvelope.report.audit_id, "report-fast");
  assert.equal(overallHealth(next.standardEnvelope).state, "stale");
  assert.equal(next.runByProfile.fast.executionState, "completed");
  assert.equal(next.runByProfile.fast.reportStatus, "pass");
});

test("execution failure is distinct from a completed failing report", () => {
  const base = { standardEnvelope: null, fastEnvelope: null, runByProfile: { fast: null, standard: null } };
  const failedExecution = applyRunStatus(base, { audit_id: "run_1", profile: "standard", state: "failed", error_code: "audit_run_failed" });
  assert.equal(failedExecution.runByProfile.standard.executionState, "failed");
  assert.equal(failedExecution.runByProfile.standard.reportStatus, "unknown");

  const failingReport = report("standard");
  failingReport.status = "fail";
  const completed = applyRunStatus(base, { audit_id: "run_2", profile: "standard", state: "completed", report: failingReport, freshness: { status: "current", age_seconds: 0, deadline_seconds: 43200 } });
  assert.equal(completed.runByProfile.standard.executionState, "completed");
  assert.equal(completed.runByProfile.standard.reportStatus, "fail");
  assert.equal(completed.standardEnvelope.report.audit_id, "report-standard");
});

test("client monitoring failure never becomes a server execution failure", () => {
  const running = applyRunStatus({ standardEnvelope: null, fastEnvelope: null, runByProfile: { fast: null, standard: null } }, {
    audit_id: "run_1", profile: "standard", state: "running"
  });
  const unknown = applyRunMonitoringUnknown(running, { auditID: "run_1", profile: "standard", reason: "poll_unavailable" });
  assert.equal(unknown.runByProfile.standard.executionState, "running");
  assert.equal(unknown.runByProfile.standard.monitoringState, "unknown");
  assert.equal(unknown.runByProfile.standard.errorCode, "");
  assert.equal(unknown.runByProfile.standard.reportStatus, "unknown");
  assert.equal(auditRunBlocksStart(running.runByProfile.standard), true);
  assert.equal(auditRunBlocksStart(unknown.runByProfile.standard), false, "retrying POST is authoritative when monitoring is unavailable");
  const retired = applyRunMonitoringUnknown(unknown, { auditID: "run_1", profile: "standard", reason: "run_status_forgotten", active: false });
  assert.equal(retired.runByProfile.standard.executionState, "unknown");
  assert.equal(retired.runByProfile.standard.monitoringState, "unknown");
  const recovered = applyRunStatus(unknown, { audit_id: "run_1", profile: "standard", state: "completed", report: report("standard"), freshness: { status: "current", age_seconds: 0, deadline_seconds: 43200 } });
  assert.equal(recovered.runByProfile.standard.monitoringState, "settled");
  assert.equal(recovered.runByProfile.standard.executionState, "completed");
});

test("a deferred old poll result cannot apply after poll generation changes", async () => {
  let resolveOld;
  let generation = 7;
  const applied = [];
  const oldRead = applyPollResultIfCurrent(
    () => new Promise((resolve) => { resolveOld = resolve; }),
    7,
    () => generation,
    (value) => applied.push(value)
  );
  generation = 8;
  resolveOld({ audit_id: "old-report" });
  assert.equal(await oldRead, false);
  assert.deepEqual(applied, []);

  const currentRead = applyPollResultIfCurrent(
    async () => ({ audit_id: "new-report" }),
    8,
    () => generation,
    (value) => applied.push(value)
  );
  assert.equal(await currentRead, true);
  assert.deepEqual(applied, [{ audit_id: "new-report" }]);
});

test("a freshness read invalidated by a newer run retries once and cannot overwrite the newer report", async () => {
  let resolveOld;
  let generation = 10;
  let reportState = { audit_id: "report-b" };
  let reads = 0;
  const refresh = runGenerationStableRead({
    initialGeneration: 10,
    currentGeneration: () => generation,
    maxAttempts: 2,
    read: async () => {
      reads += 1;
      if (reads === 1) return new Promise((resolve) => { resolveOld = resolve; });
      return { audit_id: "report-b" };
    },
    apply: (value) => { reportState = value; }
  });
  generation = 11;
  resolveOld({ audit_id: "report-a" });
  assert.equal(await refresh, true);
  assert.equal(reads, 2);
  assert.deepEqual(reportState, { audit_id: "report-b" });
});

test("a standard history read invalidated by a concurrent run retries under the current generation", async () => {
  let resolveOld;
  let generation = 20;
  let historyState = ["existing"];
  let reads = 0;
  const refresh = runGenerationStableRead({
    initialGeneration: 20,
    currentGeneration: () => generation,
    maxAttempts: 2,
    read: async () => {
      reads += 1;
      if (reads === 1) return new Promise((resolve) => { resolveOld = resolve; });
      return ["completed-standard"];
    },
    apply: (value) => { historyState = value; }
  });
  generation = 21;
  resolveOld(["stale-history"]);
  assert.equal(await refresh, true);
  assert.equal(reads, 2);
  assert.deepEqual(historyState, ["completed-standard"]);
});

test("generation-stable reads stop after the bounded retry budget without applying stale data", async () => {
  let generation = 30;
  let reads = 0;
  const applied = [];
  const refreshed = await runGenerationStableRead({
    initialGeneration: 30,
    currentGeneration: () => generation,
    maxAttempts: 2,
    read: async () => {
      reads += 1;
      generation += 1;
      return { attempt: reads };
    },
    apply: (value) => applied.push(value)
  });
  assert.equal(refreshed, false);
  assert.equal(reads, 2);
  assert.deepEqual(applied, []);
});

test("a generation retry cannot overwrite a terminal envelope applied while its second read is pending", async () => {
  let resolveFirst;
  let resolveRetry;
  let markRetryStarted;
  const retryStarted = new Promise((resolve) => { markRetryStarted = resolve; });
  let generation = 40;
  let revision = 1;
  let envelopeState = { audit_id: "report-a" };
  let reads = 0;
  const refresh = runGenerationStableRead({
    initialGeneration: generation,
    initialRevision: revision,
    currentGeneration: () => generation,
    currentRevision: () => revision,
    maxAttempts: 2,
    read: async () => {
      reads += 1;
      if (reads === 1) return new Promise((resolve) => { resolveFirst = resolve; });
      return new Promise((resolve) => {
        resolveRetry = resolve;
        markRetryStarted();
      });
    },
    apply: (value) => { envelopeState = value; revision += 1; }
  });

  generation = 41;
  resolveFirst({ audit_id: "report-a" });
  await retryStarted;
  assert.equal(reads, 2);

  envelopeState = { audit_id: "report-b" };
  revision += 1;
  resolveRetry({ audit_id: "report-a" });
  assert.equal(await refresh, false);
  assert.equal(reads, 2);
  assert.deepEqual(envelopeState, { audit_id: "report-b" });
});

test("a same-generation history response cannot overwrite a newer history revision", async () => {
  let resolveOld;
  const generation = 50;
  let revision = 3;
  let historyState = ["old"];
  let reads = 0;
  const refresh = runGenerationStableRead({
    initialGeneration: generation,
    initialRevision: revision,
    currentGeneration: () => generation,
    currentRevision: () => revision,
    maxAttempts: 2,
    read: async () => {
      reads += 1;
      if (reads === 1) return new Promise((resolve) => { resolveOld = resolve; });
      return ["new-terminal-row"];
    },
    apply: (value) => { historyState = value; revision += 1; }
  });
  historyState = ["new-terminal-row"];
  revision += 1;
  resolveOld(["old"]);
  assert.equal(await refresh, true);
  assert.equal(reads, 2);
  assert.deepEqual(historyState, ["new-terminal-row"]);
});

test("poll and quiet arrivals are separate importer signals", () => {
  const [apple] = selectImporters(report());
  assert.equal(apple.source, "apple_notes");
  assert.equal(apple.poll.status, "pass");
  assert.equal(apple.poll.succeededAt, "2026-07-14T00:58:00Z");
  assert.equal(apple.arrivals.quietSeconds, 86400);
  assert.equal(apple.arrivals.informational, true);
  assert.equal(apple.arrivals.status, "pass");
});

test("feature-disabled importers and pipeline stages are not presented as unknown health", () => {
  const fixture = report();
  fixture.checks.push(
    { ...check("imports.feeds.poll", "imports", "skipped", {}), skip_reason: "feature_disabled" },
    { ...check("imports.feeds.arrivals", "imports", "skipped", {}, false), skip_reason: "feature_disabled" },
    { ...check("pipeline.transcription.partition", "pipeline", "skipped", {}), skip_reason: "feature_disabled" }
  );
  assert.deepEqual(selectImporters(fixture).map((row) => row.source), ["apple_notes"]);
  assert.deepEqual(selectPipeline(fixture).map((row) => row.stage), ["ocr"]);
});

test("enabled warning and unknown statuses remain explicit", () => {
  const fixture = report();
  fixture.checks.find((row) => row.id === "imports.apple_notes.poll").status = "warn";
  fixture.checks.find((row) => row.id === "pipeline.ocr.partition").status = "unknown";
  assert.equal(selectImporters(fixture)[0].poll.status, "warn");
  assert.equal(selectImporters(fixture)[0].poll.skipReason, "");
  assert.equal(selectPipeline(fixture)[0].status, "unknown");
  assert.equal(selectPipeline(fixture)[0].skipReason, "");
});

test("pipeline partitions preserve current pending blocked terminal failed and unknown", () => {
  const [ocr] = selectPipeline(report());
  assert.equal(ocr.stage, "ocr");
  assert.deepEqual(ocr.counts, { total: 15, current: 8, pending: 2, blocked: 1, terminal: 3, failed: 1, unknown: 0 });
  assert.equal(ocr.oldestPendingAgeSeconds, 5400);
});

test("pipeline card severity includes pending-age check status", () => {
  const fixture = report();
  fixture.checks.find((row) => row.id === "pipeline.ocr.partition").status = "pass";
  fixture.checks.find((row) => row.id === "pipeline.ocr.pending_age").status = "fail";
  const [ocr] = selectPipeline(fixture);
  assert.equal(ocr.partitionStatus, "pass");
  assert.equal(ocr.pendingStatus, "fail");
  assert.equal(ocr.status, "fail");
});

test("semantic selector admits only the registered v2 evidence contract", () => {
  const fixture = report();
  fixture.schema = "dbrain.audit.v2";
  fixture.checks.push(
    check("semantic.current_readiness", "semantic", "warn", {
      configured: true, capability: "available", backend: "ollama", profile_id: "embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", active_generation_id: "generation-2026",
      readiness: "catching_up", dirty_parent_count: 4, pending_parent_count: 3, due_embedding_count: 2, blocked_embedding_count: 1,
      failed_embedding_count: 0, indexed_vector_count: 55, l0_vector_count: 6, tombstone_count: 7, segment_count: 8,
      raw_error: "must not render"
    }),
    check("semantic.latest_attached_refresh", "semantic", "pass", {
      refresh_state: "succeeded", started_at: "2026-07-14T00:55:00Z", completed_at: "2026-07-14T00:56:00Z", age_seconds: 300,
      duration_seconds: 60, projected_parent_count: 2, embedded_chunk_count: 3, flushed_vector_count: 4, compacted_vector_count: 5,
      verified_vector_count: 6, successor_run_count: 0, semantic_error_code: "", host_path: "/private/no"
    }),
    check("semantic.stage_summary", "semantic", "pass", { stages: [
      { stage: "projection", status: "succeeded", duration_seconds: 1 }, { stage: "embedding", status: "succeeded", duration_seconds: 2 },
      { stage: "flush", status: "succeeded", duration_seconds: 3 }, { stage: "compaction", status: "succeeded", duration_seconds: 4 },
      { stage: "verification", status: "succeeded", duration_seconds: 5 }, { stage: "readiness", status: "succeeded", duration_seconds: 6 }
    ] })
  );

  const semantic = selectSemantic(fixture);
  assert.equal(semantic.state, "available");
  assert.equal(semantic.current.profileID, "embedding-profile-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
  assert.equal(semantic.current.readiness, "catching_up");
  assert.deepEqual(semantic.latest.counts, { projectedParents: 2, embeddedChunks: 3, flushedVectors: 4, compactedVectors: 5, verifiedVectors: 6, successorRuns: 0 });
  assert.deepEqual(semantic.stages.map((row) => [row.stage, row.status, row.durationSeconds]), [
    ["projection", "succeeded", 1], ["embedding", "succeeded", 2], ["flush", "succeeded", 3], ["compaction", "succeeded", 4], ["verification", "succeeded", 5], ["readiness", "succeeded", 6]
  ]);
  assert.equal(Object.hasOwn(semantic.current, "raw_error"), false);
  assert.equal(Object.hasOwn(semantic.latest, "host_path"), false);
});

test("semantic selector keeps legacy disabled unsupported and incomplete states visible", () => {
  assert.deepEqual(selectSemantic(report()), { state: "legacy", current: null, latest: null, stages: [] });

  const legacyWithForgedSemanticEvidence = report();
  legacyWithForgedSemanticEvidence.checks.push(check("semantic.current_readiness", "semantic", "pass", { configured: true, capability: "available", backend: "ollama", readiness: "ready" }));
  assert.deepEqual(selectSemantic(legacyWithForgedSemanticEvidence), { state: "legacy", current: null, latest: null, stages: [] });

  const disabled = report();
  disabled.schema = "dbrain.audit.v2";
  disabled.checks.push(
    check("semantic.current_readiness", "semantic", "unknown", { configured: false, capability: "disabled", backend: "none", readiness: "disabled" }),
    check("semantic.latest_attached_refresh", "semantic", "unknown", { refresh_state: "skipped" }),
    check("semantic.stage_summary", "semantic", "pass", { stages: [
      { stage: "projection", status: "skipped", duration_seconds: 0 }, { stage: "embedding", status: "skipped", duration_seconds: 0 },
      { stage: "flush", status: "skipped", duration_seconds: 0 }, { stage: "compaction", status: "skipped", duration_seconds: 0 },
      { stage: "verification", status: "skipped", duration_seconds: 0 }, { stage: "readiness", status: "skipped", duration_seconds: 0 }
    ] })
  );
  const selected = selectSemantic(disabled);
  assert.equal(selected.state, "disabled");
  assert.equal(selected.latest.refreshState, "skipped");
  assert.equal(selected.stages.length, 6);

  const unsupported = report();
  unsupported.schema = "dbrain.audit.v2";
  unsupported.checks.push(check("semantic.current_readiness", "semantic", "unknown", { configured: true, capability: "unsupported", backend: "unsupported", readiness: "unavailable" }));
  assert.equal(selectSemantic(unsupported).state, "unsupported");

  const incomplete = report();
  incomplete.schema = "dbrain.audit.v2";
  incomplete.checks.push(check("semantic.current_readiness", "semantic", "unknown", { configured: true, capability: "available", backend: "ollama", readiness: "ready" }));
  assert.equal(selectSemantic(incomplete).state, "incomplete");
  assert.deepEqual(selectSemantic(incomplete).stages, []);
});

test("semantic selector rejects malformed or unregistered evidence without crashing", () => {
  const malformed = report();
  malformed.schema = "dbrain.audit.v2";
  malformed.checks.push(
    check("semantic.current_readiness", "semantic", "pass", { configured: "yes", capability: "available", backend: "ollama", readiness: "ready", profile_id: "profile-2026" }),
    check("semantic.latest_attached_refresh", "semantic", "unknown", { refresh_state: "forged", duration_seconds: -1, semantic_error_code: "https://private.invalid" }),
    check("semantic.stage_summary", "semantic", "unknown", { stages: [{ stage: "forged", status: "ok", duration_seconds: 1 }] })
  );
  const selected = selectSemantic(malformed);
  assert.equal(selected.state, "incomplete");
  assert.equal(selected.current.configured, null);
  assert.equal(selected.current.profileID, "");
  assert.equal(selected.latest.refreshState, "unknown");
  assert.deepEqual(selected.stages, []);
});

test("overview derives build layout last audit and last sync only from the standard report", () => {
  assert.deepEqual(selectOverview(envelope()), {
    auditID: "report-standard",
    profile: "standard",
    completedAt: "2026-07-14T01:01:00Z",
    build: { version: "v0.7.0", commit: "abcdef123456" },
    layout: "xdg",
    configVerified: true,
    lastSyncAt: "2026-07-14T00:55:00Z"
  });
  assert.equal(selectOverview(envelope("fast")), null);
});

test("durability cards select only exact audit check IDs", () => {
  const cards = selectDurability(report());
  assert.deepEqual(cards.map((card) => card.id), [
    "durability.media_local_coverage",
    "durability.media_remote",
    "durability.sqlite_backup_configuration",
    "durability.sqlite_backup_age",
    "durability.okf_freshness",
    "durability.okf_validation"
  ]);
  assert.equal(cards.find((card) => card.id === "durability.sqlite_backup_age").status, "fail");
  assert.equal(cards.find((card) => card.id === "durability.okf_validation").status, "unknown");
});

test("feature-disabled durability is explicit rather than unknown", () => {
  const fixture = report();
  const okf = fixture.checks.find((row) => row.id === "durability.okf_validation");
  okf.status = "skipped";
  okf.skip_reason = "feature_disabled";
  const card = selectDurability(fixture).find((row) => row.id === "durability.okf_validation");
  assert.equal(card.status, "skipped");
  assert.equal(card.skipReason, "feature_disabled");
});

test("freshness schedules a GET refresh and becomes stale without mutating the report", () => {
  const current = envelope("standard", { status: "current", age_seconds: 59, deadline_seconds: 60 });
  const longLived = envelope("standard", { status: "current", age_seconds: 0, deadline_seconds: 43200 });
  assert.equal(freshnessRefreshDelayMs(longLived), 300000);
  assert.equal(freshnessDeadlineElapsed(longLived, 300000), false, "a failed five-minute refresh does not make a current report stale");
  assert.equal(freshnessRefreshDelayMs(longLived, 43199000), 1000, "elapsed time continues toward the original deadline after failed refreshes");
  assert.equal(freshnessDeadlineElapsed(longLived, 43199000), false);
  assert.equal(freshnessDeadlineElapsed(longLived, 43200000), true);
  assert.equal(freshnessRefreshDelayMs(current), 1000);
  assert.equal(freshnessDeadlineElapsed(current, 999), false);
  assert.equal(freshnessDeadlineElapsed(current, 1000), true);
  assert.equal(freshnessRefreshDelayMs(envelope("standard", { status: "current", age_seconds: 60, deadline_seconds: 60 })), 1000);
  assert.equal(freshnessRefreshDelayMs(envelope("standard", { status: "unknown", reason: "stale", age_seconds: 61, deadline_seconds: 60 })), 300000);
  const stale = markEnvelopeStale(current);
  assert.equal(stale.report, current.report);
  assert.notEqual(stale.freshness, current.freshness);
  assert.equal(stale.freshness.status, "unknown");
  assert.equal(stale.freshness.reason, "stale");
  assert.equal(current.freshness.status, "current");
});

test("findings order fail unknown warn and expose only typed evidence allowlist", () => {
  const findings = selectFindings(report());
  assert.deepEqual(findings.slice(0, 3).map((finding) => finding.status), ["fail", "unknown", "warn"]);
  const sqlite = findings[0];
  assert.equal(sqlite.id, "durability.sqlite_backup_age");
  assert.equal(sqlite.remediation, "Back up SQLite now");
  assert.equal(sqlite.evidence.latest_age_seconds, 99000);
  assert.equal(Object.hasOwn(sqlite.evidence, "host_path"), false);
  assert.equal(Object.hasOwn(sqlite.evidence, "url"), false);
  assert.equal(Object.hasOwn(sqlite.evidence, "source_key"), false);
  assert.equal(Object.hasOwn(sqlite.evidence, "object_key"), false);
  assert.equal(Object.hasOwn(sqlite.evidence, "title"), false);
  assert.equal(Object.hasOwn(sqlite.evidence, "raw_error"), false);
});

test("typed evidence rejects hostile nested fields and malformed pipeline numbers", () => {
  assert.deepEqual(safeEvidence({
    daily: [{ day: "2026-07-14", created: 1, updated: 0, unchanged: 0, skipped: 0, linked: 0, blocked: 0, failed: 0, raw_error: "https://secret.invalid" }],
    by_kind: [{ kind: "x_photo", total: 1, current: 1, pending: 0, blocked: 0, terminal: 0, failed: 0, unknown: 0, partition_valid: true, source_key: "secret" }],
    missing_by_field: { model: 1, object_key: 2 },
    configuration_state: "https://secret.invalid/private"
  }), {});
  assert.deepEqual(safeEvidence({ configuration_state: "required_ready", listing_complete: true, latest_age_seconds: 42 }), {
    configuration_state: "required_ready", listing_complete: true, latest_age_seconds: 42
  });

  const malformed = report();
  const partition = malformed.checks.find((row) => row.id === "pipeline.ocr.partition");
  delete partition.evidence.unknown;
  partition.evidence.pending = "2";
  assert.deepEqual(selectPipeline(malformed)[0].counts, { total: 15, current: 8, pending: null, blocked: 1, terminal: 3, failed: 1, unknown: null });
});

test("history is standard-only and marks recovery transitions without accepting fast health", () => {
  const history = selectHistory({ profile: "standard", history: [
    { audit_id: "new-pass", profile: "standard", status: "pass", confidence: "high", completed_at: "2026-07-14T01:00:00Z", summary: {}, freshness: { status: "current" } },
    { audit_id: "fast-pass", profile: "fast", status: "pass", confidence: "high", completed_at: "2026-07-14T00:30:00Z", summary: {}, freshness: { status: "current" } },
    { audit_id: "old-fail", profile: "standard", status: "fail", confidence: "high", completed_at: "2026-07-14T00:00:00Z", summary: {}, freshness: { status: "current" } }
  ] });
  assert.deepEqual(history.map((entry) => entry.auditID), ["new-pass", "old-fail"]);
  assert.equal(history[0].transition, "recovered");
  assert.equal(history[1].transition, "initial");
});

test("initial audit load is GET-only and terminal states stop polling", () => {
  assert.deepEqual(initialAuditRequests(true), [
    { method: "GET", profile: "standard", resource: "latest" },
    { method: "GET", profile: "standard", resource: "history" },
    { method: "GET", profile: "fast", resource: "latest" }
  ]);
  assert.deepEqual(initialAuditRequests(false), []);
  assert.deepEqual(pollRunDecision({ state: "running" }), { poll: true, terminal: false });
  assert.deepEqual(pollRunDecision({ state: "completed" }), { poll: false, terminal: true });
  assert.deepEqual(pollRunDecision({ state: "failed" }), { poll: false, terminal: true });
});
