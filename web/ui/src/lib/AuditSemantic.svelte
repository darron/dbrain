<script>
  import { formatTime } from "./time.js";

  export let semantic = { state: "legacy", current: null, latest: null, stages: [] };

  const labels = { projection: "Projection", embedding: "Embedding", flush: "Flush", compaction: "Compaction", verification: "Verification", readiness: "Readiness" };
  const marks = { succeeded: "●", failed: "×", canceled: "×", skipped: "—", unknown: "?" };
  function count(value) { return value == null ? "?" : value.toLocaleString(); }
  function duration(value) { return value == null ? "unknown" : value < 60 ? `${value}s` : value < 3600 ? `${Math.round(value / 60)}m` : `${(value / 3600).toFixed(1)}h`; }
  function stateLabel(value) { return String(value || "unknown").replaceAll("_", " "); }
</script>

<section class="audit-console audit-semantic" aria-labelledby="audit-semantic-heading">
  <div class="audit-section-head">
    <div><p class="audit-kicker">Semantic retrieval / v2 evidence</p><h2 id="audit-semantic-heading">Readiness is health; refreshes are activity.</h2></div>
    <p class="audit-section-note">Successful zero-work remains a healthy refresh.</p>
  </div>

  {#if semantic.state === "legacy"}
    <p class="audit-empty">Semantic audit unavailable in this legacy report.</p>
  {:else}
    <div class="semantic-health-grid">
      <article class="semantic-health-card" data-status={semantic.current?.readiness === "ready" ? "pass" : semantic.current?.readiness === "catching_up" ? "warn" : "unknown"}>
        <span class="audit-label">Current readiness</span>
        <strong>{stateLabel(semantic.current?.readiness)}</strong>
        <small>{semantic.current?.configured === true ? "Configured" : semantic.current?.configured === false ? "Not configured" : "Configuration unknown"} · capability {stateLabel(semantic.current?.capability)} · backend {stateLabel(semantic.current?.backend)} · audit {semantic.state}</small>
      </article>
      <article class="semantic-health-card">
        <span class="audit-label">Profile / generation</span>
        <strong class="semantic-identifier">{semantic.current?.profileID || "none"}</strong>
        <small class="semantic-identifier">generation {semantic.current?.activeGenerationID || "none"}</small>
      </article>
      <article class="semantic-health-card" data-status={semantic.latest?.refreshState === "failed" || semantic.latest?.refreshState === "canceled" ? "fail" : semantic.latest?.refreshState === "succeeded" ? "pass" : "unknown"}>
        <span class="audit-label">Latest refresh</span>
        <strong>{stateLabel(semantic.latest?.refreshState)}</strong>
        <small>{duration(semantic.latest?.ageSeconds)} old · {duration(semantic.latest?.durationSeconds)} duration{semantic.latest?.errorCode ? ` · ${semantic.latest.errorCode}` : ""}</small>
      </article>
    </div>

    <div class="semantic-activity-grid">
      <article class="semantic-panel">
        <span class="audit-label">Current debt</span>
        <dl class="semantic-facts">
          {#each [["dirty", semantic.current?.debt?.dirty_parent_count], ["pending", semantic.current?.debt?.pending_parent_count], ["due", semantic.current?.debt?.due_embedding_count], ["blocked", semantic.current?.debt?.blocked_embedding_count], ["failed", semantic.current?.debt?.failed_embedding_count]] as [label, value]}
            <div><dt>{label}</dt><dd>{count(value)}</dd></div>
          {/each}
        </dl>
      </article>
      <article class="semantic-panel">
        <span class="audit-label">Index shape</span>
        <dl class="semantic-facts">
          {#each [["indexed", semantic.current?.shape?.indexed_vector_count], ["L0", semantic.current?.shape?.l0_vector_count], ["tombstones", semantic.current?.shape?.tombstone_count], ["segments", semantic.current?.shape?.segment_count]] as [label, value]}
            <div><dt>{label}</dt><dd>{count(value)}</dd></div>
          {/each}
        </dl>
      </article>
      <article class="semantic-panel">
        <span class="audit-label">Latest-run work</span>
        <dl class="semantic-facts">
          {#each [["projected", semantic.latest?.counts?.projectedParents], ["embedded", semantic.latest?.counts?.embeddedChunks], ["flushed", semantic.latest?.counts?.flushedVectors], ["compacted", semantic.latest?.counts?.compactedVectors], ["verified", semantic.latest?.counts?.verifiedVectors], ["successors", semantic.latest?.counts?.successorRuns]] as [label, value]}
            <div><dt>{label}</dt><dd>{count(value)}</dd></div>
          {/each}
        </dl>
      </article>
    </div>

    <div class="semantic-stage-grid" aria-label="Latest semantic refresh stages">
      {#each semantic.stages as stage}
        <article class="semantic-stage" data-status={stage.status === "succeeded" ? "pass" : stage.status === "failed" || stage.status === "canceled" ? "fail" : stage.status}><span class="status-mark">{marks[stage.status] || "?"}</span><strong>{labels[stage.stage] || stage.stage}</strong><small>{stage.status} · {duration(stage.durationSeconds)}</small></article>
      {/each}
    </div>
    {#if semantic.latest?.startedAt || semantic.latest?.completedAt || semantic.latest?.failureAt}
      <p class="semantic-timestamps">Started {formatTime(semantic.latest?.startedAt)} · completed {formatTime(semantic.latest?.completedAt)}{semantic.latest?.failureAt ? ` · failed ${formatTime(semantic.latest.failureAt)}` : ""}</p>
    {/if}
  {/if}
</section>
