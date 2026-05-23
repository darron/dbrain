import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import test from "node:test";

const here = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(here, "../App.svelte"), "utf8");

test("chat is default and research is absent from primary mode tabs", () => {
  assert.match(appSource, /let inputMode = "chat"/);
  assert.doesNotMatch(appSource, /class:active=\{inputMode === "research"\}[\s\S]{0,120}>\s*Research\s*</);
  assert.match(appSource, />\s*Chat\s*</);
  assert.match(appSource, />\s*Harness\s*</);
  assert.match(appSource, /progress-timeline/);
});
