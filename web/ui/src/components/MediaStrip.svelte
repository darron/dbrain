<script>
  export let media = [];
  export let compact = false;

  function mediaURL(ref) {
    if (ref.archive_status === "archived" && ref.media_asset_id > 0) {
      return `/media/asset/${ref.media_asset_id}`;
    }
    if (ref.archive_url) return ref.archive_url;
    if (ref.remote_url) return ref.remote_url;
    return null;
  }

  function mediaKey(ref) {
    return `${ref.media_asset_id || ref.remote_url || ref.expanded_url || "media"}-${ref.ordinal || 0}`;
  }

  $: visibleMedia = (Array.isArray(media) ? media : [])
    .map((ref) => ({ ...ref, url: mediaURL(ref) }))
    .filter((ref) => ref.url);
  $: displayedMedia = visibleMedia.slice(0, compact ? 4 : 8);
</script>

{#if displayedMedia.length}
  <div class="inline-media-strip" class:inline-media-strip--compact={compact}>
    {#each displayedMedia as ref (mediaKey(ref))}
      {#if ref.media_type === "photo"}
        <a class="inline-media-item" href={ref.url} target="_blank" rel="noopener noreferrer" on:click|stopPropagation>
          <img src={ref.url} alt="" loading="lazy" />
        </a>
      {:else if ref.media_type === "animated_gif"}
        <!-- svelte-ignore a11y_media_has_caption -->
        <div class="inline-media-item inline-media-item--video">
          <video src={ref.url} autoplay loop muted playsinline preload="metadata"></video>
        </div>
      {:else if ref.media_type === "video"}
        <!-- svelte-ignore a11y_media_has_caption -->
        <div class="inline-media-item inline-media-item--wide">
          <video src={ref.url} controls playsinline preload="metadata"></video>
        </div>
      {:else if ref.media_type === "audio"}
        <div class="inline-media-item inline-media-item--audio">
          <audio src={ref.url} controls preload="metadata"></audio>
        </div>
      {/if}
    {/each}
  </div>
{/if}
