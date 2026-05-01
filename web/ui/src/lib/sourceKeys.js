export function normalizeLookupKey(rawKey) {
  let value = String(rawKey || "").trim();
  while (value.startsWith("src:src:")) {
    value = value.slice("src:".length);
  }
  if (value.startsWith("src:apple-note:")) {
    return value.slice("src:".length);
  }
  return value;
}
