export function formatSemanticDiagnostics(pack = {}) {
  const plan = pack?.query_plan;
  if (!plan || typeof plan !== "object") return "";
  const mode = String(plan.semantic_mode || "").trim();
  if (!mode) return "";

  const parts = [`Semantic ${mode}`];
  const lanes = Array.isArray(plan.retrieval_lanes)
    ? plan.retrieval_lanes.map(formatLane).filter(Boolean)
    : [];
  if (lanes.length > 0) parts.push(`lanes ${lanes.join(", ")}`);

  const comparisons = [];
  if (plan.shadow_comparison) comparisons.push(`initial=${formatComparison(plan.shadow_comparison)}`);
  if (plan.retry_shadow_comparison) comparisons.push(`retry=${formatComparison(plan.retry_shadow_comparison)}`);
  if (comparisons.length > 0) parts.push(`shadow ${comparisons.join("; ")}`);
  return parts.join(" · ");
}

function formatLane(lane = {}) {
  const name = String(lane?.name || "").trim();
  const status = String(lane?.status || "").trim();
  if (!name || !status) return "";
  const reason = String(lane?.reason || "").trim();
  return `${name}=${status}${reason ? `(${reason})` : ""}`;
}

function formatComparison(comparison = {}) {
  const status = String(comparison?.status || "unknown").trim() || "unknown";
  const reason = String(comparison?.reason || "").trim();
  const lexical = count(comparison?.lexical_count);
  const hybrid = count(comparison?.hybrid_count);
  const added = Array.isArray(comparison?.added) ? comparison.added.length : 0;
  const removed = Array.isArray(comparison?.removed) ? comparison.removed.length : 0;
  const reordered = Array.isArray(comparison?.reordered) ? comparison.reordered.length : 0;
  return `${status}${reason ? `(${reason})` : ""} L${lexical}/H${hybrid} +${added}/-${removed}/~${reordered}`;
}

function count(value) {
  const parsed = Number.parseInt(String(value ?? 0), 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}
