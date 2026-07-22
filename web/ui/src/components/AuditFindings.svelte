<script>
  import { tick } from "svelte";
  import { formatTime } from "../lib/time.js";
  export let findings = [];

  let selectedID = "";
  let detailEl;
  $: if (findings.length && !findings.some((row) => row.id === selectedID)) selectedID = findings[0].id;
  $: selected = findings.find((row) => row.id === selectedID) || null;

  async function selectFinding(id) {
    selectedID = id;
    await tick();
    if (typeof window !== "undefined" && window.matchMedia("(max-width: 760px)").matches) {
      detailEl?.focus({ preventScroll: true });
      detailEl?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  }

  function evidenceLabel(key) { return key.replaceAll("_", " "); }
  function evidenceValue(value) {
    if (Array.isArray(value)) return `${value.length} bounded row${value.length === 1 ? "" : "s"}`;
    if (value && typeof value === "object") return Object.entries(value).map(([key, count]) => `${key}: ${count}`).join(" · ");
    if (typeof value === "boolean") return value ? "yes" : "no";
    return String(value);
  }
</script>

<section class="audit-console" aria-labelledby="audit-findings-heading">
  <div class="audit-section-head">
    <div><p class="audit-kicker">Action queue</p><h2 id="audit-findings-heading">Failed and unknown first.</h2></div>
    <p class="audit-section-note">Only typed, bounded evidence is rendered.</p>
  </div>
  {#if findings.length === 0}
    <p class="audit-empty good">No failed, unknown, or warning checks in this report.</p>
  {:else}
    <div class="audit-findings-layout">
      <ul class="audit-findings-list" aria-label="Audit findings">
        {#each findings as finding}
          <li>
            <button type="button" aria-pressed={finding.id === selectedID} class="audit-finding-row" class:selected={finding.id === selectedID} data-status={finding.status} on:click={() => selectFinding(finding.id)}>
              <span class="status-mark">{finding.status === "fail" ? "×" : finding.status === "warn" ? "▲" : "?"}</span>
              <span><strong>{finding.summary}</strong><small>{finding.id} · {finding.required ? "required" : "informational"}</small></span>
            </button>
          </li>
        {/each}
      </ul>
      <article class="audit-finding-detail" bind:this={detailEl} tabindex="-1">
        {#if selected}
          <div class="audit-card-title"><span class="status-mark">{selected.status === "fail" ? "×" : selected.status === "warn" ? "▲" : "?"}</span><strong>{selected.status.toUpperCase()} · {selected.confidence} confidence</strong></div>
          <h3>{selected.summary}</h3>
          <p class="audit-check-id">{selected.id}</p>
          <p class="audit-observed">Observed {formatTime(selected.observedAt)}</p>
          {#if selected.remediation}<div class="audit-remediation"><span>Remediation</span><p>{selected.remediation}</p></div>{/if}
          {#if selected.threshold}<p class="audit-threshold">Thresholds: warn {selected.threshold.warn_after_seconds ?? "?"}s · fail {selected.threshold.fail_after_seconds ?? "?"}s</p>{/if}
          <dl class="audit-evidence">
            {#each Object.entries(selected.evidence) as [key, value]}
              <div><dt>{evidenceLabel(key)}</dt><dd>{evidenceValue(value)}</dd></div>
            {/each}
          </dl>
        {/if}
      </article>
    </div>
  {/if}
</section>
