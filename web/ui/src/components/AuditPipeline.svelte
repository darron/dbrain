<script>
  export let stages = [];
  const labels = { hydration: "Hydration", extraction: "Extraction", summary: "Summary", transcription: "Audio → text", ocr: "Photo OCR" };
  function count(value) { return value == null ? "?" : value.toLocaleString(); }
  function age(value) { return value == null ? "unknown" : value < 3600 ? `${Math.round(value / 60)}m` : `${(value / 3600).toFixed(value < 36000 ? 1 : 0)}h`; }
</script>

<section class="audit-console" aria-labelledby="audit-pipeline-heading">
  <div class="audit-section-head">
    <div><p class="audit-kicker">Pipeline partitions</p><h2 id="audit-pipeline-heading">Every row has one outcome.</h2></div>
    <p class="audit-section-note">Terminal work is intentionally separate from failure.</p>
  </div>
  {#if stages.length === 0}
    <p class="audit-empty">No pipeline partition checks are present in this standard report.</p>
  {:else}
    <div class="audit-pipeline-grid">
      {#each stages as stage}
        <article class="audit-stage" data-status={stage.status}>
          <div class="audit-card-title"><span class="status-mark">{stage.status === "pass" ? "●" : stage.status === "fail" ? "×" : stage.status === "warn" ? "▲" : stage.status === "skipped" ? "—" : "?"}</span><strong>{labels[stage.stage] || stage.stage}</strong><small>{stage.status} · {stage.partitionValid ? "partition valid" : "partition unknown"}</small></div>
          <div class="audit-counts" aria-label={`${labels[stage.stage] || stage.stage} outcome counts`}>
            {#each ["current", "pending", "blocked", "terminal", "failed", "unknown"] as outcome}
              <div class={`count-${outcome}`}><span>{outcome}</span><strong>{count(stage.counts[outcome])}</strong></div>
            {/each}
          </div>
          <small>Oldest pending: {age(stage.oldestPendingAgeSeconds)} · total {count(stage.counts.total)}</small>
        </article>
      {/each}
    </div>
  {/if}
</section>
