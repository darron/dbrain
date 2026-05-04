const defaultMaxPriorQuestions = 3;
const defaultMaxPriorEvidence = 8;
const defaultMaxMergedEvidence = 18;
const defaultMaxPriorEvidenceContext = 6;

export function buildChatRetrievalQuestion(question, turns = [], pinnedEvidenceKeys = [], options = {}) {
  const current = cleanText(question);
  const maxPriorQuestions = positiveInt(options.maxPriorQuestions, defaultMaxPriorQuestions);
  const maxPriorEvidence = positiveInt(options.maxPriorEvidence, defaultMaxPriorEvidence);
  const maxPriorEvidenceContext = positiveInt(options.maxPriorEvidenceContext, defaultMaxPriorEvidenceContext);
  const parts = [`Current question: ${current}`];
  const includePriorExpansion = shouldIncludePriorExpansion(current);

  const priorQuestions = turns
    .map((turn) => cleanText(turn?.question))
    .filter(Boolean)
    .slice(-maxPriorQuestions);
  if (priorQuestions.length > 0 && includePriorExpansion) {
    parts.push("Recent user questions:\n" + priorQuestions.map((value) => `- ${value}`).join("\n"));
  }

  const priorEvidence = collectPriorEvidence(turns, pinnedEvidenceKeys, maxPriorEvidence);
  const priorContext = priorEvidence
    .slice(0, maxPriorEvidenceContext)
    .map(formatEvidenceSearchHint)
    .filter(Boolean);
  if (priorContext.length > 0 && includePriorExpansion) {
    parts.push("Prior evidence titles for query focus:\n" + priorContext.map((value) => `- ${value}`).join("\n"));
  }

  return parts.join("\n\n");
}

export function mergeResearchPackForChat(question, currentPack = {}, turns = [], pinnedEvidenceKeys = [], options = {}) {
  const maxPriorEvidence = positiveInt(options.maxPriorEvidence, defaultMaxPriorEvidence);
  const maxMergedEvidence = positiveInt(options.maxMergedEvidence, defaultMaxMergedEvidence);
  const currentEvidence = Array.isArray(currentPack?.evidence) ? currentPack.evidence : [];
  const priorEvidence = collectPriorEvidence(turns, pinnedEvidenceKeys, maxPriorEvidence)
    .map((row) => ({ ...row, relationship: row.relationship || "prior_chat_context" }));
  const mergedEvidence = mergeEvidenceRows(currentEvidence, priorEvidence, maxMergedEvidence);
  const coverage = { ...(currentPack?.coverage || {}) };

  if (priorEvidence.length > 0) {
    const note = coverage.recall_note || "Retrieved evidence from the local brain.";
    coverage.recall_note = `${note} Includes prior chat evidence for follow-up continuity.`;
  }

  return {
    ...currentPack,
    question,
    coverage,
    evidence: mergedEvidence
  };
}

export function collectPriorEvidence(turns = [], pinnedEvidenceKeys = [], limit = defaultMaxPriorEvidence) {
  const maxRows = positiveInt(limit, defaultMaxPriorEvidence);
  const rowsByKey = new Map();
  const pinned = new Set((pinnedEvidenceKeys || []).filter(Boolean));

  for (const turn of [...turns].reverse()) {
    const evidence = Array.isArray(turn?.research_pack?.evidence) ? turn.research_pack.evidence : [];
    for (const row of evidence) {
      const key = row?.source_key;
      if (!key || rowsByKey.has(key)) continue;
      rowsByKey.set(key, row);
    }
  }

  const prioritized = [];
  for (const key of pinned) {
    if (rowsByKey.has(key)) prioritized.push(rowsByKey.get(key));
  }
  for (const row of rowsByKey.values()) {
    if (prioritized.length >= maxRows) break;
    if (!pinned.has(row.source_key)) prioritized.push(row);
  }
  return prioritized.slice(0, maxRows);
}

export function mergeEvidenceRows(primary = [], secondary = [], limit = defaultMaxMergedEvidence) {
  const maxRows = positiveInt(limit, defaultMaxMergedEvidence);
  const merged = [];
  const seen = new Set();
  for (const row of [...primary, ...secondary]) {
    const key = row?.source_key;
    if (!key || seen.has(key)) continue;
    seen.add(key);
    merged.push(row);
    if (merged.length >= maxRows) break;
  }
  return merged;
}

export function normalizeStoredChatSession(value = {}) {
  return {
    turns: Array.isArray(value?.turns) ? value.turns.map(normalizeStoredTurn).filter((turn) => turn.id && turn.question) : [],
    pinnedEvidenceKeys: Array.isArray(value?.pinnedEvidenceKeys) ? value.pinnedEvidenceKeys.filter(Boolean) : []
  };
}

function normalizeStoredTurn(turn = {}) {
  return {
    id: String(turn.id || ""),
    question: String(turn.question || ""),
    retrieval_question: String(turn.retrieval_question || ""),
    status: normalizeStatus(turn.status),
    answer: String(turn.answer || ""),
    research_pack: normalizePack(turn.research_pack),
    start: turn.start && typeof turn.start === "object" ? turn.start : null,
    done: turn.done && typeof turn.done === "object" ? turn.done : null,
    citations: Array.isArray(turn.citations) ? turn.citations : [],
    error: String(turn.error || ""),
    created_at: String(turn.created_at || "")
  };
}

function normalizePack(pack = {}) {
  return {
    ...pack,
    question: String(pack?.question || ""),
    evidence: Array.isArray(pack?.evidence) ? pack.evidence : [],
    coverage: pack?.coverage && typeof pack.coverage === "object" ? pack.coverage : {},
    query_plan: pack?.query_plan && typeof pack.query_plan === "object" ? pack.query_plan : {},
    next_steps: Array.isArray(pack?.next_steps) ? pack.next_steps : []
  };
}

function normalizeStatus(status) {
  switch (status) {
    case "researching":
    case "synthesizing":
    case "ready":
    case "error":
      return status;
    default:
      return "ready";
  }
}

function cleanText(value) {
  return String(value || "").replace(/\s+/g, " ").trim();
}

function shouldIncludePriorExpansion(question) {
  const cleaned = cleanText(question);
  if (!cleaned) return false;
  const lower = cleaned.toLowerCase();
  const words = lower.split(/\s+/).filter(Boolean);
  if (words.length <= 5) return true;
  if (/\b(actually|correction|did not|didn't|missed|no,|not that|there is data|that's wrong|that is wrong|you missed)\b/.test(lower)) return false;
  if (/\b(also|another|else|expand|follow-up|from there|how about|more|other|same|what about)\b/.test(lower)) return true;
  if (words.length <= 8) {
    return /\b(it|its|that|them|there|these|this|those)\b/.test(lower);
  }
  return false;
}

function formatEvidenceSearchHint(row = {}) {
  const parts = [];
  for (const value of [row.title, row.source_type]) {
    const cleaned = truncateClean(value, 160);
    if (cleaned) parts.push(cleaned);
  }
  return parts.join(" | ");
}

function truncateClean(value, limit) {
  const cleaned = cleanText(value);
  if (!cleaned) return "";
  if (cleaned.length <= limit) return cleaned;
  return `${cleaned.slice(0, limit).trim()}…`;
}

function positiveInt(value, fallback) {
  const parsed = Number.parseInt(String(value), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}
