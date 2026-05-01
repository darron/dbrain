export function isOpenableOriginalURL(rawURL) {
  const value = String(rawURL || "").trim();
  if (!value) return false;
  try {
    const parsed = new URL(value);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
}

export function isAppleNoteItem(item) {
  return item?.source_type === "apple_note";
}

export function appleNoteBodyText(item) {
  if (!isAppleNoteItem(item)) return "";
  return String(item?.text || "").trim();
}

export function appleNoteAttachmentText(item) {
  if (!isAppleNoteItem(item)) return "";
  return String(item?.article_text || "").trim();
}
