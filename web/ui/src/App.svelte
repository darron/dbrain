<script>
  import { onMount, tick } from "svelte";

  import AddLinkPanel from "./components/AddLinkPanel.svelte";
  import DetailPanel from "./components/DetailPanel.svelte";
  import GraphView from "./components/GraphView.svelte";
  import MarkdownView from "./components/MarkdownView.svelte";
  import OperationsPanel from "./components/OperationsPanel.svelte";
  import ResultList from "./components/ResultList.svelte";
  import StatsBar from "./components/StatsBar.svelte";
  import { addLink, getBootstrap, getLookup, getSourceActivity, researchBrain, saveChatTranscript, searchBrain, synthesizeResearch } from "./lib/api.js";
  import { buildChatRetrievalQuestion, mergeResearchPackForChat, normalizeStoredChatSession } from "./lib/chat.js";
  import { normalizeLookupKey } from "./lib/sourceKeys.js";
  import { pageHref, readRouteState, writeRouteState } from "./lib/urlState.js";

  const defaultBacklog = { x_hydration_pending: 0, link_discovery_pending: 0, source_extraction_pending: 0, source_summary_pending: 0 };
  const defaultActivity = { window: "24h", items_updated_in_window: 0, sources_updated_in_window: 0, sources_summarized_in_window: 0, latest_item_updated_at: "", latest_source_updated_at: "", latest_source_summary_at: "" };
  const defaultSourceActivity = { window: "24h", recent_successes: [], recent_failures: [], failure_hotspots: [], failure_kinds: [], failure_statuses: [], failure_domains: [], failure_table: [], failure_table_total: 0, failure_table_offset: 0, failure_table_limit: 8, failure_table_sort: "newest", trend_bucket: "", trend: [] };
  const defaultSourceActivityFilters = { sourceType: "", domain: "", status: "", failureKind: "", message: "", window: "24h", limit: 8, failureOffset: 0, failureSort: "newest" };
  const chatStorageKey = "dbrain.web.chat.v1";

  let mounted = false;
  let currentPage = "home";
  let bootstrapState = "idle";
  let sourceActivityState = "idle";

  let app = { name: "dbrain", has_fts: false };
  let backlog = { ...defaultBacklog };
  let activity = { ...defaultActivity };
  let sourceActivity = { ...defaultSourceActivity };
  let sourceActivityFilters = { ...defaultSourceActivityFilters };
  let bootstrapError = "";
  let sourceActivityError = "";

  // Search/research state
  let inputMode = "search"; // "search" | "research" | "chat"
  let viewMode = "graph";   // "graph" | "list"
  let searchQuery = "";
  let searchState = "idle";
  let searchError = "";
  let searchResults = [];

  let researchDraft = "";
  let researchQuestion = "";
  let researchState = "idle";
  let researchError = "";
  let researchPack = { question: "", evidence: [], coverage: {}, query_plan: {}, next_steps: [] };
  let synthesisEnabled = true;
  let synthesisState = "idle";
  let synthesisError = "";
  let synthesisAnswer = "";
  let synthesisStart = null;
  let synthesisDone = null;
  let synthesisCitations = [];
  let researchController = null;
  let synthesisController = null;

  let chatDraft = "";
  let chatTurns = [];
  let chatState = "idle";
  let chatError = "";
  let chatController = null;
  let pinnedEvidenceKeys = [];
  let chatSaveState = "idle";
  let chatSaveError = "";
  let chatSavedPath = "";

  // Graph state
  let graphNodes = [];
  let graphEdges = [];

  // Detail state
  let selectedLookup = "";
  let detailState = "idle";
  let detailError = "";
  let detail = null;
  let detailPanelEl;
  let chatBottomEl;

  // Add-link panel
  let showAddLink = false;
  let linkURL = "";
  let linkState = "idle";
  let linkError = "";
  let linkResponse = null;

  $: currentChatTurn = chatTurns[chatTurns.length - 1] || null;
  $: chatBusy = chatState === "researching" || chatState === "synthesizing";
  $: chatVisibleEvidence = currentChatTurn?.research_pack?.evidence || [];
  $: activeResults = inputMode === "search" ? searchResults : inputMode === "research" ? (researchPack.evidence || []) : chatVisibleEvidence;
  $: activeState = inputMode === "search" ? searchState : inputMode === "research" ? researchState : chatBusy ? "loading" : chatState === "error" ? "error" : chatTurns.length > 0 ? "ready" : "idle";
  $: activeError = inputMode === "search" ? searchError : inputMode === "research" ? researchError : chatError;
  $: hasResults = inputMode !== "chat" && activeState === "ready" && activeResults.length > 0;
  $: showDetailPanel = Boolean(detail || detailState !== "idle" || detailError);
  $: synthesisWarnings = synthesisDone?.answer_warnings || synthesisStart?.answer_warnings || [];
  $: visibleCitations = synthesisDone?.citations || synthesisCitations || [];
  $: synthesisWarningMessages = synthesisWarnings.map(formatSynthesisWarning);
  $: appVersion = app?.version || {};
  $: releaseVersion = cleanVersionValue(appVersion.release_version);
  $: moduleVersion = cleanVersionValue(appVersion.module_version);
  $: commitSHA = cleanVersionValue(appVersion.commit);
  $: shortSHA = cleanVersionValue(appVersion.short) || (commitSHA ? commitSHA.slice(0, 7) : "");
  $: gitDirty = String(appVersion.git_status || "").trim().toLowerCase() === "modified";
  $: releaseLabel = releaseVersion || moduleVersion || "";
  $: releaseURL = releaseVersion ? `https://github.com/darron/dbrain/releases/tag/${encodeURIComponent(releaseVersion)}` : "https://github.com/darron/dbrain/releases";
  $: commitURL = commitSHA ? `https://github.com/darron/dbrain/commit/${encodeURIComponent(commitSHA)}` : "";
  $: versionReady = bootstrapState === "ready" && Boolean(releaseLabel || shortSHA || gitDirty);

  onMount(async () => {
    const storedChat = loadChatSession();
    chatTurns = storedChat.turns;
    pinnedEvidenceKeys = storedChat.pinnedEvidenceKeys;

    const route = readRouteState();
    currentPage = route.page;
    searchQuery = route.q;
    researchDraft = route.research;
    researchQuestion = route.research;
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
    if (currentPage === "home" && researchQuestion) {
      inputMode = "research";
      await runResearch();
    }
    if (selectedLookup) {
      await loadDetail(selectedLookup);
    }
  });

  $: if (mounted) {
    persistChatSession();
  }

  $: if (mounted) {
    const onAdminPage = currentPage === "admin";
    writeRouteState({
      q: inputMode === "search" ? searchQuery : "",
      lookup: selectedLookup,
      research: inputMode === "research" ? researchQuestion : "",
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
    } else if (inputMode === "research") {
      await submitResearch();
    } else {
      await submitChat();
    }
  }

  async function submitResearch() {
    researchQuestion = researchDraft.trim();
    await runResearch(researchQuestion);
  }

  function urlToLookup(query) {
    try {
      const u = new URL(query);
      const host = u.hostname.replace(/^www\./, "");
      if (host === "x.com" || host === "twitter.com") {
        const m = u.pathname.match(/\/status\/(\d+)/);
        if (m) return "x:" + m[1];
      }
      if (host === "github.com") return query;
      if (host === "youtube.com" || host === "youtu.be") return query;
      return query; // generic URL — try as lookup
    } catch {
      return null; // not a URL
    }
  }

  async function runSearch() {
    const query = searchQuery.trim();
    searchState = "loading";
    searchError = "";
    searchResults = [];
    selectedLookup = "";
    detail = null;
    detailState = "idle";
    detailError = "";
    if (!query) {
      searchState = "idle";
      graphNodes = [];
      graphEdges = [];
      return;
    }

    // If the query looks like a URL, try a direct lookup first
    const lookup = urlToLookup(query);
    if (lookup) {
      await loadDetail(lookup);
      searchState = "ready";
      return;
    }

    try {
      const response = await searchBrain(query);
      searchResults = response.results ?? [];
      searchState = "ready";
      buildGraphFromResults(searchResults);
    } catch (error) {
      searchError = error.message;
      searchState = "error";
    }
  }

  async function runResearch(question = researchQuestion) {
    question = String(question || "").trim();
    researchQuestion = question;
    researchDraft = question;
    abortResearch();
    abortSynthesis();
    researchState = "loading";
    researchError = "";
    researchPack = { question, evidence: [], coverage: {}, query_plan: {}, next_steps: [] };
    resetSynthesis();
    selectedLookup = "";
    detail = null;
    detailState = "idle";
    detailError = "";
    if (!question) {
      researchState = "idle";
      return;
    }
    researchController = new AbortController();
    const controller = researchController;
    try {
      researchPack = await researchBrain(question, { signal: controller.signal });
      researchState = "ready";
      if (synthesisEnabled) {
        await runSynthesis(question, researchPack);
      }
    } catch (error) {
      if (error.name !== "AbortError") {
        researchError = error.message;
        researchState = "error";
      }
    } finally {
      if (researchController === controller) {
        researchController = null;
      }
    }
  }

  function resetSynthesis() {
    synthesisState = "idle";
    synthesisError = "";
    synthesisAnswer = "";
    synthesisStart = null;
    synthesisDone = null;
    synthesisCitations = [];
  }

  function abortSynthesis() {
    if (synthesisController) {
      synthesisController.abort();
      synthesisController = null;
    }
  }

  function abortResearch() {
    if (researchController) {
      researchController.abort();
      researchController = null;
    }
  }

  async function handleSynthesisToggle() {
    if (!synthesisEnabled) {
      abortSynthesis();
      resetSynthesis();
      return;
    }
    if (researchState === "ready" && researchPack?.schema_version) {
      await runSynthesis(researchQuestion.trim(), researchPack);
    }
  }

  async function runSynthesis(question, pack) {
    abortSynthesis();
    resetSynthesis();
    synthesisState = "loading";
    synthesisController = new AbortController();
    const controller = synthesisController;
    try {
      await synthesizeResearch(question, pack, {
        signal: controller.signal,
        onEvent: (event, payload) => {
          if (event === "start") {
            synthesisStart = payload;
          } else if (event === "answer") {
            synthesisAnswer = payload.text || "";
          } else if (event === "citation") {
            synthesisCitations = [...synthesisCitations, payload];
          } else if (event === "done") {
            synthesisDone = payload;
            synthesisState = "ready";
            synthesisCitations = payload.citations || synthesisCitations;
          } else if (event === "error") {
            synthesisError = payload.error || "Synthesis failed";
            synthesisDone = payload;
            synthesisState = "error";
          }
        }
      });
      if (synthesisState === "loading") {
        synthesisState = "ready";
      }
    } catch (error) {
      if (error.name !== "AbortError") {
        synthesisError = error.message;
        synthesisState = "error";
      }
    } finally {
      if (synthesisController === controller) {
        synthesisController = null;
      }
    }
  }

  async function submitChat() {
    const question = chatDraft.trim();
    if (!question || chatBusy) return;
    chatDraft = "";
    await runChatTurn(question);
  }

  async function runChatTurn(question) {
    abortChat();
    chatError = "";
    chatSaveError = "";
    chatSavedPath = "";
    chatSaveState = "idle";
    const priorTurns = chatTurns;
    const id = newID("chat");
    const retrievalQuestion = buildChatRetrievalQuestion(question, priorTurns, pinnedEvidenceKeys);
    const createdAt = new Date().toISOString();
    const turn = {
      id,
      question,
      retrieval_question: retrievalQuestion,
      status: "researching",
      answer: "",
      research_pack: { question: retrievalQuestion, evidence: [], coverage: {}, query_plan: {}, next_steps: [] },
      start: null,
      done: null,
      citations: [],
      error: "",
      created_at: createdAt
    };
    chatTurns = [...chatTurns, turn];
    chatState = "researching";
    void scrollChatBottomIntoView();

    chatController = new AbortController();
    const controller = chatController;
    try {
      const pack = await researchBrain(retrievalQuestion, { signal: controller.signal });
      const mergedPack = mergeResearchPackForChat(question, pack, priorTurns, pinnedEvidenceKeys);
      updateChatTurn(id, { research_pack: mergedPack, status: "synthesizing" });
      chatState = "synthesizing";

      await synthesizeResearch(question, mergedPack, {
        signal: controller.signal,
        onEvent: (event, payload) => {
          if (event === "start") {
            updateChatTurn(id, { start: payload });
          } else if (event === "answer") {
            updateChatTurn(id, { answer: payload.text || "" });
          } else if (event === "citation") {
            const current = chatTurns.find((candidate) => candidate.id === id);
            updateChatTurn(id, { citations: [...(current?.citations || []), payload] });
          } else if (event === "done") {
            updateChatTurn(id, {
              done: payload,
              citations: payload.citations || chatTurns.find((candidate) => candidate.id === id)?.citations || [],
              status: "ready"
            });
            chatState = "ready";
          } else if (event === "error") {
            updateChatTurn(id, { done: payload, error: payload.error || "Synthesis failed", status: "error" });
            chatError = payload.error || "Synthesis failed";
            chatState = "error";
          }
        }
      });
      if (chatTurns.find((candidate) => candidate.id === id)?.status === "synthesizing") {
        updateChatTurn(id, { status: "ready" });
      }
      if (chatState === "synthesizing") {
        chatState = "ready";
      }
    } catch (error) {
      if (error.name !== "AbortError") {
        updateChatTurn(id, { error: error.message, status: "error" });
        chatError = error.message;
        chatState = "error";
      }
    } finally {
      if (chatController === controller) {
        chatController = null;
      }
    }
  }

  function updateChatTurn(id, patch) {
    chatTurns = chatTurns.map((turn) => turn.id === id ? { ...turn, ...patch } : turn);
  }

  function chatEvidence(turn) {
    return Array.isArray(turn?.research_pack?.evidence) ? turn.research_pack.evidence : [];
  }

  function chatWarnings(turn) {
    const warnings = turn?.done?.answer_warnings || turn?.start?.answer_warnings || [];
    return warnings.map(formatSynthesisWarning);
  }

  function chatCitations(turn) {
    return turn?.done?.citations || turn?.citations || [];
  }

  function togglePinnedEvidence(sourceKey) {
    if (!sourceKey) return;
    if (pinnedEvidenceKeys.includes(sourceKey)) {
      pinnedEvidenceKeys = pinnedEvidenceKeys.filter((key) => key !== sourceKey);
    } else {
      pinnedEvidenceKeys = [sourceKey, ...pinnedEvidenceKeys].slice(0, 24);
    }
  }

  function isPinnedEvidence(sourceKey) {
    return pinnedEvidenceKeys.includes(sourceKey);
  }

  function clearChat() {
    abortChat();
    chatTurns = [];
    pinnedEvidenceKeys = [];
    chatDraft = "";
    chatError = "";
    chatSaveError = "";
    chatSavedPath = "";
    chatSaveState = "idle";
    chatState = "idle";
  }

  async function saveCurrentChatTranscript() {
    if (chatTurns.length === 0 || chatSaveState === "loading") return;
    chatSaveState = "loading";
    chatSaveError = "";
    chatSavedPath = "";
    try {
      const response = await saveChatTranscript({
        turns: chatTurns,
        pinned_evidence_keys: pinnedEvidenceKeys,
        selected_lookup: selectedLookup
      });
      chatSavedPath = response.path || "";
      chatSaveState = "ready";
    } catch (error) {
      chatSaveError = error.message;
      chatSaveState = "error";
    }
  }

  function abortChat() {
    if (chatController) {
      chatController.abort();
      chatController = null;
    }
  }

  async function scrollChatBottomIntoView() {
    if (inputMode !== "chat") return;
    await tick();
    chatBottomEl?.scrollIntoView({ behavior: "smooth", block: "end" });
  }

  function loadChatSession() {
    if (typeof sessionStorage === "undefined") return { turns: [], pinnedEvidenceKeys: [] };
    try {
      const raw = sessionStorage.getItem(chatStorageKey);
      if (!raw) return { turns: [], pinnedEvidenceKeys: [] };
      return normalizeStoredChatSession(JSON.parse(raw));
    } catch {
      return { turns: [], pinnedEvidenceKeys: [] };
    }
  }

  function persistChatSession() {
    if (typeof sessionStorage === "undefined") return;
    const value = {
      turns: chatTurns.slice(-8),
      pinnedEvidenceKeys: pinnedEvidenceKeys.slice(0, 24)
    };
    try {
      sessionStorage.setItem(chatStorageKey, JSON.stringify(value));
    } catch {
      // Ignore full or unavailable session storage; chat still works for the current render.
    }
  }

  function newID(prefix) {
    if (typeof crypto !== "undefined" && crypto.randomUUID) {
      return `${prefix}:${crypto.randomUUID()}`;
    }
    return `${prefix}:${Date.now().toString(36)}:${Math.random().toString(36).slice(2)}`;
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
    const normalizedLookup = normalizeLookupKey(lookup);
    if (!normalizedLookup) return;
    selectedLookup = normalizedLookup;
    detailState = "loading";
    detailError = "";
    detail = null;
    void scrollDetailIntoViewOnMobile();

    try {
      detail = await getLookup(normalizedLookup);
      detailState = "ready";
      if (inputMode === "search") {
        expandGraphFromDetail(detail);
      }
      void scrollDetailIntoViewOnMobile();
    } catch (error) {
      detailError = error.message;
      detailState = "error";
      void scrollDetailIntoViewOnMobile();
    }
  }

  async function scrollDetailIntoViewOnMobile() {
    if (typeof window === "undefined") return;
    if (!window.matchMedia("(max-width: 760px)").matches) return;
    await tick();
    detailPanelEl?.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  async function searchFor(term) {
    inputMode = "search";
    searchQuery = term;
    await runSearch();
  }

  function evidencePreview(evidence) {
    return truncateText(evidence?.summary || evidence?.excerpt || "", 280);
  }

  function truncateText(value, max) {
    const text = String(value || "").replace(/\s+/g, " ").trim();
    if (text.length <= max) return text;
    return text.slice(0, max).trimEnd() + "…";
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

  function formatSynthesisWarning(warning) {
    switch (warning) {
      case "evidence_truncated":
        return "Evidence was capped to the highest-ranked working set; use the cited sources or follow-up research to expand the context.";
      case "model_unavailable":
        return "No local synthesis model is configured, so only retrieved evidence is shown.";
      case "model_error":
        return "The local synthesis model failed after evidence retrieval completed.";
      case "no_evidence":
        return "No matching evidence was found for this question.";
      default:
        return warning;
    }
  }

  function cleanVersionValue(value) {
    value = String(value || "").trim();
    if (!value || value.toLowerCase() === "unknown" || value === "(devel)") {
      return "";
    }
    return value;
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
        <div class="search-controls">
          <div class="search-tabs">
            <button class="search-tab" class:active={inputMode === "search"} on:click={() => { inputMode = "search"; }} type="button">
              Search
            </button>
            <button class="search-tab" class:active={inputMode === "research"} on:click={() => { inputMode = "research"; }} type="button">
              Research
            </button>
            <button class="search-tab" class:active={inputMode === "chat"} on:click={() => { inputMode = "chat"; }} type="button">
              Chat
            </button>
          </div>

          {#if inputMode === "search" && hasResults}
            <div class="view-toggle">
              <button class="view-btn" class:active={viewMode === "graph"} on:click={() => (viewMode = "graph")} type="button">
                Graph
              </button>
              <button class="view-btn" class:active={viewMode === "list"} on:click={() => (viewMode = "list")} type="button">
                List
              </button>
            </div>
          {/if}
          {#if inputMode === "research"}
            <label class="synthesis-toggle">
              <input type="checkbox" bind:checked={synthesisEnabled} on:change={handleSynthesisToggle} />
              <span>Synthesize answer</span>
            </label>
          {/if}
          {#if inputMode === "chat" && chatTurns.length > 0}
            <button class="btn-ghost compact-action" type="button" disabled={chatSaveState === "loading"} on:click={saveCurrentChatTranscript}>
              {chatSaveState === "loading" ? "Saving…" : "Save transcript"}
            </button>
            <button class="btn-ghost compact-action" type="button" on:click={clearChat}>
              New chat
            </button>
          {/if}
        </div>

        {#if inputMode !== "chat" || chatTurns.length === 0}
          <!-- Visible search bar on home; active chat moves its composer into the thread. -->
          <form class="search-bar" on:submit|preventDefault={handleSubmit}>
            {#if inputMode === "search"}
              <input
                class="search-input"
                bind:value={searchQuery}
                placeholder="agent memory, kubernetes, tailscale…"
              />
            {:else}
              {#if inputMode === "research"}
                <input
                  class="search-input"
                  bind:value={researchDraft}
                  placeholder="What do I have on agent memory?"
                />
              {:else}
                <input
                  class="search-input"
                  bind:value={chatDraft}
                  placeholder="Ask a follow-up about your brain…"
                />
              {/if}
            {/if}
            <button class="search-btn" type="submit" disabled={activeState === "loading"}>
              {activeState === "loading" ? "…" : inputMode === "search" ? "Search" : inputMode === "research" ? "Research" : "Send"}
            </button>
          </form>
        {/if}

        {#if inputMode === "chat" && pinnedEvidenceKeys.length > 0}
          <div class="chat-pins" aria-label="Pinned chat evidence">
            <span>Context pins</span>
            {#each pinnedEvidenceKeys as sourceKey}
              <button class="citation-chip" type="button" on:click={() => loadDetail(sourceKey)}>
                {sourceKey}
              </button>
            {/each}
            <button class="btn-ghost compact-action" type="button" on:click={() => (pinnedEvidenceKeys = [])}>
              Clear pins
            </button>
          </div>
        {/if}

        {#if inputMode === "chat" && (chatSavedPath || chatSaveError)}
          <p class="chat-save-note" class:error={chatSaveError}>
            {chatSaveError ? chatSaveError : `Saved transcript: ${chatSavedPath}`}
          </p>
        {/if}

        {#if activeState === "ready" && hasResults}
          <div class="result-status">
            <span class="result-count">{activeResults.length} results</span>
            {#if inputMode === "research" && researchPack.coverage?.recall_note}
              <span class="coverage-note">· {researchPack.coverage.recall_note}</span>
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
          {#if inputMode === "chat"}
            <h2>Start a session to research, clarify, and iterate against your local brain.</h2>
            <p class="message muted" style="margin-top:0.5rem">
              Chat history stays in this browser session. Evidence carries forward; previous model answers do not.
            </p>
          {:else}
            <h2>Search for exact matches. Research to pull an evidence pack.</h2>
            <p class="message muted" style="margin-top:0.5rem">
              Results appear as a navigable graph — click nodes to expand their relationships.
            </p>
          {/if}
        </div>
      {:else if hasResults || showDetailPanel || activeState === "loading" || (inputMode === "research" && activeState === "ready") || (inputMode === "chat" && chatTurns.length > 0)}
        <div class="content-area" class:has-detail={showDetailPanel}>
          <div class="content-main">
            {#if inputMode === "chat"}
              <div class="chat-thread">
                <div class="chat-thread-header">
                  <div>
                    <p class="panel-kicker" style="margin:0">Chat Research</p>
                    <p class="message muted">Each turn retrieves a fresh pack and synthesizes locally with prior evidence context.</p>
                  </div>
                  {#if chatTurns.length > 0}
                    <span>{chatTurns.length} turns</span>
                  {/if}
                </div>

                {#each chatTurns as turn}
                  <article class="chat-turn">
                    <div class="chat-question">
                      <span>You</span>
                      <p>{turn.question}</p>
                    </div>

                    <div class="answer-card synthesis-card chat-answer">
                      <div class="synthesis-header">
                        <p class="panel-kicker" style="margin:0">dbrain</p>
                        <span>{turn.done?.model || turn.start?.model || turn.status}</span>
                      </div>

                      {#if turn.status === "researching"}
                        <p class="message muted">Retrieving evidence from the local brain…</p>
                      {:else if turn.status === "synthesizing" && !turn.answer}
                        <p class="message muted">Generating local answer…</p>
                      {:else if turn.status === "error"}
                        <p class="message error">{turn.error || "Chat turn failed"}</p>
                      {/if}

                      {#if turn.answer}
                        <MarkdownView
                          markdown={turn.answer}
                          linkSourceKeys={true}
                          showSourceKeyPins={true}
                          sourceKeyPinVersion={pinnedEvidenceKeys.join("|")}
                          isSourceKeyPinned={isPinnedEvidence}
                          onLookup={loadDetail}
                          onPinSourceKey={togglePinnedEvidence}
                        />
                      {/if}

                      {#if chatWarnings(turn).length > 0}
                        <div class="synthesis-warnings">
                          {#each chatWarnings(turn) as message}
                            <p class="message muted">{message}</p>
                          {/each}
                        </div>
                      {/if}

                      {#if chatCitations(turn).length > 0}
                        <div class="citation-chips" aria-label="Chat citations">
                          {#each chatCitations(turn) as citation}
                            <span class="citation-pin-pair">
                              <button class="citation-chip" type="button" on:click={() => loadDetail(citation.source_key)} title={citation.title || citation.note_path || citation.source_key}>
                                {citation.source_key}
                              </button>
                              <button class="pin-chip tiny" class:active={isPinnedEvidence(citation.source_key)} type="button" on:click={() => togglePinnedEvidence(citation.source_key)}>
                                Pin
                              </button>
                            </span>
                          {/each}
                        </div>
                      {/if}
                    </div>

                    {#if chatEvidence(turn).length > 0}
                      <details class="chat-evidence-details">
                        <summary>
                          <span>
                            <span class="panel-kicker" style="margin:0">Evidence Used</span>
                            <small>{chatEvidence(turn).length} rows with excerpts and pin controls</small>
                          </span>
                        </summary>
                        <div class="evidence-compact-list">
                          {#each chatEvidence(turn) as evidence}
                            <div class="evidence-card chat-evidence-card" class:selected={selectedLookup === evidence.source_key}>
                              <button class="evidence-card-main" type="button" on:click={() => loadDetail(evidence.source_key)}>
                                <span class="result-key">{evidence.source_type || evidence.kind || evidence.relationship || "source"}</span>
                                <strong>{evidence.title || evidence.url || evidence.source_key}</strong>
                                {#if evidencePreview(evidence)}
                                  <p>{evidencePreview(evidence)}</p>
                                {/if}
                              </button>
                              <button class="pin-chip" class:active={isPinnedEvidence(evidence.source_key)} type="button" on:click={() => togglePinnedEvidence(evidence.source_key)}>
                                Pin
                              </button>
                            </div>
                          {/each}
                        </div>
                      </details>
                    {/if}
                  </article>
                {/each}

                <form class="chat-composer" on:submit|preventDefault={submitChat} bind:this={chatBottomEl}>
                  <input
                    class="search-input"
                    bind:value={chatDraft}
                    placeholder={chatBusy ? "Waiting for this turn…" : "Ask a follow-up…"}
                    disabled={chatBusy}
                  />
                  <button class="search-btn" type="submit" disabled={chatBusy || !chatDraft.trim()}>
                    {chatBusy ? "…" : "Send"}
                  </button>
                </form>
              </div>
            {:else if inputMode === "research"}
              <div class="research-summary">
                <div class="answer-card">
                  <p class="panel-kicker" style="margin:0">Research Pack</p>
                  {#if researchState === "loading"}
                    <p class="message muted">Retrieving evidence from the local brain…</p>
                  {:else if researchState === "ready"}
                    <p>{researchPack.coverage?.recall_note || "Retrieved evidence from the local brain."}</p>
                    {#if researchPack.topic_brief?.summary}
                      <p>{researchPack.topic_brief.summary}</p>
                    {/if}
                    {#if !hasResults}
                      <p class="message muted">No evidence matched this question. Try a narrower phrase, a known tag, or a source type.</p>
                    {/if}
                  {:else}
                    <p class="message muted">Submit a question to retrieve an evidence pack.</p>
                  {/if}
                </div>

                {#if synthesisEnabled && researchState !== "loading"}
                  <div class="answer-card synthesis-card">
                    <div class="synthesis-header">
                      <p class="panel-kicker" style="margin:0">Synthesis</p>
                      {#if synthesisStart?.model || synthesisDone?.model}
                        <span>{synthesisDone?.model || synthesisStart?.model}</span>
                      {/if}
                    </div>
                    {#if synthesisState === "loading"}
                      <p class="message muted">Generating local answer…</p>
                    {:else if synthesisState === "error"}
                      <p class="message error">{synthesisError}</p>
                    {:else if synthesisDone?.answer_status === "no_evidence"}
                      <p class="message muted">No model call was made because the research pack has no evidence.</p>
                    {:else if synthesisAnswer}
                      <MarkdownView markdown={synthesisAnswer} linkSourceKeys={true} onLookup={loadDetail} />
                    {:else}
                      <p class="message muted">Synthesis is enabled for this question.</p>
                    {/if}

                    {#if synthesisWarningMessages.length > 0}
                      <div class="synthesis-warnings">
                        {#each synthesisWarningMessages as message}
                          <p class="message muted">{message}</p>
                        {/each}
                      </div>
                    {/if}
                    {#if visibleCitations.length > 0}
                      <div class="citation-chips" aria-label="Synthesis citations">
                        {#each visibleCitations as citation}
                          <button class="citation-chip" type="button" on:click={() => loadDetail(citation.source_key)} title={citation.title || citation.note_path || citation.source_key}>
                            {citation.source_key}
                          </button>
                        {/each}
                      </div>
                    {/if}
                  </div>
                {/if}

                {#if researchState === "ready" && hasResults}
                  <div class="evidence-compact">
                    <div class="evidence-compact-header">
                      <p class="panel-kicker" style="margin:0">Evidence</p>
                      <span>{activeResults.length} results</span>
                    </div>
                    <div class="evidence-compact-list">
                      {#each activeResults as evidence}
                        <button class="evidence-card" class:selected={selectedLookup === evidence.source_key} type="button" on:click={() => loadDetail(evidence.source_key)}>
                          <span class="result-key">{evidence.source_type || evidence.kind || "source"}</span>
                          <strong>{evidence.title || evidence.url || evidence.source_key}</strong>
                          {#if evidencePreview(evidence)}
                            <p>{evidencePreview(evidence)}</p>
                          {/if}
                        </button>
                      {/each}
                    </div>
                  </div>
                {/if}
              </div>
            {:else if viewMode === "graph"}
              <div class="graph-area">
                <GraphView
                  nodes={graphNodes}
                  edges={graphEdges}
                  selectedId={selectedLookup}
                  onSelect={loadDetail}
                />
              </div>
            {:else}
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
            <div class="content-detail" bind:this={detailPanelEl}>
              <DetailPanel
                {detailState}
                {detailError}
                {detail}
                onSelect={loadDetail}
                onSearch={searchFor}
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

  <footer class="version-footer" aria-label="Build version">
    {#if versionReady}
      <span>dbrain</span>
      {#if releaseLabel}
        <a href={releaseURL} target="_blank" rel="noreferrer">{releaseLabel}</a>
      {:else}
        <a href="https://github.com/darron/dbrain/releases" target="_blank" rel="noreferrer">releases</a>
      {/if}
      {#if commitURL}
        <span aria-hidden="true">·</span>
        <a href={commitURL} target="_blank" rel="noreferrer">{shortSHA}</a>
      {:else if shortSHA}
        <span aria-hidden="true">·</span>
        <span>{shortSHA}</span>
      {/if}
      {#if gitDirty}
        <span class="dirty">dirty</span>
      {/if}
    {/if}
  </footer>
</div>
