<script>
  export let url = "";
  export let state = "idle";
  export let result = null;
  export let error = "";
  export let onAdd = () => {};
  export let onSelect = () => {};
</script>

<section class="panel stack focus-panel add-link-panel">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Add Link</p>
      <h2>Queue a URL for extraction.</h2>
    </div>
  </div>

  <p class="message muted">
    Adds a source directly to the same backlog used by discovered links. Run the worker or sync
    pipeline to extract and summarize it.
  </p>

  <form class="form" on:submit|preventDefault={onAdd}>
    <label>
      <span>URL</span>
      <input bind:value={url} placeholder="https://example.com/article" />
    </label>
    <button disabled={state === "loading"} type="submit">
      {state === "loading" ? "Queueing..." : "Queue Link"}
    </button>
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
            <small>{item.source_created ? "new source" : "existing source"} · {item.source_type}</small>
          </button>
        {/if}
      {/each}
    </div>
  {/if}
</section>
