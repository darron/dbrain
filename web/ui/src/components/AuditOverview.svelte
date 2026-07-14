<script>
  import { formatTime } from "../lib/time.js";
  import { auditHeadline, auditRunBlocksStart } from "../lib/audit.js";

  export let health;
  export let overview;
  export let standardEnvelope;
  export let fastEnvelope;
  export let loading = false;
  export let loadState = "idle";
  export let error = "";
  export let authEnabled = false;
  export let runByProfile = { fast: null, standard: null };
  export let onRun = () => {};

  $: headline = auditHeadline(health, { authEnabled, loadState });
  $: currentStatus = headline.status;
  $: stateLabel = headline.label;
  $: anyRunning = auditRunBlocksStart(runByProfile?.fast) || auditRunBlocksStart(runByProfile?.standard);
  $: fastRunCopy = runCopy("fast", runByProfile?.fast);
  $: standardRunCopy = runCopy("standard", runByProfile?.standard);

  function runCopy(profile, run) {
    if (!run) return "";
    if (run.monitoringState === "unknown") return `${profile === "fast" ? "Fast local refresh" : "Standard audit"} status unavailable · audit may still be running`;
    if (run.executionState === "running") return `${profile === "fast" ? "Fast local refresh" : "Standard audit"} running`;
    if (run.executionState === "failed") return `${profile === "fast" ? "Fast local refresh" : "Standard audit"} execution failed`;
    return `${profile === "fast" ? "Fast local refresh" : "Standard audit"} completed · report ${run.reportStatus}`;
  }
</script>

<section class="audit-console audit-overview" aria-labelledby="audit-overview-heading">
  <div class="audit-section-head">
    <div>
      <p class="audit-kicker">Production health / standard profile</p>
      <h2 id="audit-overview-heading">Operational truth, not activity.</h2>
    </div>
    <div class="audit-actions">
      <button class="btn-ghost" type="button" disabled={!authEnabled || loading || anyRunning} on:click={() => onRun("fast")}>Fast local refresh</button>
      <button class="btn-primary" type="button" disabled={!authEnabled || loading || anyRunning} on:click={() => onRun("standard")}>Run standard audit</button>
    </div>
  </div>

  <div class="audit-live" role="status" aria-live="polite">
    {#if loading}Loading saved audit reports…{/if}
    {#if error}Audit reports could not be loaded: {error}{/if}
    {#if !authEnabled && !loading}Audit view unavailable because web authentication is disabled.{/if}
    {#if fastRunCopy}{fastRunCopy}.{/if}
    {#if standardRunCopy}{standardRunCopy}.{/if}
  </div>

  <div class="audit-overview-grid">
    <article class="audit-health-core" data-status={currentStatus}>
      <span class="audit-signal" aria-hidden="true">{currentStatus === "pass" ? "●" : currentStatus === "fail" ? "×" : currentStatus === "warn" ? "▲" : "?"}</span>
      <div>
        <span class="audit-label">Current whole-system health</span>
        <strong>{stateLabel}</strong>
        {#if headline.state === "unavailable"}
          <small>Audit reports are unavailable until web authentication is enabled.</small>
        {:else if headline.state === "error"}
          <small>The standard report request failed; this is not evidence that no report exists.</small>
        {:else if headline.state === "loading"}
          <small>Reading the latest saved standard report. No audit has been started.</small>
        {:else if health?.state === "stale"}
          <small>The saved standard report is retained below, but it no longer establishes current health.</small>
        {:else if health?.state === "absent"}
          <small>No standard report has been persisted yet.</small>
        {:else if health?.state === "unknown"}
          <small>The report is not a current unfiltered whole-system standard audit.</small>
        {:else}
          <small>Based only on the current standard audit report.</small>
        {/if}
      </div>
    </article>

    {#if loadState === "ready"}
      <div class="audit-overview-facts">
        <article class="audit-fact"><span>Last standard audit</span><strong>{formatTime(overview?.completedAt)}</strong><small>{overview?.auditID || "No saved report"}</small></article>
        <article class="audit-fact"><span>Last successful sync</span><strong>{formatTime(overview?.lastSyncAt)}</strong><small>scheduler.latest_sync</small></article>
        <article class="audit-fact"><span>Runtime boundary</span><strong>{overview?.layout || "unknown"}</strong><small>{overview?.configVerified ? "Configuration verified" : "Configuration not verified"}</small></article>
        <article class="audit-fact"><span>Build</span><strong>{overview?.build?.version || "unknown"}</strong><small>{overview?.build?.commit || "Commit unknown"}</small></article>
      </div>
    {:else}
      <div class="audit-overview-facts audit-facts-unavailable">
        <article class="audit-fact"><span>Standard report metadata</span><strong>Unavailable</strong><small>No absence or health claim is made until the saved-report read succeeds.</small></article>
      </div>
    {/if}
  </div>

  <div class="audit-profile-strip">
    <div><span>Standard authority</span><strong>{loadState === "ready" ? (standardEnvelope?.report ? `${standardEnvelope.report.status} · ${standardEnvelope.freshness?.status || "unknown"}` : "absent") : headline.state}</strong></div>
    <div><span>Fast local refresh</span><strong>{fastEnvelope?.report ? `${fastEnvelope.report.status} · ${formatTime(fastEnvelope.report.completed_at)}` : "not run"}</strong><small>Local evidence only; never replaces standard health.</small></div>
  </div>
</section>
