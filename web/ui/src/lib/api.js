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

export function searchBrain(query, limit = 12) {
  const params = new URLSearchParams({
    q: query,
    limit: String(limit)
  });
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
