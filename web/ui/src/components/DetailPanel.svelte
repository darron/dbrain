<script>
  import MarkdownView from "./MarkdownView.svelte";

  export let detailState = "idle";
  export let detailError = "";
  export let detail = null;
  export let onSelect = () => {};

  $: titleText = !detail
    ? "Select a node"
    : detail.kind === "item"
      ? (detail.item?.title || detail.item?.source_key || "Item")
      : (detail.source?.title || detail.source?.source_key || "Source");

  $: canonURL = !detail
    ? ""
    : detail.kind === "item"
      ? detail.item?.canonical_url
      : detail.source?.canonical_url;

  $: npath = !detail
    ? ""
    : detail.kind === "item"
      ? detail.item?.note_path
      : detail.source?.note_path;

  $: stype = !detail
    ? ""
    : detail.kind === "item"
      ? detail.item?.source_type
      : detail.source?.source_type;

  $: linkedCount = !detail
    ? 0
    : detail.kind === "item"
      ? (detail.linked_sources?.length || 0)
      : (detail.backlinks?.length || 0);
</script>

<div class="detail-panel">
  <div class="panel-header">
    <div style="min-width:0">
      <p class="panel-kicker">Detail</p>
      <h2 style="overflow-wrap:anywhere">{titleText}</h2>
    </div>
  </div>

  {#if detailState === "loading"}
    <p class="message muted">Loading…</p>
  {:else if detailError}
    <p class="message error">{detailError}</p>
  {:else if detail}
    <div class="detail-meta">
      {#if stype}
        <span class="meta-chip type-badge type-{stype}">{stype}</span>
      {/if}
      {#if linkedCount > 0}
        <span class="meta-chip">
          {detail.kind === "item" ? linkedCount + " sources" : linkedCount + " backlinks"}
        </span>
      {/if}
    </div>

    <div class="detail-actions">
      {#if canonURL}
        <a class="link-chip" href={canonURL} rel="noopener noreferrer" target="_blank">
          ↗ Open original
        </a>
      {/if}
      {#if npath}
        <span class="link-chip muted-chip" title={npath}>
          {npath.split("/").slice(-1)[0]}
        </span>
      {/if}
    </div>

    {#if detail.kind === "item" && detail.linked_sources?.length}
      <div class="detail-section">
        <h3>Linked Sources ({detail.linked_sources.length})</h3>
        <div class="relation-grid">
          {#each detail.linked_sources as ref}
            <button class="relation-card" on:click={() => onSelect(ref.source_key)} type="button">
              <span class="result-key">{ref.source_type || "source"}</span>
              <strong>{ref.title || ref.canonical_url}</strong>
              <small>{ref.canonical_url}</small>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    {#if detail.kind === "source" && detail.backlinks?.length}
      <div class="detail-section">
        <h3>Referenced by ({detail.backlinks.length})</h3>
        <div class="relation-grid">
          {#each detail.backlinks as ref}
            <button class="relation-card" on:click={() => onSelect(ref.source_key)} type="button">
              <span class="result-key">{ref.source_type || "item"}</span>
              <strong>{ref.title || ref.canonical_url}</strong>
              {#if ref.author_handle}
                <small>@{ref.author_handle}</small>
              {/if}
            </button>
          {/each}
        </div>
      </div>
    {/if}

    {#if detail.note_error}
      <p class="message error">{detail.note_error}</p>
    {/if}

    {#if detail.note_content}
      <MarkdownView markdown={detail.note_content} />
    {:else}
      <p class="message muted">No rendered note for this record yet.</p>
    {/if}
  {:else}
    <p class="message muted">Select a result or graph node to inspect it.</p>
  {/if}
</div>
