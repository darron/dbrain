import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import test from "node:test";

const here = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(here, "../App.svelte"), "utf8");
const statsSource = readFileSync(resolve(here, "../components/StatsBar.svelte"), "utf8");
const overviewSource = readFileSync(resolve(here, "../components/AuditOverview.svelte"), "utf8");
const pipelineSource = readFileSync(resolve(here, "../components/AuditPipeline.svelte"), "utf8");
const durabilitySource = readFileSync(resolve(here, "../components/AuditDurability.svelte"), "utf8");
const semanticSource = readFileSync(resolve(here, "../lib/AuditSemantic.svelte"), "utf8");
const auditComponentNames = ["AuditOverview", "AuditImporters", "AuditPipeline", "AuditSemantic", "AuditDurability", "AuditFindings", "AuditHistory"];

test("chat is default and research is absent from primary mode tabs", () => {
  assert.match(appSource, /let inputMode = "chat"/);
  assert.doesNotMatch(appSource, /class:active=\{inputMode === "research"\}[\s\S]{0,120}>\s*Research\s*</);
  assert.match(appSource, />\s*Chat\s*</);
  assert.match(appSource, />\s*Harness\s*</);
  assert.match(appSource, /progress-current/);
  assert.match(appSource, /turn\.status === "researching" && !currentProgressStep\(turn\)/);
  assert.match(appSource, /turn\.status === "synthesizing" && !turn\.answer && !currentProgressStep\(turn\)/);
  assert.match(appSource, /void loadHarnessTraces\(\{ quiet: true \}\)/);
  assert.match(appSource, /Generated answer rejected:/);
  assert.match(appSource, /turn\?\.status === "verification_failed"\) return \[\]/);
  assert.match(appSource, /formatSemanticDiagnostics\(turn\.research_pack\)/);
  assert.match(appSource, /formatSemanticDiagnostics\(researchPack\)/);
});

test("admin audit surface loads saved reports only and cancels polling on destroy", () => {
  for (const name of auditComponentNames) {
    assert.match(appSource, new RegExp(`import ${name} from`));
    assert.match(appSource, new RegExp(`<${name}(?:\\s|>)`));
  }
  assert.match(appSource, /Promise\.allSettled\(\[\s*getAuditLatest\("standard"/);
  assert.doesNotMatch(appSource, /onMount[\s\S]{0,1800}startAuditRun\(/);
  assert.match(appSource, /onDestroy\(\(\) => \{[\s\S]*auditController\.abort\(\)/);
  assert.match(appSource, /getAuditRun\(auditID, \{ signal: auditController\.signal \}\)/);
  assert.match(appSource, /getAuditLatest\("standard", \{ signal: auditController\.signal \}\)/);
  assert.match(appSource, /markEnvelopeStale\(standardEnvelope\)/);
  assert.match(appSource, /clearTimeout\(standardFreshnessTimer\)/);
  assert.doesNotMatch(appSource, /report\?\.audit_id === activeAuditID/);
  assert.match(appSource, /error\.status === 404/);
  assert.match(appSource, /if \(!statusForgotten\) scheduleAuditPoll/);
  assert.match(appSource, /auditRunBlocksStart\(runByProfile\.fast\)/);
  assert.match(appSource, /refreshLatestAudit\(profile, generation\)/);
  assert.match(appSource, /30000, generation\)/);
  assert.match(appSource, /refreshStandardAuditHistory\(generation, 2, 3\)/);
  assert.match(appSource, /runGenerationStableRead\(\{/);
  assert.match(appSource, /const generation = auditPollGeneration/);
  assert.match(appSource, /refreshLatestAudit\("standard", generation, 2\)/);
  assert.match(appSource, /standardHistoryNeedsRefresh/);
  assert.match(appSource, /scheduleStandardHistoryRetry/);
  assert.match(appSource, /if \(standardHistoryNeedsRefresh\) await refreshStandardAuditHistory/);
  assert.match(appSource, /auditEnvelopeRevision/);
  assert.match(appSource, /auditHistoryRevision/);
  assert.match(appSource, /currentRevision:/);
  assert.doesNotMatch(appSource, /state: "failed", error_code: "audit_poll_/);
  assert.match(overviewSource, /status unavailable · audit may still be running/);
  assert.match(pipelineSource, /pending age \{stage\.pendingStatus\}/);
  assert.match(durabilitySource, /Disabled by configuration/);
  assert.match(appSource, /<AuditSemantic semantic=\{auditSemantic\}/);
  assert.match(semanticSource, /Semantic audit unavailable in this legacy report/);
  assert.match(semanticSource, /\{#each semantic\.stages as stage\}/);
  assert.match(semanticSource, /Successful zero-work remains a healthy refresh/);
});

test("legacy drained signal is explicitly scoped away from whole-system health", () => {
  assert.match(statsSource, />Source backlog drained</);
  assert.match(statsSource, /backlog\.scope_description/);
  assert.match(statsSource, /not whole-system health/);
});

test("audit components render text without raw HTML", () => {
  for (const name of auditComponentNames) {
    const source = name === "AuditSemantic" ? semanticSource : readFileSync(resolve(here, `../components/${name}.svelte`), "utf8");
    assert.doesNotMatch(source, /\{@html/);
    assert.doesNotMatch(source, /https?:\/\//);
  }
});

test("fast lifecycle wording and history grid contract match the accepted UI language", () => {
  const overview = readFileSync(resolve(here, "../components/AuditOverview.svelte"), "utf8");
  const importers = readFileSync(resolve(here, "../components/AuditImporters.svelte"), "utf8");
  const history = readFileSync(resolve(here, "../components/AuditHistory.svelte"), "utf8");
  assert.equal((overview.match(/Fast local refresh/g) || []).length >= 4, true);
  assert.match(overview, /\$: fastRunCopy = runCopy\("fast", runByProfile\?\.fast\)/);
  assert.match(overview, /\$: standardRunCopy = runCopy\("standard", runByProfile\?\.standard\)/);
  assert.match(overview, /\{#if loadState === "ready"\}[\s\S]*Last standard audit/);
  assert.match(overview, /No absence or health claim is made/);
  assert.match(importers, /status === "warn" \? "▲"/);
  assert.doesNotMatch(history, /audit-history-rail/);
});
