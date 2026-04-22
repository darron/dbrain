<script>
  export let items = [];
  export let selectedLookup = "";
  export let onSelect = () => {};
  export let emptyMessage = "No matching notes yet.";
</script>

<div class="result-list">
  {#if items.length === 0}
    <p class="message muted">{emptyMessage}</p>
  {/if}

  {#each items as item}
    <button
      class:selected={selectedLookup === item.source_key}
      class="result-card"
      on:click={() => onSelect(item.source_key)}
      type="button"
    >
      <span class="result-key">{item.source_key}</span>
      <strong>{item.title || item.canonical_url || item.url}</strong>
      <small>{item.canonical_url || item.url}</small>
      {#if item.summary}
        <p>{item.summary}</p>
      {:else if item.snippet}
        <p>{item.snippet}</p>
      {:else if item.excerpt}
        <p>{item.excerpt}</p>
      {/if}
    </button>
  {/each}
</div>
