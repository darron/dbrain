<script>
  export let query = "";
  export let state = "idle";
  export let onSearch = () => {};
</script>

<section class="panel stack focus-panel">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Search</p>
      <h2>Find the exact note, source, repo, or URL.</h2>
    </div>
  </div>

  <p class="message muted">
    Best when you already know the phrase, project name, author handle, or domain you want to
    inspect.
  </p>

  <form class="form" on:submit|preventDefault={onSearch}>
    <label>
      <span>Keyword query</span>
      <input bind:value={query} placeholder="agent memory, kubernetes, tailscale" />
    </label>
    <button type="submit" disabled={state === "loading"}>
      {state === "loading" ? "Searching..." : "Search"}
    </button>
  </form>
</section>
