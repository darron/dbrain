<script>
  import { renderMarkdown } from "../lib/markdown.js";

  export let markdown = "";
  export let linkSourceKeys = false;
  export let onLookup = () => {};

  $: html = renderMarkdown(markdown, { linkSourceKeys });

  function sourceKeyLinks(node) {
    node.addEventListener("click", handleClick);
    return {
      destroy() {
        node.removeEventListener("click", handleClick);
      }
    };
  }

  function handleClick(event) {
    const link = event.target?.closest?.("a[data-lookup]");
    if (!link) return;
    event.preventDefault();
    onLookup(link.dataset.lookup);
  }
</script>

{#if html}
  <article class="note-markdown" use:sourceKeyLinks>
    {@html html}
  </article>
{/if}
