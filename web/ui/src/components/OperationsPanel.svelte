<script>
  import { formatTime } from "../lib/time.js";

  export let activity = {
    window: "24h",
    recent_successes: [],
    recent_failures: [],
    failure_hotspots: [],
    failure_kinds: [],
    failure_statuses: [],
    failure_domains: [],
    failure_table: [],
    failure_table_total: 0,
    failure_table_offset: 0,
    failure_table_limit: 8,
    failure_table_sort: "newest",
    trend_bucket: "",
    trend: []
  };
  export let filters = {
    sourceType: "",
    domain: "",
    status: "",
    failureKind: "",
    message: "",
    window: "24h",
    limit: 8,
    failureOffset: 0,
    failureSort: "newest"
  };
  export let refreshing = false;
  export let error = "";
  export let onRefresh = () => {};
  export let onApplyFilters = () => {};
  export let onApplyHotspot = () => {};
  export let onClearFilters = () => {};
  export let onSelect = () => {};

  let draftFilters = { ...filters };

  $: draftFilters = { ...filters };
  $: maxTrendCount = Math.max(
    1,
    ...(activity.trend || []).flatMap((point) => [point.success_count || 0, point.failure_count || 0])
  );

  function eventLabel(event) {
    switch (event?.event_kind) {
      case "summary_ok":
        return "Summary saved";
      case "extract_ok":
        return "Extract saved";
      case "extract_empty":
        return "Extract empty";
      case "summary_error":
        return "Summary failed";
      case "extract_dead":
        return "Marked dead";
      case "extract_gone":
        return "Marked gone";
      case "extract_error":
        return "Extract failed";
      default:
        return event?.event_kind || "Source event";
    }
  }

  function eventTitle(event) {
    return event?.title || event?.canonical_url || event?.source_key || "Source";
  }

  function applyFilters() {
    onApplyFilters({
      sourceType: draftFilters.sourceType,
      domain: draftFilters.domain,
      status: draftFilters.status,
      failureKind: draftFilters.failureKind,
      message: draftFilters.message,
      window: draftFilters.window,
      limit: Number.parseInt(String(draftFilters.limit || 8), 10) || 8,
      failureOffset: 0,
      failureSort: draftFilters.failureSort || "newest"
    });
  }

  function clearFilters() {
    draftFilters = {
      sourceType: "",
      domain: "",
      status: "",
      failureKind: "",
      message: "",
      window: "24h",
      limit: 8,
      failureOffset: 0,
      failureSort: "newest"
    };
    onClearFilters();
  }

  function applyHotspot(hotspot) {
    onApplyHotspot(hotspot);
  }

  function toggleFacet(field, value) {
    onApplyFilters({
      ...filters,
      [field]: filters[field] === value ? "" : value,
      message: "",
      window: filters.window,
      limit: Number.parseInt(String(filters.limit || 8), 10) || 8,
      failureOffset: 0
    });
  }

  function applyFailureSort(event) {
    onApplyFilters({
      ...filters,
      failureSort: event.currentTarget.value,
      failureOffset: 0,
      limit: Number.parseInt(String(filters.limit || 8), 10) || 8
    });
  }

  function pageFailures(nextOffset) {
    onApplyFilters({
      ...filters,
      failureOffset: Math.max(0, nextOffset),
      limit: Number.parseInt(String(filters.limit || 8), 10) || 8
    });
  }

  function failureRangeEnd() {
    const total = activity.failure_table_total || 0;
    const start = activity.failure_table_offset || 0;
    const pageSize = activity.failure_table_limit || 0;
    return Math.min(total, start + pageSize);
  }

  function trendHeight(count) {
    if (!count || count <= 0) {
      return "0%";
    }
    return `${Math.max(12, Math.round((count / maxTrendCount) * 100))}%`;
  }
</script>

<section class="panel operations-panel">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Operations</p>
      <h2>Recent Source Activity</h2>
    </div>
    <button class="secondary-button" disabled={refreshing} on:click={onRefresh} type="button">
      {refreshing ? "Refreshing..." : "Refresh Status"}
    </button>
  </div>

  <form class="operations-filters" on:submit|preventDefault={applyFilters}>
    <label>
      Source Type
      <input bind:value={draftFilters.sourceType} placeholder="web" />
    </label>
    <label>
      Domain
      <input bind:value={draftFilters.domain} placeholder="nytimes.com" />
    </label>
    <label>
      Status
      <select bind:value={draftFilters.status}>
        <option value="">all</option>
        <option value="ok">ok</option>
        <option value="empty">empty</option>
        <option value="error">error</option>
        <option value="dead">dead</option>
        <option value="gone">gone</option>
      </select>
    </label>
    <label>
      Failure Kind
      <input bind:value={draftFilters.failureKind} placeholder="tls_certificate" />
    </label>
    <label>
      Message
      <input bind:value={draftFilters.message} placeholder="certificate" />
    </label>
    <label>
      Window
      <select bind:value={draftFilters.window}>
        <option value="6h">6h</option>
        <option value="12h">12h</option>
        <option value="24h">24h</option>
        <option value="72h">72h</option>
        <option value="168h">168h</option>
      </select>
    </label>
    <label>
      Limit
      <select bind:value={draftFilters.limit}>
        <option value="8">8</option>
        <option value="20">20</option>
        <option value="50">50</option>
      </select>
    </label>
    <div class="operations-filter-actions">
      <button class="secondary-button" disabled={refreshing} type="submit">Apply Filters</button>
      <button class="ghost-button" disabled={refreshing} on:click={clearFilters} type="button">Clear</button>
    </div>
  </form>

  {#if error}
    <p class="message error">{error}</p>
  {/if}

  {#if activity.failure_kinds?.length || activity.failure_statuses?.length || activity.failure_domains?.length}
    <section class="operation-column">
      <div class="operation-heading">
        <h3>Failure Facets</h3>
        <small>Quick pivots for repeated breakage</small>
      </div>

      <div class="facet-groups">
        {#if activity.failure_kinds?.length}
          <div class="facet-group">
            <p class="facet-title">Failure Kind</p>
            <div class="facet-row">
              {#each activity.failure_kinds as bucket}
                <button
                  class="facet-chip"
                  class:active={filters.failureKind === bucket.key}
                  on:click={() => toggleFacet("failureKind", bucket.key)}
                  type="button"
                >
                  <span>{bucket.key}</span>
                  <strong>{bucket.count}</strong>
                </button>
              {/each}
            </div>
          </div>
        {/if}

        {#if activity.failure_statuses?.length}
          <div class="facet-group">
            <p class="facet-title">Status</p>
            <div class="facet-row">
              {#each activity.failure_statuses as bucket}
                <button
                  class="facet-chip"
                  class:active={filters.status === bucket.key}
                  on:click={() => toggleFacet("status", bucket.key)}
                  type="button"
                >
                  <span>{bucket.key}</span>
                  <strong>{bucket.count}</strong>
                </button>
              {/each}
            </div>
          </div>
        {/if}

        {#if activity.failure_domains?.length}
          <div class="facet-group">
            <p class="facet-title">Domain</p>
            <div class="facet-row">
              {#each activity.failure_domains as bucket}
                <button
                  class="facet-chip"
                  class:active={filters.domain === bucket.key}
                  on:click={() => toggleFacet("domain", bucket.key)}
                  type="button"
                >
                  <span>{bucket.key}</span>
                  <strong>{bucket.count}</strong>
                </button>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    </section>
  {/if}

  {#if activity.trend?.length}
    <section class="operation-column">
      <div class="operation-heading">
        <h3>Window Trend</h3>
        <small>{activity.trend_bucket || "bucketed"} buckets</small>
      </div>

      <div class="trend-chart">
        {#each activity.trend as point}
          <div class="trend-column" title={`${point.label}: ${point.success_count} success, ${point.failure_count} failure`}>
            <div class="trend-bars">
              <div class="trend-bar success" style={`height: ${trendHeight(point.success_count)};`}></div>
              <div class="trend-bar failure" style={`height: ${trendHeight(point.failure_count)};`}></div>
            </div>
            <small>{point.label}</small>
          </div>
        {/each}
      </div>
      <div class="trend-legend">
        <span><i class="trend-swatch success"></i> successes</span>
        <span><i class="trend-swatch failure"></i> failures</span>
      </div>
    </section>
  {/if}

  <section class="operation-column">
    <div class="operation-heading">
      <h3>Repeated Failure Hotspots</h3>
      <small>{activity.window || filters.window} window</small>
    </div>

      {#if activity.failure_hotspots?.length}
      <div class="hotspot-grid">
        {#each activity.failure_hotspots as hotspot}
          <button class="hotspot-card" on:click={() => applyHotspot(hotspot)} type="button">
            <div class="event-meta">
              <span class="event-badge failure">{hotspot.status}</span>
              <small>{formatTime(hotspot.latest_event_at)}</small>
            </div>
            <strong>{hotspot.domain || "(no domain)"}</strong>
            <p class="event-key">{hotspot.source_type}</p>
            {#if hotspot.failure_kind}
              <p class="event-kind">{hotspot.failure_kind}</p>
            {/if}
            <p class="hotspot-count">{hotspot.count} repeated failures</p>
          </button>
        {/each}
      </div>
    {:else}
      <p class="message muted">No repeated failure hotspots in the current window.</p>
    {/if}
  </section>

  <section class="operation-column">
    <div class="operation-heading">
      <h3>Failure Table</h3>
      <small>
        {#if activity.failure_table_total}
          {activity.failure_table_offset + 1}-{failureRangeEnd()} of {activity.failure_table_total}
        {:else}
          0 events
        {/if}
      </small>
    </div>

    <div class="failure-table-toolbar">
      <label class="inline-control">
        Sort
        <select on:change={applyFailureSort} value={filters.failureSort || "newest"}>
          <option value="newest">Newest</option>
          <option value="oldest">Oldest</option>
          <option value="domain">Domain</option>
          <option value="kind">Failure Kind</option>
          <option value="status">Status</option>
        </select>
      </label>
      <div class="failure-table-pagination">
        <button
          class="ghost-button"
          disabled={refreshing || (activity.failure_table_offset || 0) <= 0}
          on:click={() => pageFailures((activity.failure_table_offset || 0) - (activity.failure_table_limit || 0))}
          type="button"
        >
          Previous
        </button>
        <button
          class="ghost-button"
          disabled={refreshing || failureRangeEnd() >= (activity.failure_table_total || 0)}
          on:click={() => pageFailures((activity.failure_table_offset || 0) + (activity.failure_table_limit || 0))}
          type="button"
        >
          Next
        </button>
      </div>
    </div>

    {#if activity.failure_table?.length}
      <div class="failure-table">
        <div class="failure-table-header">
          <span>When</span>
          <span>Domain</span>
          <span>Type</span>
          <span>Kind</span>
          <span>Status</span>
          <span>Source</span>
        </div>
        {#each activity.failure_table as event}
          <button class="failure-row" on:click={() => onSelect(event.source_key)} type="button">
            <span>{formatTime(event.event_at)}</span>
            <span>{event.domain || "(no domain)"}</span>
            <span>{event.source_type || "source"}</span>
            <span>{event.failure_kind || "unknown"}</span>
            <span>{event.status}</span>
            <span class="failure-row-title">{eventTitle(event)}</span>
          </button>
        {/each}
      </div>
    {:else}
      <p class="message muted">No failure rows for the current filters.</p>
    {/if}
  </section>

  <div class="operations-grid">
    <section class="operation-column">
      <div class="operation-heading">
        <h3>Recent Successes</h3>
        <small>{activity.recent_successes?.length || 0} events</small>
      </div>

      {#if activity.recent_successes?.length}
        <div class="event-list">
          {#each activity.recent_successes as event}
            <button class="event-card event-success" on:click={() => onSelect(event.source_key)} type="button">
              <div class="event-meta">
                <span class="event-badge success">{eventLabel(event)}</span>
                <small>{formatTime(event.event_at)}</small>
              </div>
              <strong>{eventTitle(event)}</strong>
              <p class="event-key">{event.source_key}</p>
              {#if event.failure_kind}
                <p class="event-kind">{event.failure_kind}</p>
              {/if}
              {#if event.domain}
                <small>{event.domain}</small>
              {/if}
              {#if event.canonical_url}
                <small>{event.canonical_url}</small>
              {/if}
            </button>
          {/each}
        </div>
      {:else}
        <p class="message muted">No recent successful source events yet.</p>
      {/if}
    </section>

    <section class="operation-column">
      <div class="operation-heading">
        <h3>Recent Failures</h3>
        <small>{activity.recent_failures?.length || 0} events</small>
      </div>

      {#if activity.recent_failures?.length}
        <div class="event-list">
          {#each activity.recent_failures as event}
            <button class="event-card event-failure" on:click={() => onSelect(event.source_key)} type="button">
              <div class="event-meta">
                <span class="event-badge failure">{eventLabel(event)}</span>
                <small>{formatTime(event.event_at)}</small>
              </div>
              <strong>{eventTitle(event)}</strong>
              <p class="event-key">{event.source_key}</p>
              {#if event.failure_kind}
                <p class="event-kind">{event.failure_kind}</p>
              {/if}
              {#if event.domain}
                <small>{event.domain}</small>
              {/if}
              {#if event.message}
                <p class="event-message">{event.message}</p>
              {/if}
              {#if event.canonical_url}
                <small>{event.canonical_url}</small>
              {/if}
            </button>
          {/each}
        </div>
      {:else}
        <p class="message muted">No recent source failures recorded.</p>
      {/if}
    </section>
  </div>
</section>
