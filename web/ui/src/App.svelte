<script>
  import { onMount } from "svelte";

  import AskPanel from "./components/AskPanel.svelte";
  import DetailPanel from "./components/DetailPanel.svelte";
  import SearchPanel from "./components/SearchPanel.svelte";
  import StatsBar from "./components/StatsBar.svelte";
  import { askEvidence, getBootstrap, getLookup, searchBrain } from "./lib/api.js";
  import { readRouteState, writeRouteState } from "./lib/urlState.js";

  const defaultBacklog = {
    x_hydration_pending: 0,
    link_discovery_pending: 0,
    source_extraction_pending: 0,
    source_summary_pending: 0
  };

  const defaultActivity = {
    window: "24h",
    items_updated_in_window: 0,
    sources_updated_in_window: 0,
    sources_summarized_in_window: 0,
    latest_item_updated_at: "",
    latest_source_updated_at: "",
    latest_source_summary_at: ""
  };

  let mounted = false;

  let app = {
    name: "dbrain",
    root_dir: "",
    vault_dir: "",
    db_path: "",
    has_fts: false
  };
  let backlog = { ...defaultBacklog };
  let activity = { ...defaultActivity };
  let bootstrapError = "";

  let searchQuery = "";
  let searchState = "idle";
  let searchError = "";
  let searchResults = [];

  let askQuestion = "";
  let askState = "idle";
  let askError = "";
  let askResponse = { question: "", answer: "", evidence: [] };

  let selectedLookup = "";
  let detailState = "idle";
  let detailError = "";
  let detail = null;

  onMount(async () => {
    const route = readRouteState();
    searchQuery = route.q;
    askQuestion = route.ask;
    selectedLookup = route.lookup;
    mounted = true;

    await loadBootstrap();
    if (searchQuery) {
      await runSearch();
    }
    if (selectedLookup) {
      await loadDetail(selectedLookup);
    }
  });

  $: if (mounted) {
    writeRouteState({
      q: searchQuery,
      lookup: selectedLookup,
      ask: askQuestion
    });
  }

  async function loadBootstrap() {
    bootstrapError = "";
    try {
      const response = await getBootstrap();
      app = response.app;
      backlog = response.backlog;
      activity = response.activity;
    } catch (error) {
      bootstrapError = error.message;
    }
  }

  async function runSearch() {
    const query = searchQuery.trim();
    searchState = "loading";
    searchError = "";
    searchResults = [];
    if (!query) {
      searchState = "idle";
      return;
    }

    try {
      const response = await searchBrain(query, 12);
      searchResults = response.results ?? [];
      searchState = "ready";
    } catch (error) {
      searchError = error.message;
      searchState = "error";
    }
  }

  async function runAsk() {
    const question = askQuestion.trim();
    askState = "loading";
    askError = "";
    askResponse = { question, answer: "", evidence: [] };
    if (!question) {
      askState = "idle";
      return;
    }

    try {
      askResponse = await askEvidence(question, {
        limit: 8,
        include_related: true,
        related_limit: 2
      });
      askState = "ready";
    } catch (error) {
      askError = error.message;
      askState = "error";
    }
  }

  async function loadDetail(lookup) {
    selectedLookup = lookup;
    detailState = "loading";
    detailError = "";
    detail = null;

    try {
      detail = await getLookup(lookup);
      detailState = "ready";
    } catch (error) {
      detailError = error.message;
      detailState = "error";
    }
  }
</script>

<svelte:head>
  <title>dbrain</title>
</svelte:head>

<div class="page">
  <header class="hero">
    <div>
      <p class="eyebrow">Local Brain Surface</p>
      <h1>{app.name}</h1>
      <p class="lede">
        Read-only search, evidence retrieval, and note inspection on top of the
        live brain database.
      </p>
    </div>
    <div class="hero-meta">
      <span>{app.has_fts ? "FTS enabled" : "LIKE search fallback"}</span>
      <span>{app.db_path}</span>
    </div>
  </header>

  {#if bootstrapError}
    <section class="banner error">{bootstrapError}</section>
  {/if}

  <StatsBar {backlog} {activity} />

  <main class="workspace">
    <SearchPanel
      bind:query={searchQuery}
      state={searchState}
      error={searchError}
      results={searchResults}
      selectedLookup={selectedLookup}
      onSearch={runSearch}
      onSelect={loadDetail}
    />

    <AskPanel
      bind:question={askQuestion}
      state={askState}
      error={askError}
      response={askResponse}
      selectedLookup={selectedLookup}
      onAsk={runAsk}
      onSelect={loadDetail}
    />

    <DetailPanel
      {detailState}
      {detailError}
      {detail}
      onSelect={loadDetail}
    />
  </main>
</div>
