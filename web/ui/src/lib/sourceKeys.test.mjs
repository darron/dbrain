import assert from "node:assert/strict";
import test from "node:test";

import { normalizeLookupKey } from "./sourceKeys.js";

test("normalizeLookupKey strips model-added source prefixes", () => {
  assert.equal(
    normalizeLookupKey("src:apple-note:default:78a7ede-fb95-46ed-8dae-562aa0f66830"),
    "apple-note:default:78a7ede-fb95-46ed-8dae-562aa0f66830"
  );
  assert.equal(normalizeLookupKey("src:src:787343fabc91"), "src:787343fabc91");
  assert.equal(normalizeLookupKey("src:787343fabc91"), "src:787343fabc91");
});
