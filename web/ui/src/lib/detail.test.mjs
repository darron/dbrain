import assert from "node:assert/strict";
import test from "node:test";

import { appleNoteAttachmentText, appleNoteBodyText, isOpenableOriginalURL } from "./detail.js";

test("isOpenableOriginalURL rejects synthetic Apple Notes URLs", () => {
  assert.equal(isOpenableOriginalURL("apple-notes://default/5fef8e35"), false);
  assert.equal(isOpenableOriginalURL("https://example.com/page"), true);
});

test("appleNoteBodyText keeps note body separate from attachment text", () => {
  const item = {
    source_type: "apple_note",
    text: "Full decoded note body",
    article_text: "Attachment OCR text"
  };

  assert.equal(appleNoteBodyText(item), "Full decoded note body");
  assert.equal(appleNoteAttachmentText(item), "Attachment OCR text");
});
