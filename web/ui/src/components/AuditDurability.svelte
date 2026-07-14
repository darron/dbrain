<script>
  export let cards = [];
  const groups = [
    { kind: "media", label: "Media archive" },
    { kind: "sqlite", label: "SQLite backups" },
    { kind: "okf", label: "OKF export" }
  ];
  function evidenceHeadline(card) {
    if (card.id === "durability.media_local_coverage") return `${card.evidence.eligible_local_count ?? "?"} local eligible · ${card.evidence.orphan_count ?? "?"} orphaned`;
    if (card.id === "durability.media_remote") return `${card.evidence.checked_count ?? "?"} / ${card.evidence.population_count ?? "?"} checked`;
    if (card.id === "durability.sqlite_backup_configuration") return String(card.evidence.configuration_state || "unknown").replaceAll("_", " ");
    if (card.id === "durability.sqlite_backup_age") return `${card.evidence.archive_count ?? "?"} archives · age ${card.evidence.latest_age_seconds ?? "?"}s`;
    if (card.id === "durability.okf_freshness") return `${card.evidence.age_seconds ?? "?"}s since export`;
    if (card.id === "durability.okf_validation") return `${card.evidence.document_count ?? "?"} docs · ${card.evidence.validation_error_count ?? "?"} errors`;
    return "Evidence unavailable";
  }
</script>

<section class="audit-console" aria-labelledby="audit-durability-heading">
  <div class="audit-section-head">
    <div><p class="audit-kicker">Durability</p><h2 id="audit-durability-heading">Local authority, remote recovery.</h2></div>
    <p class="audit-section-note">Exact audit checks only—no inference from activity counters.</p>
  </div>
  <div class="audit-durability-grid">
    {#each groups as group}
      <article class="audit-durability-group">
        <h3>{group.label}</h3>
        {#each cards.filter((card) => card.kind === group.kind) as card}
          <div class="audit-durability-check" data-status={card.status}>
            <span class="status-mark">{card.status === "pass" ? "●" : card.status === "fail" ? "×" : card.status === "warn" ? "▲" : "?"}</span>
            <div><strong>{card.label}</strong><p>{evidenceHeadline(card)}</p><small>{card.id}</small></div>
          </div>
        {:else}
          <p class="audit-empty compact">No checks reported.</p>
        {/each}
      </article>
    {/each}
  </div>
</section>
