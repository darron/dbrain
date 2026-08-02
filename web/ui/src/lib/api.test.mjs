import assert from "node:assert/strict";
import test from "node:test";

import { compareResearchTrace, deleteChatShare, getAuditHistory, getAuditLatest, getAuditRun, listResearchTraces, researchBrain, startAuditRun } from "./api.js";

test("researchBrain passes AbortSignal to fetch without serializing it into JSON", async () => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  let captured = null;
  globalThis.fetch = async (url, options) => {
    captured = { url, options };
    return {
      ok: true,
      json: async () => ({ ok: true })
    };
  };

  try {
    await researchBrain("question", {
      signal: controller.signal,
      limit: 5,
      use_model_planner: false
    });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(captured.url, "/api/research");
  assert.equal(captured.options.signal, controller.signal);
  const body = JSON.parse(captured.options.body);
  assert.equal(body.question, "question");
  assert.equal(body.limit, 5);
  assert.equal(body.use_model_planner, false);
  assert.equal(Object.hasOwn(body, "signal"), false);
});

test("deleteChatShare sends an encoded same-origin DELETE request", async () => {
  const originalFetch = globalThis.fetch;
  let captured = null;
  globalThis.fetch = async (url, options = {}) => {
    captured = { url, options };
    return {
      ok: true,
      status: 204,
      json: async () => {
        throw new SyntaxError("empty response");
      }
    };
  };

  try {
    await deleteChatShare("slug/with spaces");
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(captured.url, "/api/chat/shares/slug%2Fwith%20spaces");
  assert.equal(captured.options.method, "DELETE");
});

test("trace lab API helpers use safe JSON request shapes", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (url, options = {}) => {
    calls.push({ url, options });
    return {
      ok: true,
      json: async () => ({ ok: true })
    };
  };

  try {
    await listResearchTraces({ limit: 12 });
    await compareResearchTrace("research-runs/run-1", { runCurrent: true });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(calls[0].url, "/api/research/traces?limit=12");
  assert.equal(calls[0].options.cache, "no-store");
  assert.equal(calls[1].url, "/api/research/trace-compare");
  assert.equal(calls[1].options.method, "POST");
  assert.equal(calls[1].options.cache, "no-store");
  assert.deepEqual(JSON.parse(calls[1].options.body), {
    trace_path: "research-runs/run-1",
    run_current: true
  });
});

test("trace lab API helper explains network fetch failures", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () => {
    throw new TypeError("Failed to fetch");
  };

  try {
    await assert.rejects(
      () => compareResearchTrace("research-runs/run-1"),
      /Could not reach dbrain web API: Failed to fetch/
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("audit API helpers keep reads uncached and runs explicit and same-origin", async () => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  const calls = [];
  globalThis.fetch = async (url, options = {}) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({ state: "running" }) };
  };
  try {
    await getAuditLatest("standard", { signal: controller.signal });
    await getAuditHistory("standard", 20, { signal: controller.signal });
    await startAuditRun("fast", { signal: controller.signal });
    await getAuditRun("run_0123456789abcdef0123456789abcdef", { signal: controller.signal });
    await assert.rejects(() => getAuditRun("../../share/secret"), /invalid audit run id/i);
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(calls.map((call) => call.url), [
    "/api/audit/latest?profile=standard",
    "/api/audit/history?profile=standard&limit=20",
    "/api/audit/run",
    "/api/audit/runs/run_0123456789abcdef0123456789abcdef"
  ]);
  assert.equal(calls[0].options.cache, "no-store");
  assert.equal(calls[0].options.signal, controller.signal);
  assert.equal(calls[1].options.cache, "no-store");
  assert.equal(calls[1].options.signal, controller.signal);
  assert.equal(calls[2].options.method, "POST");
  assert.equal(calls[2].options.headers["Content-Type"], "application/json");
  assert.equal(calls[2].options.signal, controller.signal);
  assert.deepEqual(JSON.parse(calls[2].options.body), { profile: "fast" });
  assert.equal(calls[3].options.cache, "no-store");
  assert.equal(calls[3].options.signal, controller.signal);
});

test("audit run API errors preserve fixed conflict and rate-limit shapes", async () => {
  const originalFetch = globalThis.fetch;
  const responses = [
    { status: 409, payload: { error: "audit_run_conflict", active_audit_id: "run_0123456789abcdef0123456789abcdef", active_profile: "standard" } },
    { status: 429, payload: { error: "audit_run_rate_limited", retry_after_seconds: 37 } }
  ];
  globalThis.fetch = async () => {
    const next = responses.shift();
    return { ok: false, status: next.status, json: async () => next.payload };
  };
  try {
    await assert.rejects(startAuditRun("fast"), (error) => error.status === 409 && error.payload.active_profile === "standard");
    await assert.rejects(startAuditRun("standard"), (error) => error.status === 429 && error.payload.retry_after_seconds === 37);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
