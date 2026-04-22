<script>
  import MarkdownView from "./MarkdownView.svelte";

  export let detailState = "idle";
  export let detailError = "";
  export let detail = null;
  export let onSelect = () => {};

  function detailTitle() {
    if (!detail) {
      return "Pick a note";
    }
    if (detail.kind === "item") {
      return detail.item?.title || detail.item?.source_key || "Item";
    }
    return detail.source?.title || detail.source?.source_key || "Source";
  }

  function canonicalURL() {
    if (!detail) {
      return "";
    }
    return detail.kind === "item" ? detail.item?.canonical_url : detail.source?.canonical_url;
  }

  function notePath() {
    if (!detail) {
      return "";
    }
    return detail.kind === "item" ? detail.item?.note_path : detail.source?.note_path;
  }
</script>

<section class="panel detail-panel">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Detail</p>
      <h2>{detailTitle()}</h2>
    </div>
  </div>

  {#if detailState === "loading"}
    <p class="message muted">Loading note...</p>
  {:else if detailError}
    <p class="message error">{detailError}</p>
  {:else if detail}
    <div class="detail-meta">
      {#if detail.kind === "item"}
        <span>{detail.item?.source_type}</span>
        <span>{detail.item?.canonical_url}</span>
        <span>{detail.linked_sources?.length || 0} linked sources</span>
      {:else}
        <span>{detail.source?.source_type}</span>
        <span>{detail.source?.canonical_url}</span>
        <span>{detail.backlinks?.length || 0} backlinks</span>
      {/if}
    </div>

    <div class="detail-actions">
      {#if canonicalURL()}
        <a class="link-chip" href={canonicalURL()} rel="noreferrer" target="_blank">Open original</a>
      {/if}
      {#if notePath()}
        <span class="link-chip muted-chip">{notePath()}</span>
      {/if}
    </div>

    {#if detail.kind === "item" && detail.linked_sources?.length}
      <section class="detail-section">
        <h3>Linked Sources</h3>
        <div class="relation-grid">
          {#each detail.linked_sources as ref}
            <button class="relation-card" on:click={() => onSelect(ref.source_key)} type="button">
              <span class="result-key">{ref.source_key}</span>
              <strong>{ref.title || ref.canonical_url}</strong>
              <small>{ref.canonical_url}</small>
            </button>
          {/each}
        </div>
      </section>
    {/if}

    {#if detail.kind === "source" && detail.backlinks?.length}
      <section class="detail-section">
        <h3>Backlinks</h3>
        <div class="relation-grid">
          {#each detail.backlinks as ref}
            <button class="relation-card" on:click={() => onSelect(ref.source_key)} type="button">
              <span class="result-key">{ref.source_key}</span>
              <strong>{ref.title || ref.canonical_url}</strong>
              <small>{ref.canonical_url}</small>
            </button>
          {/each}
        </div>
      </section>
    {/if}

    {#if detail.note_error}
      <p class="message error">{detail.note_error}</p>
    {/if}

    {#if detail.note_content}
      <MarkdownView markdown={detail.note_content} />
    {:else}
      <p class="message muted">No rendered note content for this record yet.</p>
    {/if}
  {:else}
    <p class="message muted">
      Select a search result or retrieved evidence card to inspect the stored note.
    </p>
  {/if}
</section>
