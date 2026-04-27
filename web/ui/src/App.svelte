<script>
  import { onMount } from "svelte";

  import AddLinkPanel from "./components/AddLinkPanel.svelte";
  import AskPanel from "./components/AskPanel.svelte";
  import DetailPanel from "./components/DetailPanel.svelte";
  import GraphView from "./components/GraphView.svelte";
  import OperationsPanel from "./components/OperationsPanel.svelte";
  import ResultList from "./components/ResultList.svelte";
  import StatsBar from "./components/StatsBar.svelte";
  import { addLink, askEvidence, getBootstrap, getLookup, getSourceActivity, searchBrain } from "./lib/api.js";
  import { pageHref, readRouteState, writeRouteState } from "./lib/urlState.js";

  const defaultBacklog = { x_hydration_pending: 0, link_discovery_pending: 0, source_extraction_pending: 0, source_summary_pending: 0 };
  const defaultActivity = { window: "24h", items_updated_in_window: 0, sources_updated_in_window: 0, sources_summarized_in_window: 0, latest_item_updated_at: "", latest_source_updated_at: "", latest_source_summary_at: "" };
  const defaultSourceActivity = { window: "24h", recent_successes: [], recent_failures: [], failure_hotspots: [], failure_kinds: [], failure_statuses: [], failure_domains: [], failure_table: [], failure_table_total: 0, failure_table_offset: 0, failure_table_limit: 8, failure_table_sort: "newest", trend_bucket: "", trend: [] };
  const defaultSourceActivityFilters = { sourceType: "", domain: "", status: "", failureKind: "", message: "", window: "24h", limit: 8, failureOffset: 0, failureSort: "newest" };

  let mounted = false;
  let currentPage = "home";
  let bootstrapState = "idle";
  let sourceActivityState = "idle";

  let app = { name: "dbrain", root_dir: "", vault_dir: "", db_path: "", has_fts: false };
  let backlog = { ...defaultBacklog };
  let activity = { ...defaultActivity };
  let sourceActivity = { ...defaultSourceActivity };
  let sourceActivityFilters = { ...defaultSourceActivityFilters };
  let bootstrapError = "";
  let sourceActivityError = "";

  // Search/ask state
  let inputMode = "search"; // "search" | "ask"
  let viewMode = "graph";   // "graph" | "list"
  let searchQuery = "";
  let searchState = "idle";
  let searchError = "";
  let searchResults = [];

  let askQuestion = "";
  let askState = "idle";
  let askError = "";
  let askResponse = { question: "", answer: "", evidence: [] };

  // Graph state
  let graphNodes = [];
  let graphEdges = [];

  // Detail state
  let selectedLookup = "";
  let detailState = "idle";
  let detailError = "";
  let detail = null;

  // Add-link panel
  let showAddLink = false;
  let linkURL = "";
  let linkState = "idle";
  let linkError = "";
  let linkResponse = null;

  $: activeResults = inputMode === "search" ? searchResults : (askResponse.evidence || []);
  $: activeState = inputMode === "search" ? searchState : askState;
  $: activeError = inputMode === "search" ? searchError : askError;
  $: hasResults = activeState === "ready" && activeResults.length > 0;
  $: showDetailPanel = Boolean(detail || detailState !== "idle" || detailError);

  onMount(async () => {
    const route = readRouteState();
    currentPage = route.page;
    searchQuery = route.q;
    askQuestion = route.ask;
    selectedLookup = route.lookup;
    sourceActivityFilters = {
      sourceType: route.activityType,
      domain: route.activityDomain,
      status: route.activityStatus,
      failureKind: route.activityFailureKind,
      message: route.activityMessage,
      window: route.activityWindow,
      limit: normalizeActivityLimit(route.activityLimit),
      failureOffset: normalizeActivityOffset(route.activityOffset),
      failureSort: normalizeActivitySort(route.activitySort)
    };
    mounted = true;

    await loadBootstrap();
    if (currentPage === "admin" && !isDefaultSourceActivityFilters(sourceActivityFilters)) {
      await loadSourceActivity();
    }
    if (currentPage === "home" && searchQuery) {
      inputMode = "search";
      await runSearch();
    }
    if (currentPage === "home" && askQuestion) {
      inputMode = "ask";
      await runAsk();
    }
    if (selectedLookup) {
      await loadDetail(selectedLookup);
    }
  });

  $: if (mounted) {
    const onAdminPage = currentPage === "admin";
    writeRouteState({
      q: searchQuery,
      lookup: selectedLookup,
      ask: askQuestion,
      activityDomain: onAdminPage ? sourceActivityFilters.domain : "",
      activityType: onAdminPage ? sourceActivityFilters.sourceType : "",
      activityStatus: onAdminPage ? sourceActivityFilters.status : "",
      activityFailureKind: onAdminPage ? sourceActivityFilters.failureKind : "",
      activityMessage: onAdminPage ? sourceActivityFilters.message : "",
      activityOffset: onAdminPage && sourceActivityFilters.failureOffset !== defaultSourceActivityFilters.failureOffset ? String(sourceActivityFilters.failureOffset) : "",
      activitySort: onAdminPage && sourceActivityFilters.failureSort !== defaultSourceActivityFilters.failureSort ? sourceActivityFilters.failureSort : "",
      activityWindow: onAdminPage && sourceActivityFilters.window !== defaultSourceActivityFilters.window ? sourceActivityFilters.window : "",
      activityLimit: onAdminPage && sourceActivityFilters.limit !== defaultSourceActivityFilters.limit ? String(sourceActivityFilters.limit) : ""
    });
  }

  async function loadBootstrap() {
    bootstrapState = "loading";
    bootstrapError = "";
    try {
      const response = await getBootstrap();
      app = response.app;
      backlog = response.backlog;
      activity = response.activity;
      if (isDefaultSourceActivityFilters(sourceActivityFilters)) {
        sourceActivity = response.source_activity ?? { ...defaultSourceActivity };
        sourceActivityState = "ready";
        sourceActivityError = "";
      }
      bootstrapState = "ready";
    } catch (error) {
      bootstrapError = error.message;
      bootstrapState = "error";
    }
  }

  async function loadSourceActivity() {
    sourceActivityState = "loading";
    sourceActivityError = "";
    try {
      sourceActivity = await getSourceActivity(sourceActivityFilters);
      sourceActivityState = "ready";
    } catch (error) {
      sourceActivityError = error.message;
      sourceActivityState = "error";
    }
  }

  async function refreshDashboard() {
    await loadBootstrap();
    if (!isDefaultSourceActivityFilters(sourceActivityFilters)) {
      await loadSourceActivity();
    }
  }

  async function applySourceActivityFilters(nextFilters) {
    sourceActivityFilters = normalizeActivityFilters(nextFilters);
    if (isDefaultSourceActivityFilters(sourceActivityFilters)) {
      await loadBootstrap();
      return;
    }
    await loadSourceActivity();
  }

  async function clearSourceActivityFilters() {
    sourceActivityFilters = { ...defaultSourceActivityFilters };
    await loadBootstrap();
  }

  async function applyHotspot(hotspot) {
    await applySourceActivityFilters({
      ...sourceActivityFilters,
      sourceType: hotspot.source_type || "",
      domain: hotspot.domain || "",
      status: hotspot.status || "",
      failureKind: hotspot.failure_kind || "",
      message: "",
      window: sourceActivity.window || sourceActivityFilters.window || defaultSourceActivityFilters.window,
      failureOffset: 0
    });
  }

  async function handleSubmit() {
    if (inputMode === "search") {
      await runSearch();
    } else {
      await runAsk();
    }
  }

  async function runSearch() {
    const query = searchQuery.trim();
    searchState = "loading";
    searchError = "";
    searchResults = [];
    if (!query) {
      searchState = "idle";
      graphNodes = [];
      graphEdges = [];
      return;
    }
    try {
      const response = await searchBrain(query, 16);
      searchResults = response.results ?? [];
      searchState = "ready";
      buildGraphFromResults(searchResults);
      // Clear detail if query changed
      if (selectedLookup && !searchResults.find(r => r.source_key === selectedLookup)) {
        selectedLookup = "";
        detail = null;
        detailState = "idle";
        detailError = "";
      }
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
      graphNodes = [];
      graphEdges = [];
      return;
    }
    try {
      askResponse = await askEvidence(question, { limit: 10, include_related: true, related_limit: 2 });
      askState = "ready";
      buildGraphFromResults(askResponse.evidence || []);
    } catch (error) {
      askError = error.message;
      askState = "error";
    }
  }

  function buildGraphFromResults(results) {
    graphNodes = results.map(r => ({
      id: r.source_key,
      title: r.title || r.canonical_url || r.source_key,
      source_type: r.source_type || guessTypeFromKey(r.source_key),
      canonical_url: r.canonical_url,
      is_secondary: false,
    }));
    graphEdges = [];
  }

  function guessTypeFromKey(key) {
    if (!key) return "web";
    if (key.startsWith("x:")) return "x_bookmark";
    if (key.includes("github.com")) return "github_star";
    if (key.includes("youtube.com") || key.includes("youtu.be")) return "youtube";
    return "web";
  }

  function expandGraphFromDetail(d) {
    if (!d) return;

    const primaryId = d.kind === "item"
      ? d.item?.source_key
      : d.source?.source_key;
    if (!primaryId) return;

    const newNodes = [];
    const newEdges = [];
    const existingIds = new Set(graphNodes.map(n => n.id));

    for (const ref of (d.linked_sources || [])) {
      if (!existingIds.has(ref.source_key)) {
        newNodes.push({
          id: ref.source_key,
          title: ref.title || ref.canonical_url || ref.source_key,
          source_type: ref.source_type || "web",
          canonical_url: ref.canonical_url,
          is_secondary: true,
        });
        existingIds.add(ref.source_key);
      }
      const edgeKey = primaryId + "->" + ref.source_key;
      if (!graphEdges.find(e => e.source + "->" + e.target === edgeKey)) {
        newEdges.push({ source: primaryId, target: ref.source_key, kind: "linked_source" });
      }
    }

    for (const ref of (d.backlinks || [])) {
      if (!existingIds.has(ref.source_key)) {
        newNodes.push({
          id: ref.source_key,
          title: ref.title || ref.canonical_url || ref.source_key,
          source_type: ref.source_type || "x_bookmark",
          canonical_url: ref.canonical_url,
          is_secondary: true,
        });
        existingIds.add(ref.source_key);
      }
      const edgeKey = ref.source_key + "->" + primaryId;
      if (!graphEdges.find(e => e.source + "->" + e.target === edgeKey)) {
        newEdges.push({ source: ref.source_key, target: primaryId, kind: "backlink" });
      }
    }

    if (newNodes.length > 0 || newEdges.length > 0) {
      graphNodes = [...graphNodes, ...newNodes];
      graphEdges = [...graphEdges, ...newEdges];
    }
  }

  async function loadDetail(lookup) {
    if (!lookup) return;
    selectedLookup = lookup;
    detailState = "loading";
    detailError = "";
    detail = null;

    try {
      detail = await getLookup(lookup);
      detailState = "ready";
      expandGraphFromDetail(detail);
    } catch (error) {
      detailError = error.message;
      detailState = "error";
    }
  }

  async function runAddLink() {
    const url = linkURL.trim();
    linkState = "loading";
    linkError = "";
    linkResponse = null;
    if (!url) {
      linkState = "idle";
      return;
    }
    try {
      linkResponse = await addLink(url);
      linkState = "ready";
      await loadBootstrap();
      const first = linkResponse?.results?.find((item) => item.source_key);
      if (first) await loadDetail(first.source_key);
    } catch (error) {
      linkError = error.message;
      linkState = "error";
    }
  }

  // ── Admin helpers ─────────────────────────────────────────────────

  function isDefaultSourceActivityFilters(filters) {
    return (
      !filters.sourceType && !filters.domain && !filters.status &&
      !filters.failureKind && !filters.message &&
      String(filters.window || defaultSourceActivityFilters.window) === defaultSourceActivityFilters.window &&
      Number(filters.limit) === defaultSourceActivityFilters.limit &&
      Number(filters.failureOffset) === defaultSourceActivityFilters.failureOffset &&
      String(filters.failureSort || defaultSourceActivityFilters.failureSort) === defaultSourceActivityFilters.failureSort
    );
  }

  function normalizeActivityLimit(value) {
    const parsed = Number.parseInt(String(value || defaultSourceActivityFilters.limit), 10);
    return Number.isNaN(parsed) || parsed <= 0 ? defaultSourceActivityFilters.limit : parsed;
  }

  function normalizeActivityOffset(value) {
    const parsed = Number.parseInt(String(value || defaultSourceActivityFilters.failureOffset), 10);
    return Number.isNaN(parsed) || parsed < 0 ? defaultSourceActivityFilters.failureOffset : parsed;
  }

  function normalizeActivitySort(value) {
    switch (String(value || "").trim()) {
      case "oldest": case "domain": case "kind": case "status": return String(value).trim();
      default: return defaultSourceActivityFilters.failureSort;
    }
  }

  function normalizeActivityFilters(filters) {
    return {
      sourceType: String(filters?.sourceType || "").trim(),
      domain: String(filters?.domain || "").trim(),
      status: String(filters?.status || "").trim(),
      failureKind: String(filters?.failureKind || "").trim(),
      message: String(filters?.message || "").trim(),
      window: String(filters?.window || defaultSourceActivityFilters.window).trim() || defaultSourceActivityFilters.window,
      limit: normalizeActivityLimit(filters?.limit),
      failureOffset: normalizeActivityOffset(filters?.failureOffset),
      failureSort: normalizeActivitySort(filters?.failureSort)
    };
  }
</script>

<svelte:head>
  <title>{currentPage === "admin" ? "dbrain · system" : "dbrain"}</title>
</svelte:head>

<div class="app">
  <!-- ── Header ──────────────────────────────────────────────── -->
  <header class="app-header">
    <a class="brand" href={pageHref("home")}>
      <span class="brand-dot"></span>
      dbrain
    </a>

    <nav class="header-nav">
      <a class="nav-link" class:active={currentPage === "home"} href={pageHref("home")}>Explore</a>
      <a class="nav-link" class:active={currentPage === "admin"} href={pageHref("admin")}>System</a>
    </nav>
  </header>

  <!-- ── Home page ───────────────────────────────────────────── -->
  {#if currentPage === "home"}
    <div class="page-home">
      <!-- Mode/view controls -->
      <div class="search-zone">
        <div style="display:flex;gap:0.5rem;align-items:center;justify-content:space-between;flex-wrap:wrap;">
          <div class="search-tabs">
            <button class="search-tab" class:active={inputMode === "search"} on:click={() => { inputMode = "search"; }} type="button">
              Search
            </button>
            <button class="search-tab" class:active={inputMode === "ask"} on:click={() => { inputMode = "ask"; }} type="button">
              Ask
            </button>
          </div>

          {#if hasResults}
            <div class="view-toggle">
              <button class="view-btn" class:active={viewMode === "graph"} on:click={() => (viewMode = "graph")} type="button">
                Graph
              </button>
              <button class="view-btn" class:active={viewMode === "list"} on:click={() => (viewMode = "list")} type="button">
                List
              </button>
            </div>
          {/if}
        </div>

        <!-- Visible search bar on home (desktop hides header one on mobile) -->
        <form class="search-bar" on:submit|preventDefault={handleSubmit}>
          {#if inputMode === "search"}
            <input
              class="search-input"
              bind:value={searchQuery}
              placeholder="agent memory, kubernetes, tailscale…"
            />
          {:else}
            <input
              class="search-input"
              bind:value={askQuestion}
              placeholder="What do I have on agent memory?"
            />
          {/if}
          <button class="search-btn" type="submit" disabled={activeState === "loading"}>
            {activeState === "loading" ? "…" : inputMode === "search" ? "Search" : "Ask"}
          </button>
        </form>

        {#if activeState === "ready" && hasResults}
          <div style="display:flex;align-items:center;gap:0.75rem;flex-wrap:wrap;">
            <span class="result-count">{activeResults.length} results</span>
            {#if inputMode === "ask" && askResponse.answer}
              <span style="font-size:0.8rem;color:var(--text-lo)">· evidence pack</span>
            {/if}
          </div>
        {/if}

        {#if activeError}
          <p class="message error">{activeError}</p>
        {/if}
      </div>

      <!-- Content area -->
      {#if activeState === "idle" && !showDetailPanel}
        <!-- Home guide -->
        <div class="home-guide">
          <p class="panel-kicker" style="margin-bottom:0.5rem">Local Brain Surface</p>
          <h2>Search for exact matches. Ask to pull evidence.</h2>
          <p class="message muted" style="margin-top:0.5rem">
            Results appear as a navigable graph — click nodes to expand their relationships.
          </p>
        </div>
      {:else if hasResults || showDetailPanel || activeState === "loading"}
        <div class="content-area" class:has-detail={showDetailPanel}>
          <div class="content-main">
            {#if viewMode === "graph"}
              <div class="graph-area">
                <GraphView
                  nodes={graphNodes}
                  edges={graphEdges}
                  selectedId={selectedLookup}
                  onSelect={loadDetail}
                />
              </div>
            {:else}
              {#if inputMode === "ask" && askResponse.answer}
                <div style="padding:1rem 1.25rem 0">
                  <div class="answer-card">
                    <p class="panel-kicker" style="margin:0">Answer</p>
                    <p>{askResponse.answer}</p>
                  </div>
                </div>
              {/if}
              <ResultList
                items={activeResults}
                selectedLookup={selectedLookup}
                onSelect={loadDetail}
                emptyMessage={activeState === "loading" ? "Loading…" : "No results yet."}
              />
            {/if}

            <!-- Add link section at bottom of content-main -->
            <div class="add-link-section" style="margin-top:auto">
              <button
                class="add-link-toggle"
                class:open={showAddLink}
                on:click={() => (showAddLink = !showAddLink)}
                type="button"
              >
                <span>+ Add Link</span>
                <span class="chevron">▾</span>
              </button>
              {#if showAddLink}
                <div class="add-link-form">
                  <AddLinkPanel
                    bind:url={linkURL}
                    state={linkState}
                    error={linkError}
                    result={linkResponse}
                    onAdd={runAddLink}
                    onSelect={loadDetail}
                  />
                </div>
              {/if}
            </div>
          </div>

          {#if showDetailPanel}
            <div class="content-detail">
              <DetailPanel
                {detailState}
                {detailError}
                {detail}
                onSelect={loadDetail}
              />
            </div>
          {/if}
        </div>
      {/if}
    </div>

  <!-- ── Admin page ──────────────────────────────────────────── -->
  {:else}
    <div class="page-admin">
      <header class="hero">
        <div>
          <p class="eyebrow">System</p>
          <h1>dbrain</h1>
          <p class="lede">Backlog, pipeline activity, and source operations.</p>
        </div>
        <div class="hero-side stack-sm">
          <nav aria-label="Primary" class="primary-nav">
            <a class:active={currentPage === "home"} class="nav-pill" href={pageHref("home")}>Explore</a>
            <a class:active={currentPage === "admin"} class="nav-pill" href={pageHref("admin")}>System</a>
          </nav>
          <div class="hero-meta">
            <span>{app.has_fts ? "FTS enabled" : "LIKE search"}</span>
            <span style="font-size:0.75rem;color:var(--text-dim)">{app.db_path}</span>
          </div>
        </div>
      </header>

      {#if bootstrapError}
        <div class="banner error">{bootstrapError}</div>
      {/if}

      <StatsBar {backlog} {activity} />

      <OperationsPanel
        activity={sourceActivity}
        filters={sourceActivityFilters}
        refreshing={bootstrapState === "loading" || sourceActivityState === "loading"}
        error={sourceActivityError}
        onRefresh={refreshDashboard}
        onApplyFilters={applySourceActivityFilters}
        onApplyHotspot={applyHotspot}
        onClearFilters={clearSourceActivityFilters}
        onSelect={loadDetail}
      />

      {#if showDetailPanel}
        <DetailPanel {detailState} {detailError} {detail} onSelect={loadDetail} />
      {/if}
    </div>
  {/if}
</div>
