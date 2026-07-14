async function fetchJSON(url, options = {}) {
  let response;
  try {
    response = await fetch(url, options);
  } catch (error) {
    throw new Error(`Could not reach dbrain web API: ${error.message || error}`);
  }
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(payload.error || `Request failed with status ${response.status}`);
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

export function getBootstrap() {
  return fetchJSON("/api/bootstrap");
}

export function getAuditLatest(profile = "standard", options = {}) {
  return fetchJSON(`/api/audit/latest?profile=${auditProfile(profile)}`, { cache: "no-store", signal: options.signal });
}

export function getAuditHistory(profile = "standard", limit = 20, options = {}) {
  const boundedLimit = Number.isInteger(limit) && limit >= 1 && limit <= 100 ? limit : 20;
  return fetchJSON(`/api/audit/history?profile=${auditProfile(profile)}&limit=${boundedLimit}`, { cache: "no-store", signal: options.signal });
}

export function startAuditRun(profile, options = {}) {
  return fetchJSON("/api/audit/run", {
    method: "POST",
    signal: options.signal,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ profile: auditProfile(profile) })
  });
}

export function getAuditRun(auditID, options = {}) {
  const id = String(auditID || "");
  if (!/^run_[0-9a-f]{32}$/.test(id)) return Promise.reject(new Error("Invalid audit run ID"));
  return fetchJSON(`/api/audit/runs/${encodeURIComponent(id)}`, { cache: "no-store", signal: options.signal });
}

function auditProfile(profile) {
  if (profile !== "fast" && profile !== "standard") throw new Error("Invalid audit profile");
  return profile;
}

export function getSourceActivity(filters = {}) {
  const params = new URLSearchParams();
  params.set("limit", String(filters.limit || 8));
  params.set("failure_offset", String(filters.failureOffset || 0));
  params.set("failure_sort", String(filters.failureSort || "newest"));
  if (filters.sourceType) {
    params.set("source_type", filters.sourceType);
  }
  if (filters.domain) {
    params.set("domain", filters.domain);
  }
  if (filters.status) {
    params.set("status", filters.status);
  }
  if (filters.failureKind) {
    params.set("failure_kind", filters.failureKind);
  }
  if (filters.message) {
    params.set("message", filters.message);
  }
  if (filters.window) {
    params.set("window", filters.window);
  }
  return fetchJSON(`/api/stats/source-activity?${params.toString()}`);
}

export function searchBrain(query, limit) {
  const params = new URLSearchParams({ q: query });
  if (limit != null) params.set("limit", String(limit));
  return fetchJSON(`/api/search?${params.toString()}`);
}

export function getLookup(lookup) {
  const params = new URLSearchParams({ lookup });
  return fetchJSON(`/api/get?${params.toString()}`);
}

export function researchBrain(question, options = {}) {
  const { signal, ...bodyOptions } = options;
  return fetchJSON("/api/research", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    signal,
    body: JSON.stringify({
      question,
      limit: 10,
      include_related: true,
      related_limit: 2,
      max_chars_per_doc: 4000,
      use_model_planner: true,
      use_semantic: options.useSemantic === true,
      disable_semantic: options.disableSemantic === true,
      ...bodyOptions
    })
  });
}

export async function synthesizeResearch(question, researchPack, options = {}) {
  const response = await fetch("/api/research/synthesize", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    signal: options.signal,
    body: JSON.stringify({
      question,
      research_pack: researchPack,
      model: options.model || "",
      max_evidence_chars: options.maxEvidenceChars || 24000,
      answer_review: options.answerReview === true,
      answer_review_model: options.answerReviewModel || "",
      trace_enabled: options.traceEnabled !== false,
      trace_surface: options.traceSurface || "",
      trace_continuity: options.traceContinuity || null
    })
  });

  if (!response.ok) {
    const payload = await response.json().catch(() => ({}));
    throw new Error(payload.error || `Request failed with status ${response.status}`);
  }
  if (!response.body) {
    throw new Error("Synthesis stream is unavailable");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    buffer = drainSSEBuffer(buffer, options.onEvent);
  }
  buffer += decoder.decode();
  drainSSEBuffer(buffer + "\n\n", options.onEvent);
}

export async function runResearch(question, options = {}) {
  const response = await fetch("/api/research/run", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    signal: options.signal,
    body: JSON.stringify({
      question,
      limit: options.limit || 10,
      related_limit: options.relatedLimit || 2,
      max_chars_per_doc: options.maxCharsPerDoc || 4000,
      use_model_planner: options.useModelPlanner !== false,
      use_semantic: options.useSemantic === true,
      disable_semantic: options.disableSemantic === true,
      model: options.model || "",
      max_evidence_chars: options.maxEvidenceChars || 24000,
      trace_enabled: options.traceEnabled !== false,
      trace_surface: options.traceSurface || "",
      trace_continuity: options.traceContinuity || null
    })
  });

  if (!response.ok) {
    const payload = await response.json().catch(() => ({}));
    throw new Error(payload.error || `Request failed with status ${response.status}`);
  }
  if (!response.body) {
    throw new Error("Research runner stream is unavailable");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    buffer = drainSSEBuffer(buffer, options.onEvent);
  }
  buffer += decoder.decode();
  drainSSEBuffer(buffer + "\n\n", options.onEvent);
}

export function saveChatTranscript(payload) {
  return fetchJSON("/api/chat/transcripts", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(payload)
  });
}

export function createChatShare(turn) {
  return fetchJSON("/api/chat/shares", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify({ turn })
  });
}

export function listChatShares() {
  return fetchJSON("/api/chat/shares");
}

export function listResearchTraces(options = {}) {
  const params = new URLSearchParams();
  params.set("limit", String(options.limit || 25));
  return fetchJSON(`/api/research/traces?${params.toString()}`, { cache: "no-store" });
}

export function compareResearchTrace(tracePath, options = {}) {
  return fetchJSON("/api/research/trace-compare", {
    method: "POST",
    cache: "no-store",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      trace_path: tracePath,
      run_current: options.runCurrent === true
    })
  });
}

function drainSSEBuffer(buffer, onEvent) {
  let boundary = buffer.indexOf("\n\n");
  while (boundary !== -1) {
    const block = buffer.slice(0, boundary);
    buffer = buffer.slice(boundary + 2);
    emitSSEBlock(block, onEvent);
    boundary = buffer.indexOf("\n\n");
  }
  return buffer;
}

function emitSSEBlock(block, onEvent) {
  if (!block.trim() || typeof onEvent !== "function") return;
  let event = "message";
  const data = [];
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) {
      event = line.slice(6).trim();
    } else if (line.startsWith("data:")) {
      data.push(line.slice(5).trim());
    }
  }
  let payload = {};
  const raw = data.join("\n");
  if (raw) {
    try {
      payload = JSON.parse(raw);
    } catch {
      payload = { raw };
    }
  }
  onEvent(event, payload);
}

export function tagRecord(lookup, tags) {
  return fetchJSON("/api/tag", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ lookup, tags })
  });
}

export function addLink(url, options = {}) {
  return fetchJSON("/api/links", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      url,
      enrich: false,
      ...options
    })
  });
}
