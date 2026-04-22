<script>
  import ResultList from "./ResultList.svelte";

  export let query = "";
  export let state = "idle";
  export let error = "";
  export let results = [];
  export let selectedLookup = "";
  export let onSearch = () => {};
  export let onSelect = () => {};
</script>

<section class="panel stack">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Search</p>
      <h2>Browse the indexed brain</h2>
    </div>
  </div>

  <form class="form" on:submit|preventDefault={onSearch}>
    <label>
      <span>Keyword query</span>
      <input bind:value={query} placeholder="agent memory, kubernetes, tailscale" />
    </label>
    <button type="submit" disabled={state === "loading"}>
      {state === "loading" ? "Searching..." : "Search"}
    </button>
  </form>

  {#if error}
    <p class="message error">{error}</p>
  {/if}

  {#if state === "ready"}
    <ResultList
      items={results}
      selectedLookup={selectedLookup}
      onSelect={onSelect}
      emptyMessage="No matching notes yet."
    />
  {/if}
</section>
