function currentURL() {
  if (typeof window === "undefined") {
    return null;
  }
  return new URL(window.location.href);
}

export function readRouteState() {
  const url = currentURL();
  if (!url) {
    return { q: "", lookup: "", ask: "" };
  }

  return {
    q: url.searchParams.get("q") || "",
    lookup: url.searchParams.get("lookup") || "",
    ask: url.searchParams.get("ask") || ""
  };
}

export function writeRouteState(state) {
  const url = currentURL();
  if (!url) {
    return;
  }

  const params = url.searchParams;
  applyParam(params, "q", state.q);
  applyParam(params, "lookup", state.lookup);
  applyParam(params, "ask", state.ask);

  const next = `${url.pathname}${params.toString() ? `?${params.toString()}` : ""}${url.hash}`;
  window.history.replaceState({}, "", next);
}

function applyParam(params, key, value) {
  const next = String(value || "").trim();
  if (next) {
    params.set(key, next);
    return;
  }
  params.delete(key);
}
