import assert from "node:assert/strict";
import test from "node:test";

import { formatSemanticDiagnostics } from "./researchDiagnostics.js";

test("semantic diagnostics compactly identify modes, lanes, and attempt comparisons", () => {
  const got = formatSemanticDiagnostics({
    query_plan: {
      semantic_mode: "shadow",
      retrieval_lanes: [
        { name: "lexical", status: "used" },
        { name: "semantic", status: "disabled", reason: "too_large" }
      ],
      shadow_comparison: { status: "searched", lexical_count: 4, hybrid_count: 5, added: [{}], removed: [], reordered: [{}, {}] },
      retry_shadow_comparison: { status: "unavailable", reason: "too_large", lexical_count: 2, hybrid_count: 2, added: [], removed: [], reordered: [] }
    }
  });

  assert.equal(got, "Semantic shadow · lanes lexical=used, semantic=disabled(too_large) · shadow initial=searched L4/H5 +1/-0/~2; retry=unavailable(too_large) L2/H2 +0/-0/~0");
});

test("semantic diagnostics remain useful for lexical-only packs", () => {
  assert.equal(formatSemanticDiagnostics({ query_plan: { semantic_mode: "off" } }), "Semantic off");
  assert.equal(formatSemanticDiagnostics({}), "");
});
