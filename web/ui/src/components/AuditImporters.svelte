<script>
  import { formatTime } from "../lib/time.js";
  export let importers = [];

  const labels = {
    apple_notes: "Apple Notes", safari_tabs: "Safari tabs", x_bookmarks: "X bookmarks",
    github_stars: "GitHub stars", youtube_liked: "YouTube liked", youtube_watch_later: "YouTube watch later", feeds: "Feeds"
  };

  function duration(value) {
    if (value == null) return "unknown";
    if (value < 3600) return `${Math.round(value / 60)}m`;
    if (value < 86400) return `${Math.round(value / 3600)}h`;
    return `${Math.round(value / 86400)}d`;
  }
</script>

<section class="audit-console" aria-labelledby="audit-importers-heading">
  <div class="audit-section-head">
    <div><p class="audit-kicker">Import cadence</p><h2 id="audit-importers-heading">Polling is not arrival.</h2></div>
    <p class="audit-section-note">A quiet source can still be polled successfully.</p>
  </div>
  {#if importers.length === 0}
    <p class="audit-empty">No importer checks are present in this standard report.</p>
  {:else}
    <div class="audit-importer-grid">
      {#each importers as importer}
        <article class="audit-importer" data-status={importer.poll?.status || "unknown"}>
          <div class="audit-card-title"><span class="status-mark">{importer.poll?.status === "pass" ? "●" : importer.poll?.status === "fail" ? "×" : importer.poll?.status === "warn" ? "▲" : importer.poll?.status === "skipped" ? "—" : "?"}</span><strong>{labels[importer.source] || importer.source}</strong><small>{importer.poll?.status || "unknown"}</small></div>
          <dl>
            <div><dt>Successful poll</dt><dd>{formatTime(importer.poll?.succeededAt)}</dd></div>
            <div><dt>Poll age</dt><dd>{duration(importer.poll?.ageSeconds)}</dd></div>
            <div><dt>Latest arrival gap</dt><dd>{duration(importer.arrivals?.quietSeconds)}</dd></div>
          </dl>
          <small>Arrival history is informational; silence does not imply a failed poll.</small>
        </article>
      {/each}
    </div>
  {/if}
</section>
