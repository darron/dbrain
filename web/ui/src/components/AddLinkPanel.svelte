<script>
  export let url = "";
  export let state = "idle";
  export let result = null;
  export let error = "";
  export let onAdd = () => {};
  export let onSelect = () => {};
</script>

<div class="stack">
  <p class="message muted" style="font-size:0.82rem">
    Queue a URL for extraction. Run the worker or sync pipeline to extract and summarize it.
  </p>

  <form class="form" on:submit|preventDefault={onAdd}>
    <div style="display:flex;gap:0.5rem;align-items:flex-end">
      <label style="flex:1;margin:0">
        <span>URL</span>
        <input bind:value={url} placeholder="https://example.com/article" />
      </label>
      <button class="btn-primary" disabled={state === "loading"} type="submit" style="flex-shrink:0;border-radius:var(--radius-sm)">
        {state === "loading" ? "…" : "Queue"}
      </button>
    </div>
  </form>

  {#if error}
    <p class="message error">{error}</p>
  {/if}

  {#if result?.results?.length}
    <div class="queued-links">
      {#each result.results as item}
        {#if item.error}
          <div class="queued-link-card failed">
            <span class="result-key">not queued</span>
            <strong>{item.url}</strong>
            <small>{item.error}</small>
          </div>
        {:else}
          <button class="queued-link-card" type="button" on:click={() => onSelect(item.source_key)}>
            <span class="result-key">{item.source_key}</span>
            <strong>{item.canonical_url}</strong>
            <small>{item.source_created ? "new" : "existing"} · {item.source_type}</small>
          </button>
        {/if}
      {/each}
    </div>
  {/if}
</div>
