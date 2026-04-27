<script>
  export let detailState = "idle";
  export let detailError = "";
  export let detail = null;
  export let onSelect = () => {};
  export let onSearch = () => {};

  $: item = detail?.kind === "item" ? detail.item : null;
  $: source = detail?.kind === "source" ? detail.source : null;
  $: rec = item ?? source;

  $: titleText = !rec
    ? "Select a node"
    : (rec.title || rec.source_key || "—");

  $: canonURL = rec?.canonical_url ?? "";
  $: stype = rec?.source_type ?? "";

  $: isX = stype === "x_bookmark" || stype === "x_quote";
  $: isGitHub = stype === "github_star" || stype === "github";
  $: isYouTube = stype === "youtube_watch_later" || stype === "youtube_liked" || stype === "youtube";

  $: summaryText = rec?.summary_text ?? "";
  $: postText = item?.x_post_text ?? "";
  $: bodyText = item?.article_text || item?.text || source?.extracted_text || "";
  $: description = source?.description ?? item?.article_title ?? "";
  $: ocrText = item?.ocr_text ?? "";

  $: authorHandle = item?.author_handle ?? "";
  $: authorName = item?.author_name ?? "";
  $: publishedAt = item?.published_at ? fmtDate(item.published_at) : "";
  $: domain = item?.primary_domain || source?.domain || "";
  $: category = item?.primary_category ?? "";

  $: likeCount = item?.like_count ?? 0;
  $: repostCount = item?.repost_count ?? 0;
  $: media = item?.media ?? [];

  function mediaURL(ref) {
    if (ref.archive_status === "archived" && ref.media_asset_id > 0)
      return `/media/asset/${ref.media_asset_id}`;
    if (ref.archive_url) return ref.archive_url;
    if (ref.remote_url) return ref.remote_url;
    return null;
  }

  $: linkedCount = detail?.kind === "item"
    ? (detail.linked_sources?.length || 0)
    : (detail?.backlinks?.length || 0);

  $: categories = parseList(item?.categories).filter(c => c !== category);
  $: domains = parseList(item?.domains).filter(d => d !== domain);
  $: folderNames = parseList(item?.folder_names);
  $: githubURLs = parseList(item?.github_urls);
  $: language = (item?.language || item?.x_post_lang || "").trim();

  function parseList(s) {
    if (!s) return [];
    try {
      const parsed = JSON.parse(s);
      if (Array.isArray(parsed)) return parsed.map(String).filter(Boolean);
    } catch {}
    return s.split(",").map(x => x.trim()).filter(Boolean);
  }

  function fmtDate(s) {
    if (!s) return "";
    try {
      return new Date(s).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
    } catch {
      return s;
    }
  }

  function truncate(text, max) {
    if (!text || text.length <= max) return text;
    return text.slice(0, max).trimEnd() + "…";
  }
</script>

<div class="detail-panel">
  <div class="panel-header">
    <div style="min-width:0">
      <p class="panel-kicker">Detail</p>
      <h2 style="overflow-wrap:anywhere">{titleText}</h2>
    </div>
  </div>

  {#if detailState === "loading"}
    <p class="message muted">Loading…</p>
  {:else if detailError}
    <p class="message error">{detailError}</p>
  {:else if rec}

    <!-- Meta row -->
    <div class="detail-meta">
      {#if stype}
        <span class="meta-chip type-badge type-{stype}">{stype}</span>
      {/if}
      {#if domain}
        <span class="meta-chip">{domain}</span>
      {/if}
      {#if category}
        <span class="meta-chip">{category}</span>
      {/if}
      {#if linkedCount > 0}
        <span class="meta-chip">
          {detail.kind === "item" ? linkedCount + " sources" : linkedCount + " backlinks"}
        </span>
      {/if}
    </div>

    <!-- Author / date row -->
    {#if authorHandle || authorName || publishedAt}
      <div class="detail-byline">
        {#if authorHandle}
          <button class="byline-handle" on:click={() => onSearch(authorHandle)} type="button">
            @{authorHandle}
          </button>
        {:else if authorName}
          <span class="byline-name">{authorName}</span>
        {/if}
        {#if publishedAt}
          <span class="byline-date">{publishedAt}</span>
        {/if}
      </div>
    {/if}

    <!-- Actions -->
    <div class="detail-actions">
      {#if canonURL}
        <a class="link-chip" href={canonURL} rel="noopener noreferrer" target="_blank">
          ↗ Open original
        </a>
      {/if}
    </div>

    <!-- Pivot chips -->
    {#if categories.length || domains.length || folderNames.length || (language && language !== "en")}
      <div class="detail-section pivot-chips">
        {#each categories as cat}
          <button class="pivot-chip" on:click={() => onSearch(cat)} type="button">{cat}</button>
        {/each}
        {#each domains as d}
          <button class="pivot-chip pivot-chip--domain" on:click={() => onSearch(d)} type="button">{d}</button>
        {/each}
        {#each folderNames as f}
          <button class="pivot-chip pivot-chip--folder" on:click={() => onSearch(f)} type="button">📁 {f}</button>
        {/each}
        {#if language && language !== "en"}
          <span class="pivot-chip pivot-chip--lang">{language}</span>
        {/if}
      </div>
    {/if}

    <!-- GitHub links -->
    {#if githubURLs.length}
      <div class="detail-section">
        <h3>GitHub</h3>
        <div style="display:flex;flex-direction:column;gap:0.3rem">
          {#each githubURLs as url}
            <a class="link-chip" href={url} rel="noopener noreferrer" target="_blank" style="font-size:0.8rem">{url.replace("https://github.com/", "")}</a>
          {/each}
        </div>
      </div>
    {/if}

    <!-- X post card -->
    {#if isX && postText}
      <div class="detail-section">
        <div class="post-card">
          <p class="post-text">{postText}</p>
          {#if likeCount > 0 || repostCount > 0}
            <div class="post-stats">
              {#if likeCount > 0}<span>♥ {likeCount.toLocaleString()}</span>{/if}
              {#if repostCount > 0}<span>↺ {repostCount.toLocaleString()}</span>{/if}
            </div>
          {/if}
        </div>
      </div>
    {/if}

    <!-- Media -->
    {#if media.length}
      <div class="detail-section">
        <div class="media-grid">
          {#each media as ref (ref.media_asset_id)}
            {@const url = mediaURL(ref)}
            {#if url}
              {#if ref.media_type === "photo"}
                <a href={url} rel="noopener noreferrer" target="_blank" class="media-item">
                  <img src={url} alt="" loading="lazy" />
                </a>
              {:else if ref.media_type === "animated_gif"}
                <!-- svelte-ignore a11y_media_has_caption -->
                <div class="media-item">
                  <video src={url} autoplay loop muted playsinline></video>
                </div>
              {:else if ref.media_type === "video"}
                <!-- svelte-ignore a11y_media_has_caption -->
                <div class="media-item media-item--video">
                  <video src={url} controls playsinline></video>
                </div>
              {/if}
            {/if}
          {/each}
        </div>
      </div>
    {/if}

    <!-- Quoted posts -->
    {#if detail?.quoted_posts?.length}
      <div class="detail-section">
        {#each detail.quoted_posts as qp}
          <div class="quoted-post-card">
            <div class="quoted-post-header">
              {#if qp.author_handle}
                <button class="byline-handle" on:click={() => onSearch(qp.author_handle)} type="button" style="font-size:0.78rem">
                  @{qp.author_handle}
                </button>
              {/if}
              {#if qp.published_at}
                <span class="byline-date" style="font-size:0.75rem">{fmtDate(qp.published_at)}</span>
              {/if}
            </div>
            <p class="post-text" style="font-size:0.85rem">{qp.x_post_text || qp.text || ""}</p>
            {#if qp.media?.length}
              <div class="media-grid" style="margin-top:0.5rem">
                {#each qp.media as ref (ref.media_asset_id)}
                  {@const url = mediaURL(ref)}
                  {#if url && ref.media_type === "photo"}
                    <a href={url} rel="noopener noreferrer" target="_blank" class="media-item">
                      <img src={url} alt="" loading="lazy" />
                    </a>
                  {/if}
                {/each}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}

    <!-- Summary -->
    {#if summaryText}
      <div class="detail-section">
        <h3>Summary</h3>
        <div class="summary-card">
          <p>{summaryText}</p>
        </div>
      </div>
    {/if}

    <!-- Source description -->
    {#if !isX && description}
      <div class="detail-section">
        <p class="body-excerpt">{description}</p>
      </div>
    {/if}

    <!-- Body text excerpt -->
    {#if !isX && bodyText}
      <div class="detail-section">
        <h3>{isGitHub ? "README" : isYouTube ? "Transcript" : "Extracted text"}</h3>
        <p class="body-excerpt">{truncate(bodyText, 800)}</p>
      </div>
    {/if}

    <!-- OCR text -->
    {#if ocrText}
      <div class="detail-section">
        <h3>Image text</h3>
        <p class="body-excerpt">{truncate(ocrText, 400)}</p>
      </div>
    {/if}

    <!-- Linked sources -->
    {#if detail.kind === "item" && detail.linked_sources?.length}
      <div class="detail-section">
        <h3>Linked Sources ({detail.linked_sources.length})</h3>
        <div class="relation-grid">
          {#each detail.linked_sources as ref}
            <button class="relation-card" on:click={() => onSelect(ref.source_key)} type="button">
              <span class="result-key">{ref.source_type || "source"}</span>
              <strong>{ref.title || ref.canonical_url}</strong>
              <small>{ref.canonical_url}</small>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Backlinks -->
    {#if detail.kind === "source" && detail.backlinks?.length}
      <div class="detail-section">
        <h3>Referenced by ({detail.backlinks.length})</h3>
        <div class="relation-grid">
          {#each detail.backlinks as ref}
            <button class="relation-card" on:click={() => onSelect(ref.source_key)} type="button">
              <span class="result-key">{ref.source_type || "item"}</span>
              <strong>{ref.title || ref.canonical_url}</strong>
              {#if ref.author_handle}
                <small>@{ref.author_handle}</small>
              {/if}
            </button>
          {/each}
        </div>
      </div>
    {/if}

  {:else}
    <p class="message muted">Select a result or graph node to inspect it.</p>
  {/if}
</div>

<style>
  .detail-byline {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
    padding: 0 1.25rem 0.5rem;
    font-size: 0.82rem;
  }

  .byline-handle {
    color: var(--accent);
    text-decoration: none;
    font-weight: 500;
  }
  .byline-handle:hover { text-decoration: underline; }

  .byline-name {
    color: var(--text);
  }

  .byline-date {
    color: var(--text-lo);
    margin-left: auto;
  }

  .post-card {
    background: rgba(74, 222, 128, 0.04);
    border: 1px solid var(--border-hi);
    border-radius: var(--radius-sm);
    padding: 0.85rem 1rem;
  }

  .post-text {
    color: var(--text-hi);
    font-size: 0.9rem;
    line-height: 1.55;
    white-space: pre-wrap;
    margin: 0;
  }

  .post-stats {
    display: flex;
    gap: 1rem;
    margin-top: 0.6rem;
    font-size: 0.78rem;
    color: var(--text-lo);
  }

  .summary-card {
    background: rgba(74, 222, 128, 0.03);
    border-left: 2px solid var(--accent);
    padding: 0.7rem 0.9rem;
    border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  }

  .summary-card p {
    margin: 0;
    color: var(--text);
    font-size: 0.875rem;
    line-height: 1.6;
  }

  .body-excerpt {
    color: var(--text);
    font-size: 0.85rem;
    line-height: 1.65;
    white-space: pre-wrap;
    margin: 0;
    opacity: 0.85;
  }

  .quoted-post-card {
    background: rgba(74, 222, 128, 0.03);
    border: 1px solid var(--border);
    border-left: 2px solid rgba(74, 222, 128, 0.3);
    border-radius: var(--radius-sm);
    padding: 0.7rem 0.9rem;
    margin-top: 0.5rem;
  }

  .quoted-post-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.4rem;
  }

  .pivot-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    padding-top: 0;
  }

  .pivot-chip {
    background: rgba(74, 222, 128, 0.07);
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    cursor: pointer;
    font-size: 0.75rem;
    padding: 0.2rem 0.65rem;
    transition: background 0.15s, border-color 0.15s;
  }
  .pivot-chip:hover {
    background: rgba(74, 222, 128, 0.18);
    border-color: var(--border-hi);
    color: var(--text-hi);
  }

  .pivot-chip--domain { color: var(--text-lo); }
  .pivot-chip--folder { color: var(--accent); }
  .pivot-chip--lang   { cursor: default; opacity: 0.6; }

  .media-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 0.5rem;
  }

  .media-item {
    display: block;
    border-radius: var(--radius-sm);
    overflow: hidden;
    background: var(--s2);
    border: 1px solid var(--border);
    line-height: 0;
  }

  .media-item img,
  .media-item video {
    width: 100%;
    height: auto;
    display: block;
    object-fit: cover;
  }

  .media-item--video {
    grid-column: 1 / -1;
  }

  .media-item--video video {
    max-height: 320px;
    object-fit: contain;
    background: #000;
  }
</style>
