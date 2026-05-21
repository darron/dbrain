<script>
  import MediaStrip from "./MediaStrip.svelte";

  export let item = {};
  export let onSelect = () => {};
  export let showHeader = true;
  export let compact = true;

  function mediaList(value) {
    return Array.isArray(value) ? value : [];
  }

  function trimText(value, max = 180) {
    const text = String(value || "").replace(/\s+/g, " ").trim();
    if (text.length <= max) return text;
    return `${text.slice(0, max - 1).trim()}…`;
  }

  function title(value) {
    return value?.title || value?.canonical_url || value?.url || value?.source_key || "Media evidence";
  }

  function host(value) {
    const raw = value?.primary_domain || value?.domain || value?.url || value?.canonical_url || "";
    if (!raw) return "";
    if (!raw.includes("://")) return raw;
    try {
      return new URL(raw).hostname.replace(/^www\./, "");
    } catch {
      return "";
    }
  }

  function author(value) {
    if (value?.author) return value.author;
    if (value?.author_handle && value?.author_name) return `${value.author_name} @${value.author_handle}`;
    if (value?.author_handle) return `@${value.author_handle}`;
    if (value?.author_name) return value.author_name;
    return "";
  }

  function mediaTypeLabel(value) {
    const types = [...new Set(mediaList(value?.media).map((ref) => ref.media_type).filter(Boolean))];
    if (types.length === 0) return "";
    if (types.length === 1) return types[0].replace("_", " ");
    return `${mediaList(value?.media).length} media`;
  }

  function dimensions(value) {
    const first = mediaList(value?.media).find((ref) => ref.width || ref.height);
    if (!first) return "";
    if (first.width && first.height) return `${first.width}×${first.height}`;
    if (first.width) return `${first.width}px wide`;
    return `${first.height}px tall`;
  }

  function matchLabel(value) {
    const types = mediaList(value?.media).map((ref) => ref.media_type);
    if (types.some((type) => type === "video" || type === "animated_gif" || type === "audio")) return "Transcript match";
    if (types.includes("photo")) return "OCR match";
    return "Evidence match";
  }

  function summaryText(value) {
    return trimText(value?.summary || "", 220);
  }

  function matchText(value) {
    const text = value?.excerpt || value?.snippet || "";
    const summary = value?.summary || "";
    if (text && text.trim() === summary.trim()) return "";
    return trimText(text, 220);
  }

  function metadata(value) {
    return [
      mediaTypeLabel(value),
      dimensions(value),
      value?.source_type || value?.kind || "",
      author(value),
      host(value),
      value?.source_key || "",
    ].filter(Boolean);
  }

  $: hasMedia = mediaList(item.media).length > 0;
</script>

{#if hasMedia}
  <section class="media-evidence-block">
    {#if showHeader}
      <div class="media-evidence-header">
        <button class="media-evidence-title" type="button" on:click={() => onSelect(item.source_key)} title={title(item)}>
          {title(item)}
        </button>
        <div class="media-evidence-meta">
          {#each metadata(item) as part}
            <span>{part}</span>
          {/each}
        </div>
      </div>
    {:else}
      <div class="media-evidence-meta media-evidence-meta--inline">
        {#each metadata(item) as part}
          <span>{part}</span>
        {/each}
      </div>
    {/if}

    <MediaStrip media={item.media || []} compact={compact} />

    {#if summaryText(item) || matchText(item)}
      <div class="media-context-links">
        {#if summaryText(item)}
          <button class="media-context-link" type="button" on:click={() => onSelect(item.source_key)}>
            <span>Summary</span>
            <small>{summaryText(item)}</small>
          </button>
        {/if}
        {#if matchText(item)}
          <button class="media-context-link" type="button" on:click={() => onSelect(item.source_key)}>
            <span>{matchLabel(item)}</span>
            <small>{matchText(item)}</small>
          </button>
        {/if}
      </div>
    {/if}
  </section>
{/if}
