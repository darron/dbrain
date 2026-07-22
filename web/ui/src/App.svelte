<script>
  import { onDestroy, onMount, tick } from "svelte";

  import AddLinkPanel from "./components/AddLinkPanel.svelte";
  import AuditDurability from "./components/AuditDurability.svelte";
  import AuditFindings from "./components/AuditFindings.svelte";
  import AuditHistory from "./components/AuditHistory.svelte";
  import AuditImporters from "./components/AuditImporters.svelte";
  import AuditOverview from "./components/AuditOverview.svelte";
  import AuditPipeline from "./components/AuditPipeline.svelte";
  import DetailPanel from "./components/DetailPanel.svelte";
  import GraphView from "./components/GraphView.svelte";
  import MarkdownView from "./components/MarkdownView.svelte";
  import MediaEvidenceBlock from "./components/MediaEvidenceBlock.svelte";
  import OperationsPanel from "./components/OperationsPanel.svelte";
  import ResultList from "./components/ResultList.svelte";
  import StatsBar from "./components/StatsBar.svelte";
  import { addLink, compareResearchTrace, createChatShare, getAuditHistory, getAuditLatest, getAuditRun, getBootstrap, getLookup, getSourceActivity, listChatShares, listResearchTraces, researchBrain, runResearch as runResearchRunner, saveChatTranscript, searchBrain, startAuditRun, synthesizeResearch } from "./lib/api.js";
  import { applyRunMonitoringUnknown, applyRunStatus, auditRunBlocksStart, freshnessDeadlineElapsed, freshnessRefreshDelayMs, markEnvelopeStale, overallHealth, runGenerationStableRead, selectDurability, selectFindings, selectHistory, selectImporters, selectOverview, selectPipeline } from "./lib/audit.js";
  import { buildChatRetrievalQuestion, buildChatTraceContinuity, mergeResearchPackForChat, normalizeStoredChatSession } from "./lib/chat.js";
  import { normalizeLookupKey } from "./lib/sourceKeys.js";
  import { formatSemanticDiagnostics } from "./lib/researchDiagnostics.js";
  import { formatTime } from "./lib/time.js";
  import { pageHref, readRouteState, writeRouteState } from "./lib/urlState.js";

  const defaultBacklog = { x_hydration_pending: 0, link_discovery_pending: 0, source_extraction_pending: 0, source_summary_pending: 0, drained: false, scope_description: "X hydration, link discovery, source extraction, and source summary only; this is not whole-system health." };
  const defaultActivity = { window: "24h", items_updated_in_window: 0, sources_updated_in_window: 0, sources_summarized_in_window: 0, latest_item_updated_at: "", latest_source_updated_at: "", latest_source_summary_at: "" };
  const defaultSourceActivity = { window: "24h", recent_successes: [], recent_failures: [], failure_hotspots: [], failure_kinds: [], failure_statuses: [], failure_domains: [], failure_table: [], failure_table_total: 0, failure_table_offset: 0, failure_table_limit: 8, failure_table_sort: "newest", trend_bucket: "", trend: [] };
  const defaultSourceActivityFilters = { sourceType: "", domain: "", status: "", failureKind: "", message: "", window: "24h", limit: 8, failureOffset: 0, failureSort: "newest" };
  const chatStorageKey = "dbrain.web.chat.v1";

  let mounted = false;
  let currentPage = "home";
  let bootstrapState = "idle";
  let sourceActivityState = "idle";

  let app = { name: "dbrain", has_fts: false };
  let auth = { enabled: false };
  let backlog = { ...defaultBacklog };
  let activity = { ...defaultActivity };
  let sourceActivity = { ...defaultSourceActivity };
  let sourceActivityFilters = { ...defaultSourceActivityFilters };
  let bootstrapError = "";
  let sourceActivityError = "";

  // Production-health audit state stays profile-separated by design.
  let auditLoadState = "idle";
  let auditLoadError = "";
  let auditHistoryState = "idle";
  let auditHistoryError = "";
  let auditActionError = "";
  let standardEnvelope = null;
  let fastEnvelope = null;
  let standardHistoryResponse = { profile: "standard", history: [] };
  let auditEnvelopeRevision = { fast: 0, standard: 0 };
  let auditHistoryRevision = 0;
  let runByProfile = { fast: null, standard: null };
  let auditStartBusy = false;
  let auditDisposed = false;
  let standardFreshnessTimer = null;
  let standardFreshnessObservedAt = 0;
  let standardRefreshBusy = false;
  let standardHistoryNeedsRefresh = false;
  let standardHistoryRetryTimer = null;
  let auditPollGeneration = 0;
  const auditController = new AbortController();
  const auditPollTimers = new Set();

  function assignAuditEnvelope(profile, envelope) {
    const normalizedProfile = profile === "fast" ? "fast" : "standard";
    if (normalizedProfile === "fast") fastEnvelope = envelope;
    else standardEnvelope = envelope;
    auditEnvelopeRevision = {
      ...auditEnvelopeRevision,
      [normalizedProfile]: auditEnvelopeRevision[normalizedProfile] + 1
    };
  }

  function assignStandardAuditHistory(response) {
    standardHistoryResponse = response;
    auditHistoryRevision += 1;
  }

  // Search/research state
  let inputMode = "chat"; // "search" | "research" | "chat" | "harness" | "shares"
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
  let chatShareByTurn = {};
  let chatShareStateByTurn = {};
  let chatShareErrorByTurn = {};
  let chatShareCopiedByTurn = {};
  let sharesState = "idle";
  let sharesError = "";
  let shares = [];
  let harnessState = "idle";
  let harnessError = "";
  let harnessTraces = [];
  let harnessCompareState = "idle";
  let harnessCompareError = "";
  let harnessComparison = null;
  let selectedTracePath = "";

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
  $: activeResults = inputMode === "search" ? searchResults : inputMode === "research" ? (researchPack.evidence || []) : inputMode === "chat" ? chatVisibleEvidence : [];
  $: activeState = inputMode === "search" ? searchState : inputMode === "research" ? researchState : inputMode === "shares" ? sharesState : inputMode === "harness" ? harnessState : chatBusy ? "loading" : chatState === "error" ? "error" : chatTurns.length > 0 ? "ready" : "idle";
  $: activeError = inputMode === "search" ? searchError : inputMode === "research" ? researchError : inputMode === "shares" ? sharesError : inputMode === "harness" ? harnessError : chatError;
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
  $: standardHealth = overallHealth(standardEnvelope);
  $: auditOverview = selectOverview(standardEnvelope);
  $: auditImporters = selectImporters(standardEnvelope?.report);
  $: auditPipeline = selectPipeline(standardEnvelope?.report);
  $: auditDurability = selectDurability(standardEnvelope?.report);
  $: auditFindings = selectFindings(standardEnvelope?.report);
  $: auditHistory = selectHistory(standardHistoryResponse);

  function isResearchRunnerFailure(stopReason) {
    return ["synthesis_unavailable", "synthesis_failed", "timeout_exceeded", "max_steps_reached", "trace_failed", "verification_failed"].includes(stopReason);
  }

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
    if (currentPage === "admin" && auth.enabled) {
      await loadAuditDashboard();
    } else if (currentPage === "admin") {
      auditLoadState = "unavailable";
    }
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

  onDestroy(() => {
    auditDisposed = true;
    auditPollGeneration += 1;
    auditController.abort();
    clearTimeout(standardFreshnessTimer);
    clearTimeout(standardHistoryRetryTimer);
    clearAuditPollTimers();
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
      auth = response.auth || { enabled: false };
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

  async function loadAuditDashboard() {
    if (!auth.enabled) {
      auditLoadState = "unavailable";
      return;
    }
    auditLoadState = "loading";
    auditHistoryState = "loading";
    auditLoadError = "";
    auditHistoryError = "";
    const [standardResult, historyResult, fastResult] = await Promise.allSettled([
      getAuditLatest("standard", { signal: auditController.signal }),
      getAuditHistory("standard", 20, { signal: auditController.signal }),
      getAuditLatest("fast", { signal: auditController.signal })
    ]);
    if (auditDisposed) return;
    if (standardResult.status === "fulfilled") {
      assignAuditEnvelope("standard", standardResult.value);
      standardFreshnessObservedAt = Date.now();
      auditLoadState = "ready";
      scheduleStandardFreshnessRefresh();
    } else {
      assignAuditEnvelope("standard", null);
      auditLoadState = "error";
      auditLoadError = standardResult.reason?.message || "audit_report_unavailable";
    }
    if (historyResult.status === "fulfilled") {
      assignStandardAuditHistory(historyResult.value);
      auditHistoryState = "ready";
    } else {
      assignStandardAuditHistory({ profile: "standard", history: [] });
      auditHistoryState = "error";
      auditHistoryError = historyResult.reason?.message || "audit_report_unavailable";
    }
    if (fastResult.status === "fulfilled") assignAuditEnvelope("fast", fastResult.value);
  }

  async function startAdminAudit(profile) {
    if (!auth.enabled || auditStartBusy || auditRunBlocksStart(runByProfile.fast) || auditRunBlocksStart(runByProfile.standard)) return;
    auditStartBusy = true;
    auditActionError = "";
    try {
      const status = await startAuditRun(profile, { signal: auditController.signal });
      auditPollGeneration += 1;
      clearAuditPollTimers();
      const next = applyRunStatus({ standardEnvelope, fastEnvelope, runByProfile }, status);
      if (status.state === "completed" && status.report && status.freshness) {
        const completedProfile = status.profile === "fast" ? "fast" : "standard";
        assignAuditEnvelope(completedProfile, completedProfile === "fast" ? next.fastEnvelope : next.standardEnvelope);
      }
      runByProfile = next.runByProfile;
      if (profile === "standard" && status.state === "completed") standardFreshnessObservedAt = Date.now();
      if (status.state === "running") scheduleAuditPoll(profile, status.audit_id, 0);
    } catch (error) {
      if (error.status === 409) {
        auditActionError = `Another ${error.payload?.active_profile || "audit"} run is already active.`;
      } else if (error.status === 429) {
        auditActionError = `Standard audit can run again in ${error.payload?.retry_after_seconds || 60} seconds.`;
      } else {
        auditActionError = error.message || "audit_run_unavailable";
      }
    } finally {
      auditStartBusy = false;
    }
  }

  function scheduleAuditPoll(profile, auditID, attempt, delayOverride = null, generation = auditPollGeneration) {
    if (auditDisposed) return;
    const delay = Math.min(1000 * (2 ** Math.min(attempt, 3)), 5000);
    const timer = setTimeout(async () => {
      auditPollTimers.delete(timer);
      if (auditDisposed || generation !== auditPollGeneration) return;
      try {
        const status = await getAuditRun(auditID, { signal: auditController.signal });
        if (auditDisposed || generation !== auditPollGeneration) return;
        const next = applyRunStatus({ standardEnvelope, fastEnvelope, runByProfile }, status);
        if (status.state === "completed" && status.report && status.freshness) {
          const completedProfile = status.profile === "fast" ? "fast" : "standard";
          assignAuditEnvelope(completedProfile, completedProfile === "fast" ? next.fastEnvelope : next.standardEnvelope);
        }
        runByProfile = next.runByProfile;
        if (profile === "standard" && status.state === "completed") standardFreshnessObservedAt = Date.now();
        if (status.state === "running" && attempt < 240) {
          scheduleAuditPoll(profile, auditID, attempt + 1, null, generation);
        } else if (status.state === "running") {
          markAuditMonitoringUnknown(profile, auditID, "poll_timeout");
          try {
            await refreshLatestAudit(profile, generation);
          } catch {
            // Reattachment remains authoritative even when latest is unreadable.
          }
          if (auditDisposed || generation !== auditPollGeneration) return;
          scheduleAuditPoll(profile, auditID, attempt + 1, 30000, generation);
        } else if (profile === "standard" && status.state === "completed") {
          standardHistoryNeedsRefresh = true;
          scheduleStandardFreshnessRefresh();
          await refreshStandardAuditHistory(generation, 2, 3);
          if (auditDisposed || generation !== auditPollGeneration) return;
        }
      } catch (error) {
        if (!auditDisposed && generation === auditPollGeneration) {
          const statusForgotten = error.status === 404;
          markAuditMonitoringUnknown(profile, auditID, statusForgotten ? "run_status_forgotten" : "poll_unavailable", !statusForgotten);
          try {
            await refreshLatestAudit(profile, generation);
          } catch {
            // The exact-profile latest report is also unavailable. Keep the
            // server execution state unknown and reattach to the run later.
          }
          if (auditDisposed || generation !== auditPollGeneration) return;
          if (!statusForgotten) scheduleAuditPoll(profile, auditID, Math.min(attempt + 1, 241), 30000, generation);
        }
      }
    }, delayOverride ?? delay);
    auditPollTimers.add(timer);
  }

  function clearAuditPollTimers() {
    for (const timer of auditPollTimers) clearTimeout(timer);
    auditPollTimers.clear();
  }

  function markAuditMonitoringUnknown(profile, auditID, reason, active = true) {
    const next = applyRunMonitoringUnknown({ standardEnvelope, fastEnvelope, runByProfile }, { auditID, profile, reason, active });
    runByProfile = next.runByProfile;
    auditActionError = "";
  }

  async function refreshLatestAudit(profile, expectedGeneration = null, maxAttempts = 1) {
    let standardReportChanged = false;
    let latestApplied = false;
    const applyEnvelope = (envelope) => {
      if (auditDisposed) return;
      latestApplied = true;
      if (profile === "fast") {
        assignAuditEnvelope("fast", envelope);
        return;
      }
      const previousAuditID = standardEnvelope?.report?.audit_id || "";
      assignAuditEnvelope("standard", envelope);
      standardFreshnessObservedAt = Date.now();
      scheduleStandardFreshnessRefresh();
      standardReportChanged = Boolean(envelope?.report?.audit_id && envelope.report.audit_id !== previousAuditID);
    };
    if (expectedGeneration == null) {
      const envelope = await getAuditLatest(profile, { signal: auditController.signal });
      if (auditDisposed) return false;
      applyEnvelope(envelope);
    } else {
      const initialRevision = auditEnvelopeRevision[profile];
      const applied = await runGenerationStableRead({
        read: () => getAuditLatest(profile, { signal: auditController.signal }),
        apply: applyEnvelope,
        currentGeneration: () => auditPollGeneration,
        initialGeneration: expectedGeneration,
        currentRevision: () => auditEnvelopeRevision[profile],
        initialRevision,
        isDisposed: () => auditDisposed,
        maxAttempts
      });
      if (!applied || auditDisposed) return false;
    }
    if (standardReportChanged) {
      standardHistoryNeedsRefresh = true;
      await refreshStandardAuditHistory(expectedGeneration == null ? null : auditPollGeneration, maxAttempts, 2);
    }
    return !auditDisposed && latestApplied;
  }

  function scheduleStandardFreshnessRefresh() {
    clearTimeout(standardFreshnessTimer);
    standardFreshnessTimer = null;
    if (auditDisposed || currentPage !== "admin" || auth.enabled !== true || auditLoadState !== "ready") return;
    const elapsed = standardFreshnessObservedAt > 0 ? Date.now() - standardFreshnessObservedAt : 0;
    const delay = Math.min(freshnessRefreshDelayMs(standardEnvelope, elapsed), 2147483647);
    standardFreshnessTimer = setTimeout(() => {
      standardFreshnessTimer = null;
      void refreshStandardAtFreshnessBoundary();
    }, delay);
  }

  async function refreshStandardAtFreshnessBoundary() {
    if (auditDisposed || standardRefreshBusy) return;
    const generation = auditPollGeneration;
    standardRefreshBusy = true;
    const elapsed = standardFreshnessObservedAt > 0 ? Date.now() - standardFreshnessObservedAt : 0;
    if (freshnessDeadlineElapsed(standardEnvelope, elapsed)) assignAuditEnvelope("standard", markEnvelopeStale(standardEnvelope));
    try {
      await refreshLatestAudit("standard", generation, 2);
      if (standardHistoryNeedsRefresh) await refreshStandardAuditHistory(auditPollGeneration, 2, 1);
    } catch {
      // Keep the prior report visible as stale and retry the read later. A GET
      // failure is not evidence that the audit itself failed.
    } finally {
      standardRefreshBusy = false;
      scheduleStandardFreshnessRefresh();
    }
  }

  async function refreshStandardAuditHistory(expectedGeneration = null, maxAttempts = 1, retryBudget = 0) {
    if (expectedGeneration == null) {
      auditHistoryState = "loading";
      auditHistoryError = "";
    }
    try {
      if (expectedGeneration == null) {
        const response = await getAuditHistory("standard", 20, { signal: auditController.signal });
        if (auditDisposed) return false;
        assignStandardAuditHistory(response);
      } else {
        const initialRevision = auditHistoryRevision;
        const applied = await runGenerationStableRead({
          read: () => getAuditHistory("standard", 20, { signal: auditController.signal }),
          apply: (value) => { if (!auditDisposed) assignStandardAuditHistory(value); },
          currentGeneration: () => auditPollGeneration,
          initialGeneration: expectedGeneration,
          currentRevision: () => auditHistoryRevision,
          initialRevision,
          isDisposed: () => auditDisposed,
          maxAttempts
        });
        if (!applied || auditDisposed) {
          if (!auditDisposed) {
            standardHistoryNeedsRefresh = true;
            scheduleStandardHistoryRetry(retryBudget);
          }
          return false;
        }
      }
      auditHistoryState = "ready";
      standardHistoryNeedsRefresh = false;
      clearTimeout(standardHistoryRetryTimer);
      standardHistoryRetryTimer = null;
      return true;
    } catch (error) {
      if (auditDisposed) return false;
      if (expectedGeneration != null && expectedGeneration !== auditPollGeneration) {
        standardHistoryNeedsRefresh = true;
        scheduleStandardHistoryRetry(retryBudget);
        return false;
      }
      auditHistoryState = "error";
      auditHistoryError = error.message || "audit_report_unavailable";
      if (expectedGeneration != null) {
        standardHistoryNeedsRefresh = true;
        scheduleStandardHistoryRetry(retryBudget);
      }
      return false;
    }
  }

  function scheduleStandardHistoryRetry(retryBudget) {
    if (auditDisposed || retryBudget <= 0 || standardHistoryRetryTimer != null) return;
    standardHistoryRetryTimer = setTimeout(() => {
      standardHistoryRetryTimer = null;
      if (auditDisposed) return;
      void refreshStandardAuditHistory(auditPollGeneration, 2, retryBudget - 1);
    }, 1000);
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

  async function openShares() {
    inputMode = "shares";
    if (sharesState === "idle") {
      await loadShares();
    }
  }

  async function openHarness() {
    inputMode = "harness";
    await loadHarnessTraces();
  }

  async function loadShares() {
    sharesState = "loading";
    sharesError = "";
    try {
      const response = await listChatShares();
      shares = Array.isArray(response?.shares) ? response.shares : [];
      sharesState = "ready";
    } catch (error) {
      sharesError = error.message;
      sharesState = "error";
    }
  }

  async function loadHarnessTraces(options = {}) {
    const quiet = options?.quiet === true;
    if (!quiet) {
      harnessState = "loading";
    }
    harnessError = "";
    try {
      const response = await listResearchTraces({ limit: 50 });
      harnessTraces = Array.isArray(response?.traces) ? response.traces : [];
      harnessState = "ready";
    } catch (error) {
      harnessError = error.message;
      harnessState = "error";
    }
  }

  async function compareTrace(tracePath, runCurrent = false) {
    if (!tracePath) return;
    selectedTracePath = tracePath;
    harnessCompareState = "loading";
    harnessCompareError = "";
    try {
      harnessComparison = await compareResearchTrace(tracePath, { runCurrent });
      harnessCompareState = "ready";
    } catch (error) {
      harnessCompareError = error.message;
      harnessCompareState = "error";
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
        traceSurface: "web_research_api",
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
      progress: [],
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
      await runResearchRunner(retrievalQuestion, {
        signal: controller.signal,
        traceSurface: "web_chat",
        traceContinuity: buildChatTraceContinuity(question, retrievalQuestion, priorTurns, pinnedEvidenceKeys),
        onEvent: (event, payload) => {
          if (event === "progress") {
            const current = chatTurns.find((candidate) => candidate.id === id);
            const progress = [...(current?.progress || []), payload].slice(-12);
            const nextStatus = payload.stage === "synthesis" ? "synthesizing" : "researching";
            const tracePath = payload?.data?.trace_path;
            const patch = { progress, status: nextStatus };
            if (tracePath) {
              patch.done = { ...(current?.done || {}), trace_path: tracePath };
              void loadHarnessTraces({ quiet: true });
            }
            updateChatTurn(id, patch);
            chatState = nextStatus;
          } else if (event === "answer") {
            updateChatTurn(id, { answer: payload.text || "" });
          } else if (event === "citation") {
            const current = chatTurns.find((candidate) => candidate.id === id);
            updateChatTurn(id, { citations: [...(current?.citations || []), payload] });
          } else if (event === "done") {
            const failed = isResearchRunnerFailure(payload.stop_reason);
            const status = payload.stop_reason === "verification_failed" ? "verification_failed" : failed ? "error" : "ready";
            updateChatTurn(id, {
              research_pack: payload.research_pack || chatTurns.find((candidate) => candidate.id === id)?.research_pack,
              done: payload,
              citations: payload.citations || chatTurns.find((candidate) => candidate.id === id)?.citations || [],
              status
            });
            if (payload.trace_path) {
              void loadHarnessTraces({ quiet: true });
            }
            chatState = status === "error" ? "error" : "ready";
          } else if (event === "verification_failed") {
            const message = payload.error || "Citation verification failed";
            updateChatTurn(id, { done: payload, error: message, status: "verification_failed" });
            chatError = "";
            chatState = "ready";
          } else if (event === "error") {
            updateChatTurn(id, { done: payload, error: payload.error || "Synthesis failed", status: "error" });
            chatError = payload.error || "Synthesis failed";
            chatState = "error";
          }
        }
      });
      const finalStatus = chatTurns.find((candidate) => candidate.id === id)?.status;
      if (finalStatus === "synthesizing" || finalStatus === "researching") {
        updateChatTurn(id, { status: "ready" });
      }
      if (chatState === "synthesizing" || chatState === "researching") {
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
    const warnings = turn?.done?.answer_warnings || turn?.done?.synthesis?.answer_warnings || turn?.done?.warnings || turn?.start?.answer_warnings || [];
    return warnings.map(formatSynthesisWarning);
  }

  function chatCitations(turn) {
    if (turn?.status === "verification_failed") return [];
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

  async function shareChatTurn(turn) {
    if (!turn?.id || turn.status !== "ready" || !turn.answer || chatShareStateByTurn[turn.id] === "loading") return;
    chatShareStateByTurn = { ...chatShareStateByTurn, [turn.id]: "loading" };
    chatShareErrorByTurn = { ...chatShareErrorByTurn, [turn.id]: "" };
    chatShareCopiedByTurn = { ...chatShareCopiedByTurn, [turn.id]: false };
    try {
      const share = await createChatShare(turn);
      chatShareByTurn = { ...chatShareByTurn, [turn.id]: share };
      chatShareStateByTurn = { ...chatShareStateByTurn, [turn.id]: "ready" };
      await copyShareURL(turn.id, share);
      if (inputMode === "shares") {
        await loadShares();
      } else {
        sharesState = "idle";
      }
    } catch (error) {
      chatShareErrorByTurn = { ...chatShareErrorByTurn, [turn.id]: error.message };
      chatShareStateByTurn = { ...chatShareStateByTurn, [turn.id]: "error" };
    }
  }

  async function copyShareURL(turnID, share) {
    const url = absoluteShareURL(share);
    if (!url || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(url);
      chatShareCopiedByTurn = { ...chatShareCopiedByTurn, [turnID]: true };
    } catch {
      chatShareCopiedByTurn = { ...chatShareCopiedByTurn, [turnID]: false };
    }
  }

  async function copyListedShare(share) {
    const url = absoluteShareURL(share);
    if (!url || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(url);
    } catch {
      // The visible link remains available when clipboard access is denied.
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

  function evidenceTypeLabel(evidence) {
    const base = evidence?.source_type || evidence?.kind || evidence?.relationship || "source";
    const role = evidence?.evidence_role || evidence?.chunk?.role || "";
    return role ? `${base} / ${role}` : base;
  }

  function evidenceSignalSummary(evidence) {
    const signals = Array.isArray(evidence?.retrieval?.signals) ? evidence.retrieval.signals : [];
    return signals.slice(0, 3).map((signal) => signal?.name).filter(Boolean).join(" / ");
  }

  function currentProgressStep(turn) {
    const rows = Array.isArray(turn?.progress) ? turn.progress : [];
    for (let index = rows.length - 1; index >= 0; index -= 1) {
      const row = rows[index];
      const stage = String(row?.stage || "").trim();
      if (!stage) continue;
      return row;
    }
    return null;
  }

  function chatHeaderStatus(turn) {
    const model = turn?.done?.model || turn?.start?.model || "";
    if (model) return model;
    if ((turn?.status === "researching" || turn?.status === "synthesizing") && currentProgressStep(turn)) {
      return "";
    }
    return turn?.status || "";
  }

  function formatProgressStage(row) {
    const stage = String(row?.stage || "").replace(/_/g, " ");
    const status = String(row?.status || "").replace(/_/g, " ");
    return status ? `${stage} / ${status}` : stage;
  }

  function formatTraceSummary(trace) {
    const parts = [];
    if (trace?.stop_reason) parts.push(trace.stop_reason);
    if (trace?.evidence_count != null) parts.push(`${trace.evidence_count} evidence`);
    if (trace?.answer_status) parts.push(trace.answer_status);
    return parts.join(" / ");
  }

  function comparisonOldEvidence() {
    return harnessComparison?.trace?.pack?.evidence || [];
  }

  function comparisonCurrentEvidence() {
    return harnessComparison?.current?.research_pack?.evidence || harnessComparison?.diff?.top_evidence || [];
  }

  function evidenceWithMedia(rows = []) {
    return (Array.isArray(rows) ? rows : []).filter((row) => Array.isArray(row?.media) && row.media.length > 0);
  }

  function truncateText(value, max) {
    const text = String(value || "").replace(/\s+/g, " ").trim();
    if (text.length <= max) return text;
    return text.slice(0, max).trimEnd() + "…";
  }

  function absoluteShareURL(share) {
    const path = String(share?.url || "");
    if (!path) return "";
    if (typeof window === "undefined") return path;
    try {
      return new URL(path, window.location.origin).href;
    } catch {
      return path;
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
            <button class="search-tab" class:active={inputMode === "chat"} on:click={() => { inputMode = "chat"; }} type="button">
              Chat
            </button>
            <button class="search-tab" class:active={inputMode === "harness"} on:click={openHarness} type="button">
              Harness
            </button>
            <button class="search-tab" class:active={inputMode === "shares"} on:click={openShares} type="button">
              Shares
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
          {#if inputMode === "shares"}
            <button class="btn-ghost compact-action" type="button" disabled={sharesState === "loading"} on:click={loadShares}>
              {sharesState === "loading" ? "Refreshing…" : "Refresh"}
            </button>
          {/if}
          {#if inputMode === "harness"}
            <button class="btn-ghost compact-action" type="button" disabled={harnessState === "loading"} on:click={loadHarnessTraces}>
              {harnessState === "loading" ? "Refreshing…" : "Refresh"}
            </button>
          {/if}
        </div>

        {#if (inputMode !== "chat" && inputMode !== "shares" && inputMode !== "harness") || (inputMode === "chat" && chatTurns.length === 0)}
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
          {#if inputMode === "shares"}
            <h2>Shared chat answers.</h2>
            <p class="message muted" style="margin-top:0.5rem">
              Newest shares appear first.
            </p>
          {:else if inputMode === "harness"}
            <h2>Compare saved research traces against the current harness.</h2>
            <p class="message muted" style="margin-top:0.5rem">
              Load a trace to inspect old evidence, current evidence, and eval proposal commands.
            </p>
          {:else if inputMode === "chat"}
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
      {:else if hasResults || showDetailPanel || activeState === "loading" || inputMode === "shares" || inputMode === "harness" || (inputMode === "research" && activeState === "ready") || (inputMode === "chat" && chatTurns.length > 0)}
        <div class="content-area" class:has-detail={showDetailPanel}>
          <div class="content-main">
            {#if inputMode === "shares"}
              <div class="shares-panel">
                <div class="shares-header">
                  <div>
                    <p class="panel-kicker" style="margin:0">Shares</p>
                    <p class="message muted">{shares.length} public chat answers</p>
                  </div>
                </div>

                {#if sharesState === "loading"}
                  <p class="message muted">Loading shares…</p>
                {:else if sharesState === "error"}
                  <p class="message error">{sharesError}</p>
                {:else if shares.length === 0}
                  <div class="answer-card">
                    <p class="message muted">No shared chat answers yet.</p>
                  </div>
                {:else}
                  <div class="share-list">
                    {#each shares as share}
                      <article class="share-list-card">
                        <div class="share-list-main">
                          <a class="share-title" href={share.url} target="_blank" rel="noreferrer">{share.title || "Shared dbrain answer"}</a>
                          <span>{formatTime(share.updated_at || share.created_at)}</span>
                        </div>
                        <p>{share.summary}</p>
                        {#if share.categories?.length > 0}
                          <div class="share-categories" aria-label="Share categories">
                            {#each share.categories as category}
                              <span>{category}</span>
                            {/each}
                          </div>
                        {/if}
                        <div class="share-actions">
                          <a class="link-chip" href={share.url} target="_blank" rel="noreferrer">Open</a>
                          <button class="btn-ghost compact-action" type="button" on:click={() => copyListedShare(share)}>Copy URL</button>
                        </div>
                      </article>
                    {/each}
                  </div>
                {/if}
              </div>
            {:else if inputMode === "harness"}
              <div class="harness-panel">
                <div class="harness-header">
                  <div>
                    <p class="panel-kicker" style="margin:0">Harness Lab</p>
                    <p class="message muted">{harnessTraces.length} saved traces available</p>
                  </div>
                  {#if selectedTracePath}
                    <button class="btn-ghost compact-action" type="button" disabled={harnessCompareState === "loading"} on:click={() => compareTrace(selectedTracePath, true)}>
                      {harnessCompareState === "loading" ? "Running…" : "Rerun current"}
                    </button>
                  {/if}
                </div>

                {#if harnessState === "loading"}
                  <p class="message muted">Loading traces…</p>
                {:else if harnessState === "error"}
                  <p class="message error">{harnessError}</p>
                {:else if harnessTraces.length === 0}
                  <div class="answer-card">
                    <p class="message muted">No saved research traces yet.</p>
                  </div>
                {:else}
                  <div class="harness-grid">
                    <div class="trace-list">
                      {#each harnessTraces as trace}
                        <button class="trace-row" class:selected={selectedTracePath === trace.relative_path} type="button" on:click={() => compareTrace(trace.relative_path)}>
                          <strong>{trace.question || trace.run_id}</strong>
                          <span>{formatTraceSummary(trace)}</span>
                          <small>{trace.relative_path}</small>
                        </button>
                      {/each}
                    </div>

                    <div class="trace-compare">
                      {#if harnessCompareState === "loading"}
                        <div class="answer-card">
                          <p class="message muted">Comparing trace against the current retrieval harness…</p>
                        </div>
                      {:else if harnessCompareState === "error"}
                        <div class="answer-card">
                          <p class="message error">{harnessCompareError}</p>
                        </div>
                      {:else if harnessComparison}
                        <div class="answer-card">
                          <div class="synthesis-header">
                            <p class="panel-kicker" style="margin:0">{harnessComparison.diff ? "Trace Diff" : "Saved Trace"}</p>
                            <span>{harnessComparison.diff?.question || harnessComparison.trace?.question}</span>
                          </div>
                          {#if harnessComparison.diff_error}
                            <p class="message error">Current diff unavailable: {harnessComparison.diff_error}</p>
                          {/if}
                          {#if harnessComparison.diff}
                            <div class="diff-stats">
                              <span>Added {harnessComparison.diff.added?.length || 0}</span>
                              <span>Removed {harnessComparison.diff.removed?.length || 0}</span>
                              <span>Reordered {harnessComparison.diff.reordered?.length || 0}</span>
                            </div>
                          {/if}
                          {#if harnessComparison.diff?.proposal_command}
                            <code class="proposal-command">{harnessComparison.diff.proposal_command}</code>
                          {/if}
                        </div>

                        <div class="answer-compare-grid">
                          <div class="answer-card">
                            <p class="panel-kicker" style="margin:0">Old Answer</p>
                            {#if harnessComparison.old_answer}
                              <MarkdownView markdown={harnessComparison.old_answer} linkSourceKeys={true} onLookup={loadDetail} />
                            {:else}
                              <p class="message muted">No answer was saved in this trace.</p>
                            {/if}
                          </div>
                          <div class="answer-card">
                            <p class="panel-kicker" style="margin:0">Current Answer</p>
                            {#if harnessComparison.current?.answer}
                              <MarkdownView markdown={harnessComparison.current.answer} linkSourceKeys={true} onLookup={loadDetail} />
                            {:else if harnessComparison.current_error}
                              <p class="message error">{harnessComparison.current_error}</p>
                            {:else if harnessComparison.current?.stop_reason}
                              <p class="message muted">Current rerun stopped: {harnessComparison.current.stop_reason}</p>
                            {:else}
                              <p class="message muted">Use Rerun current to synthesize a fresh answer for comparison.</p>
                            {/if}
                          </div>
                        </div>

                        <div class="answer-compare-grid">
                          <div class="evidence-compact">
                            <div class="evidence-compact-header">
                              <p class="panel-kicker" style="margin:0">Old Evidence</p>
                              <span>{comparisonOldEvidence().length} rows</span>
                            </div>
                            <div class="evidence-compact-list">
                              {#each comparisonOldEvidence() as evidence}
                                <article class="evidence-card">
                                  <button class="evidence-card-main" type="button" on:click={() => loadDetail(evidence.source_key)}>
	                                    <span class="result-key">{evidenceTypeLabel(evidence)}</span>
	                                    <strong>{evidence.title || evidence.url || evidence.source_key}</strong>
	                                    {#if evidenceSignalSummary(evidence)}
	                                      <small>{evidenceSignalSummary(evidence)}</small>
	                                    {/if}
	                                  </button>
                                </article>
                              {/each}
                            </div>
                          </div>
                          <div class="evidence-compact">
                            <div class="evidence-compact-header">
                              <p class="panel-kicker" style="margin:0">Current Evidence</p>
                              <span>{comparisonCurrentEvidence().length} rows</span>
                            </div>
                            <div class="evidence-compact-list">
                              {#each comparisonCurrentEvidence() as evidence}
                                <article class="evidence-card">
                                  <button class="evidence-card-main" type="button" on:click={() => loadDetail(evidence.source_key)}>
	                                    <span class="result-key">{evidenceTypeLabel(evidence)}</span>
	                                    <strong>{evidence.title || evidence.url || evidence.source_key}</strong>
	                                    {#if evidenceSignalSummary(evidence)}
	                                      <small>{evidenceSignalSummary(evidence)}</small>
	                                    {/if}
	                                  </button>
                                </article>
                              {/each}
                            </div>
                          </div>
                        </div>
                      {:else}
                        <div class="answer-card">
                          <p class="message muted">Select a trace to inspect its saved answer and evidence diff.</p>
                        </div>
                      {/if}
                    </div>
                  </div>
                {/if}
              </div>
            {:else if inputMode === "chat"}
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
                        <div class="answer-actions">
                          {#if chatHeaderStatus(turn)}
                            <span>{chatHeaderStatus(turn)}</span>
                          {/if}
                          {#if turn.status === "ready" && turn.answer}
                            <button class="btn-ghost compact-action" type="button" disabled={chatShareStateByTurn[turn.id] === "loading"} on:click={() => shareChatTurn(turn)}>
                              {chatShareStateByTurn[turn.id] === "loading" ? "Sharing…" : "Share"}
                            </button>
                          {/if}
                        </div>
                      </div>

                      {#if turn.done?.trace_path}
                        <p class="message muted">Trace saved: {turn.done.trace_path}</p>
                      {/if}

                      {#if turn.status === "researching" && !currentProgressStep(turn)}
                        <p class="message muted">{turn.progress?.[turn.progress.length - 1]?.message || "Retrieving evidence from the local brain…"}</p>
                      {:else if turn.status === "synthesizing" && !turn.answer && !currentProgressStep(turn)}
                        <p class="message muted">{turn.progress?.[turn.progress.length - 1]?.message || "Generating local answer…"}</p>
                      {:else if turn.status === "verification_failed"}
                        <p class="message error">Generated answer rejected: {turn.error || "citation verification failed"}</p>
                      {:else if turn.status === "error"}
                        <p class="message error">{turn.error || "Chat turn failed"}</p>
                      {/if}

                      {#if currentProgressStep(turn) && (turn.status === "researching" || turn.status === "synthesizing")}
                        <div class="progress-current" aria-label="Research runner progress">
                          <span>{formatProgressStage(currentProgressStep(turn))}</span>
                          <small>{currentProgressStep(turn).message}</small>
                        </div>
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

                      {#if formatSemanticDiagnostics(turn.research_pack)}
                        <p class="message muted retrieval-diagnostics">{formatSemanticDiagnostics(turn.research_pack)}</p>
                      {/if}

                      {#if chatShareByTurn[turn.id] || chatShareErrorByTurn[turn.id]}
                        <p class="chat-share-note" class:error={chatShareErrorByTurn[turn.id]}>
                          {#if chatShareErrorByTurn[turn.id]}
                            {chatShareErrorByTurn[turn.id]}
                          {:else}
                            Share URL: <a href={chatShareByTurn[turn.id].url} target="_blank" rel="noreferrer">{absoluteShareURL(chatShareByTurn[turn.id])}</a>{chatShareCopiedByTurn[turn.id] ? " copied" : ""}
                          {/if}
                        </p>
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

                      {#if evidenceWithMedia(chatEvidence(turn)).length > 0}
                        <div class="answer-media-evidence">
                          {#each evidenceWithMedia(chatEvidence(turn)) as evidence}
                            <MediaEvidenceBlock item={evidence} onSelect={loadDetail} />
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
	                                <span class="result-key">{evidenceTypeLabel(evidence)}</span>
	                                <strong>{evidence.title || evidence.url || evidence.source_key}</strong>
	                                {#if evidenceSignalSummary(evidence)}
	                                  <small>{evidenceSignalSummary(evidence)}</small>
	                                {/if}
	                                {#if evidencePreview(evidence)}
                                  <p>{evidencePreview(evidence)}</p>
                                {/if}
                              </button>
                              <button class="pin-chip" class:active={isPinnedEvidence(evidence.source_key)} type="button" on:click={() => togglePinnedEvidence(evidence.source_key)}>
                                Pin
                              </button>
                              <div class="evidence-card-media">
                                <MediaEvidenceBlock item={evidence} onSelect={loadDetail} showHeader={false} />
                              </div>
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
                    {#if formatSemanticDiagnostics(researchPack)}
                      <p class="message muted retrieval-diagnostics">{formatSemanticDiagnostics(researchPack)}</p>
                    {/if}
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
                    {#if evidenceWithMedia(activeResults).length > 0}
                      <div class="answer-media-evidence">
                        {#each evidenceWithMedia(activeResults) as evidence}
                          <MediaEvidenceBlock item={evidence} onSelect={loadDetail} />
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
                        <article class="evidence-card" class:selected={selectedLookup === evidence.source_key}>
                          <button class="evidence-card-main" type="button" on:click={() => loadDetail(evidence.source_key)}>
	                            <span class="result-key">{evidenceTypeLabel(evidence)}</span>
	                            <strong>{evidence.title || evidence.url || evidence.source_key}</strong>
	                            {#if evidenceSignalSummary(evidence)}
	                              <small>{evidenceSignalSummary(evidence)}</small>
	                            {/if}
	                            {#if evidencePreview(evidence)}
                              <p>{evidencePreview(evidence)}</p>
                            {/if}
                          </button>
                          <MediaEvidenceBlock item={evidence} onSelect={loadDetail} showHeader={false} />
                        </article>
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

            {#if inputMode !== "shares" && inputMode !== "harness"}
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
            {/if}
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
          <p class="lede">Audited production health, durability, and source operations.</p>
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

      {#if auditActionError}
        <div class="banner error" role="status" aria-live="polite">{auditActionError}</div>
      {/if}

      <AuditOverview
        health={standardHealth}
        overview={auditOverview}
        {standardEnvelope}
        {fastEnvelope}
        loading={auditLoadState === "loading" || auditStartBusy}
        loadState={auditLoadState}
        error={auditLoadError}
        authEnabled={auth.enabled === true}
        {runByProfile}
        onRun={startAdminAudit}
      />

      <div class="audit-two-column">
        <AuditImporters importers={auditImporters} />
        <AuditPipeline stages={auditPipeline} />
      </div>

      <AuditDurability cards={auditDurability} />
      <AuditFindings findings={auditFindings} />
      <AuditHistory history={auditHistory} loading={auditHistoryState === "loading"} error={auditHistoryError} />

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
