<script>
  import { onMount } from "svelte";

  import AddLinkPanel from "./components/AddLinkPanel.svelte";
  import AskPanel from "./components/AskPanel.svelte";
  import DetailPanel from "./components/DetailPanel.svelte";
  import OperationsPanel from "./components/OperationsPanel.svelte";
  import ResultList from "./components/ResultList.svelte";
  import SearchPanel from "./components/SearchPanel.svelte";
  import StatsBar from "./components/StatsBar.svelte";
  import { addLink, askEvidence, getBootstrap, getLookup, getSourceActivity, searchBrain } from "./lib/api.js";
  import { pageHref, readRouteState, writeRouteState } from "./lib/urlState.js";

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

  const defaultSourceActivity = {
    window: "24h",
    recent_successes: [],
    recent_failures: [],
    failure_hotspots: [],
    failure_kinds: [],
    failure_statuses: [],
    failure_domains: [],
    failure_table: [],
    failure_table_total: 0,
    failure_table_offset: 0,
    failure_table_limit: 8,
    failure_table_sort: "newest",
    trend_bucket: "",
    trend: []
  };

  const defaultSourceActivityFilters = {
    sourceType: "",
    domain: "",
    status: "",
    failureKind: "",
    message: "",
    window: "24h",
    limit: 8,
    failureOffset: 0,
    failureSort: "newest"
  };

  let mounted = false;
  let currentPage = "home";
  let bootstrapState = "idle";
  let sourceActivityState = "idle";

  let app = {
    name: "dbrain",
    root_dir: "",
    vault_dir: "",
    db_path: "",
    has_fts: false
  };
  let backlog = { ...defaultBacklog };
  let activity = { ...defaultActivity };
  let sourceActivity = { ...defaultSourceActivity };
  let sourceActivityFilters = { ...defaultSourceActivityFilters };
  let bootstrapError = "";
  let sourceActivityError = "";

  let searchQuery = "";
  let searchState = "idle";
  let searchError = "";
  let searchResults = [];

  let askQuestion = "";
  let askState = "idle";
  let askError = "";
  let askResponse = { question: "", answer: "", evidence: [] };

  let linkURL = "";
  let linkState = "idle";
  let linkError = "";
  let linkResponse = null;

  let selectedLookup = "";
  let detailState = "idle";
  let detailError = "";
  let detail = null;

  $: showSearchResults = searchState !== "idle";
  $: showAskResults = askState !== "idle";
  $: showLinkResults = linkState !== "idle";
  $: showDetailPanel = detailState !== "idle" || Boolean(detailError) || Boolean(detail);
  $: showHomeGuide = currentPage === "home" && !showSearchResults && !showAskResults && !showLinkResults && !showDetailPanel;

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
      await runSearch();
    }
    if (currentPage === "home" && askQuestion) {
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
      activityOffset:
        onAdminPage && sourceActivityFilters.failureOffset !== defaultSourceActivityFilters.failureOffset
          ? String(sourceActivityFilters.failureOffset)
          : "",
      activitySort:
        onAdminPage && sourceActivityFilters.failureSort !== defaultSourceActivityFilters.failureSort
          ? sourceActivityFilters.failureSort
          : "",
      activityWindow:
        onAdminPage && sourceActivityFilters.window !== defaultSourceActivityFilters.window
          ? sourceActivityFilters.window
          : "",
      activityLimit:
        onAdminPage && sourceActivityFilters.limit !== defaultSourceActivityFilters.limit
          ? String(sourceActivityFilters.limit)
          : ""
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
      if (first) {
        await loadDetail(first.source_key);
      }
    } catch (error) {
      linkError = error.message;
      linkState = "error";
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

  function heroCopy() {
    if (currentPage === "admin") {
      return {
        eyebrow: "Admin Surface",
        lede: "Backlog, activity, failures, and source operations live here so the homepage stays focused on retrieval."
      };
    }
    return {
      eyebrow: "Local Brain Surface",
      lede: "Search exact notes or ask for evidence from the live brain database. The homepage is optimized for retrieval first."
    };
  }

  function isDefaultSourceActivityFilters(filters) {
    return (
      !filters.sourceType &&
      !filters.domain &&
      !filters.status &&
      !filters.failureKind &&
      !filters.message &&
      String(filters.window || defaultSourceActivityFilters.window) === defaultSourceActivityFilters.window &&
      Number(filters.limit) === defaultSourceActivityFilters.limit &&
      Number(filters.failureOffset) === defaultSourceActivityFilters.failureOffset &&
      String(filters.failureSort || defaultSourceActivityFilters.failureSort) === defaultSourceActivityFilters.failureSort
    );
  }

  function normalizeActivityLimit(value) {
    const parsed = Number.parseInt(String(value || defaultSourceActivityFilters.limit), 10);
    if (Number.isNaN(parsed) || parsed <= 0) {
      return defaultSourceActivityFilters.limit;
    }
    return parsed;
  }

  function normalizeActivityOffset(value) {
    const parsed = Number.parseInt(String(value || defaultSourceActivityFilters.failureOffset), 10);
    if (Number.isNaN(parsed) || parsed < 0) {
      return defaultSourceActivityFilters.failureOffset;
    }
    return parsed;
  }

  function normalizeActivitySort(value) {
    switch (String(value || "").trim()) {
      case "oldest":
      case "domain":
      case "kind":
      case "status":
        return String(value).trim();
      default:
        return defaultSourceActivityFilters.failureSort;
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
  <title>{currentPage === "admin" ? "dbrain admin" : "dbrain"}</title>
</svelte:head>

<div class="page">
  <header class="hero">
    <div>
      <p class="eyebrow">{heroCopy().eyebrow}</p>
      <h1>{app.name}</h1>
      <p class="lede">{heroCopy().lede}</p>
    </div>

    <div class="hero-side stack-sm">
      <nav aria-label="Primary" class="primary-nav">
        <a class:active={currentPage === "home"} class="nav-pill" href={pageHref("home")}>
          Search + Ask
        </a>
        <a class:active={currentPage === "admin"} class="nav-pill" href={pageHref("admin")}>
          Admin
        </a>
      </nav>

      <div class="hero-meta">
        <span>{app.has_fts ? "FTS enabled" : "LIKE search fallback"}</span>
        {#if currentPage === "admin"}
          <span>{app.db_path}</span>
        {/if}
      </div>
    </div>
  </header>

  {#if bootstrapError}
    <section class="banner error">{bootstrapError}</section>
  {/if}

  <main class="page-content">
    {#if currentPage === "admin"}
      <section class="admin-shell stack">
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
          <DetailPanel
            {detailState}
            {detailError}
            {detail}
            onSelect={loadDetail}
          />
        {/if}
      </section>
    {:else}
      <section class="home-shell stack">
        <div class="focus-grid">
          <SearchPanel bind:query={searchQuery} state={searchState} onSearch={runSearch} />

          <AskPanel bind:question={askQuestion} state={askState} onAsk={runAsk} />

          <AddLinkPanel
            bind:url={linkURL}
            state={linkState}
            error={linkError}
            result={linkResponse}
            onAdd={runAddLink}
            onSelect={loadDetail}
          />
        </div>

        {#if showHomeGuide}
          <section class="panel stack home-guide">
            <div class="panel-header">
              <div>
                <p class="panel-kicker">Start Here</p>
                <h2>Use search for exact matches and ask for clustered evidence.</h2>
              </div>
            </div>
            <p class="message muted">
              Search is the fastest way to jump to a saved note, URL, repo, or author. Ask pulls a
              small evidence pack from the local graph without forcing you into the admin console.
            </p>
          </section>
        {/if}

        {#if showSearchResults || showAskResults}
          <div class="results-grid">
            {#if showSearchResults}
              <section class="panel stack result-stage">
                <div class="panel-header">
                  <div>
                    <p class="panel-kicker">Search Results</p>
                    <h2>{searchQuery || "Search"}</h2>
                  </div>
                  {#if searchState === "ready"}
                    <p class="message muted">{searchResults.length} matches</p>
                  {/if}
                </div>

                {#if searchState === "loading"}
                  <p class="message muted">Searching the brain...</p>
                {:else if searchError}
                  <p class="message error">{searchError}</p>
                {:else if searchState === "ready"}
                  <ResultList
                    items={searchResults}
                    selectedLookup={selectedLookup}
                    onSelect={loadDetail}
                    emptyMessage="No matching notes yet."
                  />
                {/if}
              </section>
            {/if}

            {#if showAskResults}
              <section class="panel stack result-stage">
                <div class="panel-header">
                  <div>
                    <p class="panel-kicker">Ask Results</p>
                    <h2>{askResponse.question || askQuestion || "Evidence"}</h2>
                  </div>
                  {#if askState === "ready"}
                    <p class="message muted">{(askResponse.evidence || []).length} evidence items</p>
                  {/if}
                </div>

                {#if askState === "loading"}
                  <p class="message muted">Retrieving evidence from local notes and sources...</p>
                {:else if askError}
                  <p class="message error">{askError}</p>
                {:else if askState === "ready"}
                  {#if askResponse.answer}
                    <div class="answer-card">
                      <p class="panel-kicker">Answer</p>
                      <p>{askResponse.answer}</p>
                    </div>
                  {/if}

                  <p class="message muted">
                    Evidence first. Open a result to inspect the stored note and linked sources.
                  </p>

                  <ResultList
                    items={askResponse.evidence || []}
                    selectedLookup={selectedLookup}
                    onSelect={loadDetail}
                    emptyMessage="No evidence matched that question yet."
                  />
                {/if}
              </section>
            {/if}
          </div>
        {/if}

        {#if showDetailPanel}
          <DetailPanel
            {detailState}
            {detailError}
            {detail}
            onSelect={loadDetail}
          />
        {/if}
      </section>
    {/if}
  </main>
</div>
