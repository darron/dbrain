<script>
  import { renderMarkdown } from "../lib/markdown.js";

  export let markdown = "";

  $: html = renderMarkdown(markdown);
</script>

{#if html}
  <article class="note-markdown">
    {@html html}
  </article>
{/if}
