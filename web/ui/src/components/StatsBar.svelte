<script>
  import { formatTime } from "../lib/time.js";

  export let backlog;
  export let activity;
</script>

<section class="stats-grid">
  <article class="stat-card">
    <span class="label">Pending Extracts</span>
    <strong>{backlog.source_extraction_pending}</strong>
    <small>sources still needing extraction</small>
  </article>
  <article class="stat-card">
    <span class="label">Pending Summaries</span>
    <strong>{backlog.source_summary_pending}</strong>
    <small>sources still needing summaries</small>
  </article>
  <article class="stat-card">
    <span class="label">Recent Source Writes</span>
    <strong>{activity.sources_updated_in_window}</strong>
    <small>{activity.window} window</small>
  </article>
  <article class="stat-card">
    <span class="label">Recent Summaries</span>
    <strong>{activity.sources_summarized_in_window}</strong>
    <small>latest: {formatTime(activity.latest_source_summary_at)}</small>
  </article>
</section>
