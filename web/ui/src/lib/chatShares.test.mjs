import assert from "node:assert/strict";
import test from "node:test";

import { confirmShareDeletion, createShareListGeneration, nextShareDeleteState, removeDeletedShareReferences } from "./chatShares.js";

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

test("share deletion requires explicit confirmation with the affected title", () => {
  const messages = [];
  const declined = confirmShareDeletion((message) => {
    messages.push(message);
    return false;
  }, { title: "Deletion check" });

  assert.equal(declined, false);
  assert.deepEqual(messages, ["Delete public share “Deletion check”? Its public URL will stop working immediately."]);
  assert.equal(confirmShareDeletion(() => true, {}), true);
  assert.equal(confirmShareDeletion(null, { title: "Deletion check" }), false);
});

test("share deletion state preserves unrelated errors through pending and failure", () => {
  const current = {
    other: { pending: false, error: "Other failure" },
    target: { pending: false, error: "Old target failure" }
  };

  const pending = nextShareDeleteState(current, "target", true);
  assert.deepEqual(pending, {
    other: { pending: false, error: "Other failure" },
    target: { pending: true, error: "" }
  });

  const failed = nextShareDeleteState(pending, "target", false, "Delete failed");
  assert.deepEqual(failed, {
    other: { pending: false, error: "Other failure" },
    target: { pending: false, error: "Delete failed" }
  });
});

test("successful share deletion removes its card and stale turn mappings only", () => {
  const result = removeDeletedShareReferences(
    [
      { slug: "keep", title: "Keep" },
      { slug: "delete", title: "Delete" }
    ],
    {
      "turn-keep": { slug: "keep" },
      "turn-delete": { slug: "delete" },
      "turn-delete-again": { slug: "delete" }
    },
    "delete"
  );

  assert.deepEqual(result, {
    shares: [{ slug: "keep", title: "Keep" }],
    chatShareByTurn: { "turn-keep": { slug: "keep" } }
  });
});

test("a list response started before deletion cannot resurrect the deleted share", async () => {
  const generation = createShareListGeneration();
  const response = deferred();
  const requestGeneration = generation.begin();
  const applyResponse = response.promise.then((shares) => (
    generation.isCurrent(requestGeneration) ? shares : null
  ));

  generation.invalidate();
  response.resolve([{ slug: "deleted", title: "Stale card" }]);

  assert.equal(await applyResponse, null);
});
