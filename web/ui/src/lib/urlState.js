function currentURL() {
  if (typeof window === "undefined") {
    return null;
  }
  return new URL(window.location.href);
}

function routePage(pathname) {
  return pathname === "/admin" ? "admin" : "home";
}

export function readRouteState() {
  const url = currentURL();
  if (!url) {
    return {
      page: "home",
      q: "",
      lookup: "",
      research: "",
      activityDomain: "",
      activityType: "",
      activityLimit: "8",
      activityOffset: "0",
      activitySort: "newest",
      activityStatus: "",
      activityFailureKind: "",
      activityMessage: "",
      activityWindow: "24h"
    };
  }

  return {
    page: routePage(url.pathname),
    q: url.searchParams.get("q") || "",
    lookup: url.searchParams.get("lookup") || "",
    research: url.searchParams.get("research") || "",
    activityDomain: url.searchParams.get("activity_domain") || "",
    activityType: url.searchParams.get("activity_type") || "",
    activityLimit: url.searchParams.get("activity_limit") || "8",
    activityOffset: url.searchParams.get("activity_offset") || "0",
    activitySort: url.searchParams.get("activity_sort") || "newest",
    activityStatus: url.searchParams.get("activity_status") || "",
    activityFailureKind: url.searchParams.get("activity_failure_kind") || "",
    activityMessage: url.searchParams.get("activity_message") || "",
    activityWindow: url.searchParams.get("activity_window") || "24h"
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
  applyParam(params, "research", state.research);
  applyParam(params, "activity_domain", state.activityDomain);
  applyParam(params, "activity_type", state.activityType);
  applyParam(params, "activity_limit", state.activityLimit);
  applyParam(params, "activity_offset", state.activityOffset);
  applyParam(params, "activity_sort", state.activitySort);
  applyParam(params, "activity_status", state.activityStatus);
  applyParam(params, "activity_failure_kind", state.activityFailureKind);
  applyParam(params, "activity_message", state.activityMessage);
  applyParam(params, "activity_window", state.activityWindow);

  const next = `${url.pathname}${params.toString() ? `?${params.toString()}` : ""}${url.hash}`;
  window.history.replaceState({}, "", next);
}

export function pageHref(page) {
  const url = currentURL();
  const pathname = page === "admin" ? "/admin" : "/";
  if (!url) {
    return pathname;
  }
  url.pathname = pathname;
  return `${url.pathname}${url.search}${url.hash}`;
}

function applyParam(params, key, value) {
  const next = String(value || "").trim();
  if (next) {
    params.set(key, next);
    return;
  }
  params.delete(key);
}
