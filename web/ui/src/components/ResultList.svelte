<script>
  export let items = [];
  export let selectedLookup = "";
  export let onSelect = () => {};
  export let emptyMessage = "No matching notes yet.";

  function typeLabel(item) {
    return item.source_type || "";
  }

  function snippet(item) {
    return item.summary || item.snippet || item.excerpt || "";
  }
</script>

<div class="result-list">
  {#if items.length === 0}
    <p class="message muted" style="padding:0.5rem 0">{emptyMessage}</p>
  {/if}

  {#each items as item}
    <button
      class="result-card"
      class:selected={selectedLookup === item.source_key}
      on:click={() => onSelect(item.source_key)}
      type="button"
    >
      <div style="display:flex;align-items:center;gap:0.5rem;flex-wrap:wrap;">
        {#if typeLabel(item)}
          <span class="type-badge type-{typeLabel(item)}">{typeLabel(item)}</span>
        {:else}
          <span class="result-key">{item.source_key}</span>
        {/if}
        {#if item.author_handle}
          <span style="font-size:0.75rem;color:var(--text-lo)">@{item.author_handle}</span>
        {/if}
        {#if item.primary_domain}
          <span style="font-size:0.75rem;color:var(--text-dim)">{item.primary_domain}</span>
        {/if}
      </div>
      <strong>{item.title || item.canonical_url || item.url || item.source_key}</strong>
      {#if item.canonical_url && item.canonical_url !== item.title}
        <small>{item.canonical_url}</small>
      {/if}
      {#if snippet(item)}
        <p>{snippet(item)}</p>
      {/if}
    </button>
  {/each}
</div>
