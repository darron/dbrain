<script>
  import { renderMarkdown } from "../lib/markdown.js";

  export let markdown = "";
  export let linkSourceKeys = false;
  export let onLookup = () => {};
  export let showSourceKeyPins = false;
  export let sourceKeyPinVersion = "";
  export let isSourceKeyPinned = () => false;
  export let onPinSourceKey = () => {};

  $: html = renderMarkdown(markdown, { linkSourceKeys });
  $: sourceKeyOptions = { html, showSourceKeyPins, sourceKeyPinVersion };

  function sourceKeyLinks(node, options) {
    decorateSourceKeyPins(node, options);
    node.addEventListener("click", handleClick);
    return {
      update(nextOptions) {
        decorateSourceKeyPins(node, nextOptions);
      },
      destroy() {
        node.removeEventListener("click", handleClick);
      }
    };
  }

  function handleClick(event) {
    const pin = event.target?.closest?.("button[data-pin-lookup]");
    if (pin) {
      event.preventDefault();
      onPinSourceKey(pin.dataset.pinLookup);
      return;
    }

    const link = event.target?.closest?.("a[data-lookup]");
    if (!link) return;
    event.preventDefault();
    onLookup(link.dataset.lookup);
  }

  function decorateSourceKeyPins(node, options) {
    node.querySelectorAll("button[data-pin-lookup]").forEach((button) => button.remove());
    if (!options?.showSourceKeyPins) return;
    node.querySelectorAll("a[data-lookup]").forEach((link) => {
      const lookup = link.dataset.lookup;
      if (!lookup) return;
      const pinned = isSourceKeyPinned(lookup);
      const button = document.createElement("button");
      button.type = "button";
      button.className = pinned ? "source-key-pin active" : "source-key-pin";
      button.dataset.pinLookup = lookup;
      button.setAttribute("aria-pressed", pinned ? "true" : "false");
      button.textContent = "Pin";
      button.title = pinned ? "Remove this evidence pin" : "Pin this evidence for follow-up turns";
      link.insertAdjacentElement("afterend", button);
    });
  }
</script>

{#if html}
  <article class="note-markdown" use:sourceKeyLinks={sourceKeyOptions}>
    {@html html}
  </article>
{/if}
