import DOMPurify from "dompurify";
import { marked } from "marked";

import { normalizeLookupKey } from "./sourceKeys.js";

marked.setOptions({
  gfm: true,
  breaks: true
});

const sourceKeyBodyPattern = [
  "src:[A-Za-z0-9_:/.-]*[A-Za-z0-9_-]",
  "apple-note:[A-Za-z0-9_-]+:[A-Za-z0-9_-]+",
  "gh-star:[A-Za-z0-9_:/.-]*[A-Za-z0-9_-]",
  "x:[A-Za-z0-9_-]+",
  "youtube:[A-Za-z0-9_-]+",
  "item:[A-Za-z0-9_-]+"
].join("|");

const sourceKeyPattern = new RegExp(`\\[(${sourceKeyBodyPattern})\\]|(^|[^\\w:/.-])(${sourceKeyBodyPattern})`, "g");

export function renderMarkdown(markdown, options = {}) {
  const source = String(markdown || "").trim();
  if (!source) {
    return "";
  }

  const html = marked.parse(source);
  const clean = DOMPurify.sanitize(html);
  if (options.linkSourceKeys) {
    return linkSourceKeyReferences(clean);
  }
  return clean;
}

export function extractSourceKeyReferences(text) {
  const references = [];
  sourceKeyPattern.lastIndex = 0;
  let match;
  while ((match = sourceKeyPattern.exec(String(text || ""))) !== null) {
    references.push(match[1] || match[3]);
  }
  return references;
}

function linkSourceKeyReferences(html) {
  if (typeof document === "undefined") {
    return html;
  }

  const template = document.createElement("template");
  template.innerHTML = html;
  const walker = document.createTreeWalker(template.content, NodeFilter.SHOW_TEXT);
  const nodes = [];
  while (walker.nextNode()) {
    nodes.push(walker.currentNode);
  }

  for (const node of nodes) {
    if (shouldSkipNode(node)) {
      continue;
    }
    const text = node.nodeValue || "";
    sourceKeyPattern.lastIndex = 0;
    if (!sourceKeyPattern.test(text)) {
      continue;
    }

    sourceKeyPattern.lastIndex = 0;
    const fragment = document.createDocumentFragment();
    let lastIndex = 0;
    let match;
    while ((match = sourceKeyPattern.exec(text)) !== null) {
      if (match.index > lastIndex) {
        fragment.appendChild(document.createTextNode(text.slice(lastIndex, match.index)));
      }
      const bracketedSourceKey = match[1];
      const prefix = bracketedSourceKey ? "" : match[2] || "";
      const sourceKey = bracketedSourceKey || match[3];
      if (prefix) {
        fragment.appendChild(document.createTextNode(prefix));
      }
      fragment.appendChild(sourceKeyLink(sourceKey, Boolean(bracketedSourceKey)));
      lastIndex = match.index + match[0].length;
    }
    if (lastIndex < text.length) {
      fragment.appendChild(document.createTextNode(text.slice(lastIndex)));
    }
    node.parentNode?.replaceChild(fragment, node);
  }

  return template.innerHTML;
}

function sourceKeyLink(sourceKey, bracketed) {
  const lookupKey = normalizeLookupKey(sourceKey);
  const link = document.createElement("a");
  link.href = "#";
  link.className = "source-key-link";
  link.dataset.lookup = lookupKey;
  link.textContent = bracketed ? `[${lookupKey}]` : lookupKey;
  return link;
}

function shouldSkipNode(node) {
  let current = node.parentElement;
  while (current) {
    if (["A", "CODE", "PRE", "SCRIPT", "STYLE"].includes(current.tagName)) {
      return true;
    }
    current = current.parentElement;
  }
  return false;
}
