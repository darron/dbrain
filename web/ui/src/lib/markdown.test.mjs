import assert from "node:assert/strict";
import test from "node:test";

import { extractSourceKeyReferences } from "./markdown.js";

test("extractSourceKeyReferences keeps colon-delimited source keys intact", () => {
  const text = [
    "[src:apple-note:default:5fef8e35-f1e8-4ae5-a08b-3382b5805fc8]",
    "src:rcmp:ca-5c6223ca33fc.",
    "apple-note:default:78a7ede-fb95-46ed-8dae-562aa0f66830",
    "src:787343fabc91"
  ].join(" ");

  assert.deepEqual(extractSourceKeyReferences(text), [
    "src:apple-note:default:5fef8e35-f1e8-4ae5-a08b-3382b5805fc8",
    "src:rcmp:ca-5c6223ca33fc",
    "apple-note:default:78a7ede-fb95-46ed-8dae-562aa0f66830",
    "src:787343fabc91"
  ]);
});
