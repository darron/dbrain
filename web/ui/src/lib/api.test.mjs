import assert from "node:assert/strict";
import test from "node:test";

import { compareResearchTrace, listResearchTraces, researchBrain } from "./api.js";

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
