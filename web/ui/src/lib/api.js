async function fetchJSON(url, options = {}) {
  const response = await fetch(url, options);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `Request failed with status ${response.status}`);
  }
  return payload;
}

export function getBootstrap() {
  return fetchJSON("/api/bootstrap");
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

export function askEvidence(question, options = {}) {
  return fetchJSON("/api/ask", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      question,
      limit: 8,
      include_related: true,
      related_limit: 2,
      ...options
    })
  });
}

export function tagItem(lookup, tags) {
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
