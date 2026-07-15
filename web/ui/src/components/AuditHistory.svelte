<script>
  import { formatTime } from "../lib/time.js";
  export let history = [];
  export let loading = false;
  export let error = "";
</script>

<section class="audit-console" aria-labelledby="audit-history-heading">
  <div class="audit-section-head">
    <div><p class="audit-kicker">Standard audit history</p><h2 id="audit-history-heading">Regression and recovery timeline.</h2></div>
    <p class="audit-section-note">Fast local refreshes are deliberately excluded.</p>
  </div>
  {#if loading}
    <p class="audit-empty">Loading standard history…</p>
  {:else if error}
    <p class="audit-empty error">History could not be loaded: {error}</p>
  {:else if history.length === 0}
    <p class="audit-empty">No standard audit history is available yet.</p>
  {:else}
    <ol class="audit-history-list">
      {#each history as entry}
        <li data-status={entry.status}>
          <span class="status-mark">{entry.status === "pass" ? "●" : entry.status === "fail" ? "×" : entry.status === "warn" ? "▲" : "?"}</span>
          <div><strong>{entry.status.toUpperCase()}</strong><span class:recovered={entry.transition === "recovered"}>{entry.transition}</span><small>{formatTime(entry.completedAt)} · {entry.auditID}</small></div>
        </li>
      {/each}
    </ol>
  {/if}
</section>
