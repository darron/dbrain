import assert from "node:assert/strict";
import test from "node:test";

import { researchBrain } from "./api.js";

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
