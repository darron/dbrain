import DOMPurify from "dompurify";
import { marked } from "marked";

marked.setOptions({
  gfm: true,
  breaks: true
});

export function renderMarkdown(markdown) {
  const source = String(markdown || "").trim();
  if (!source) {
    return "";
  }

  const html = marked.parse(source);
  return DOMPurify.sanitize(html);
}
