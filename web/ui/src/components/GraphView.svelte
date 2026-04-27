<script>
  import { onMount, onDestroy } from "svelte";

  export let nodes = [];
  export let edges = [];
  export let selectedId = "";
  export let onSelect = () => {};

  const TYPE_COLORS = {
    x_bookmark: "#60a5fa",
    x_quote: "#60a5fa",
    github_star: "#fb923c",
    github: "#fb923c",
    youtube_watch_later: "#f87171",
    youtube_liked: "#f87171",
    youtube: "#f87171",
    web: "#34d399",
  };

  function nodeColor(type) {
    return TYPE_COLORS[type] || "#a78bfa";
  }

  let svgEl;
  let containerEl;
  let w = 800;
  let h = 600;
  let simNodes = [];
  let simEdges = [];
  let rafId = null;
  let alpha = 1;
  let mounted = false;

  function buildSim(inputNodes, inputEdges) {
    if (rafId) cancelAnimationFrame(rafId);

    const cx = w / 2;
    const cy = h / 2;
    const n = inputNodes.length;

    const sn = inputNodes.map((node, i) => {
      const existing = simNodes.find((s) => s.id === node.id);
      if (existing) {
        return { ...node, x: existing.x, y: existing.y, vx: existing.vx * 0.5, vy: existing.vy * 0.5, fx: null, fy: null };
      }
      // Place new nodes in a circle around center, or near their connected neighbor
      const angle = (i / Math.max(n, 1)) * 2 * Math.PI - Math.PI / 2;
      const radius = node.is_secondary ? Math.min(w, h) * 0.38 : Math.min(w, h) * 0.27;
      return {
        ...node,
        x: cx + radius * Math.cos(angle) + (Math.random() - 0.5) * 30,
        y: cy + radius * Math.sin(angle) + (Math.random() - 0.5) * 30,
        vx: 0,
        vy: 0,
        fx: null,
        fy: null,
      };
    });

    const nodeById = new Map(sn.map((n) => [n.id, n]));

    const se = inputEdges
      .map((e) => ({ ...e, sourceNode: nodeById.get(e.source), targetNode: nodeById.get(e.target) }))
      .filter((e) => e.sourceNode && e.targetNode);

    simNodes = sn;
    simEdges = se;
    alpha = 0.8;
    scheduleAnim();
  }

  function simStep() {
    if (alpha < 0.001 || simNodes.length === 0) return;

    const REPULSION = 2800;
    const SPRING_K = 0.055;
    const REST_LEN = 130;
    const CENTER_K = 0.012;
    const DAMP = 0.86;
    const cx = w / 2;
    const cy = h / 2;
    const margin = 55;

    for (let i = 0; i < simNodes.length; i++) {
      const ni = simNodes[i];
      if (ni.fx != null) {
        ni.x = ni.fx;
        ni.vx = 0;
      }
      if (ni.fy != null) {
        ni.y = ni.fy;
        ni.vy = 0;
      }
      if (ni.fx != null && ni.fy != null) continue;

      ni.vx += (cx - ni.x) * CENTER_K * alpha;
      ni.vy += (cy - ni.y) * CENTER_K * alpha;

      for (let j = 0; j < simNodes.length; j++) {
        if (i === j) continue;
        const nj = simNodes[j];
        const dx = ni.x - nj.x;
        const dy = ni.y - nj.y;
        const d2 = Math.max(dx * dx + dy * dy, 1);
        const d = Math.sqrt(d2);
        const f = (REPULSION * alpha) / d2;
        ni.vx += (dx / d) * f;
        ni.vy += (dy / d) * f;
      }
    }

    for (const e of simEdges) {
      const s = e.sourceNode;
      const t = e.targetNode;
      const dx = t.x - s.x;
      const dy = t.y - s.y;
      const d = Math.sqrt(dx * dx + dy * dy) || 0.01;
      const f = SPRING_K * (d - REST_LEN);
      const fx = (dx / d) * f;
      const fy = (dy / d) * f;
      if (s.fx == null) {
        s.vx += fx;
        s.vy += fy;
      }
      if (t.fx == null) {
        t.vx -= fx;
        t.vy -= fy;
      }
    }

    for (const n of simNodes) {
      if (n.fx != null && n.fy != null) continue;
      n.vx *= DAMP;
      n.vy *= DAMP;
      n.x += n.vx;
      n.y += n.vy;
      n.x = Math.max(margin, Math.min(w - margin, n.x));
      n.y = Math.max(margin, Math.min(h - margin, n.y));
    }

    alpha *= 0.97;
    simNodes = [...simNodes];
  }

  function scheduleAnim() {
    if (rafId) cancelAnimationFrame(rafId);
    function frame() {
      simStep();
      if (alpha > 0.001) {
        rafId = requestAnimationFrame(frame);
      }
    }
    rafId = requestAnimationFrame(frame);
  }

  $: if (mounted) buildSim(nodes, edges);

  let draggingNode = null;
  let dragMoved = false;
  let tooltip = null;

  function handleMouseDown(e, node) {
    e.stopPropagation();
    draggingNode = node;
    dragMoved = false;
    node.fx = node.x;
    node.fy = node.y;
  }

  function handleSvgMouseMove(e) {
    if (!draggingNode || !svgEl) return;
    dragMoved = true;
    const rect = svgEl.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    draggingNode.fx = x;
    draggingNode.fy = y;
    draggingNode.x = x;
    draggingNode.y = y;
    simNodes = [...simNodes];
    alpha = Math.max(alpha, 0.12);
    scheduleAnim();
  }

  function handleMouseUp(e, node) {
    if (draggingNode) {
      if (!dragMoved) {
        onSelect(node.id);
      }
      draggingNode.fx = null;
      draggingNode.fy = null;
      draggingNode = null;
      dragMoved = false;
    }
  }

  function handleGlobalUp() {
    if (draggingNode) {
      draggingNode.fx = null;
      draggingNode.fy = null;
      draggingNode = null;
      dragMoved = false;
    }
  }

  function truncate(text, max) {
    if (!text) return "";
    return text.length > max ? text.slice(0, max) + "…" : text;
  }

  let resizeObs;

  onMount(() => {
    function measure() {
      if (!containerEl) return;
      const r = containerEl.getBoundingClientRect();
      if (r.width > 10 && r.height > 10) {
        w = r.width;
        h = r.height;
        mounted = true;
        buildSim(nodes, edges);
      }
    }
    resizeObs = new ResizeObserver(measure);
    resizeObs.observe(containerEl);
    measure();
  });

  onDestroy(() => {
    if (resizeObs) resizeObs.disconnect();
    if (rafId) cancelAnimationFrame(rafId);
  });
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  class="graph-wrap"
  bind:this={containerEl}
  on:mousemove={handleSvgMouseMove}
  on:mouseup={handleGlobalUp}
  on:mouseleave={handleGlobalUp}
  role="application"
  aria-label="Knowledge graph"
>
  <svg
    bind:this={svgEl}
    width={w}
    height={h}
    style="width:{w}px;height:{h}px"
  >
    <defs>
      <filter id="glow-sm" x="-60%" y="-60%" width="220%" height="220%">
        <feGaussianBlur stdDeviation="2.5" result="b" />
        <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
      </filter>
      <filter id="glow-lg" x="-100%" y="-100%" width="300%" height="300%">
        <feGaussianBlur stdDeviation="6" result="b" />
        <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
      </filter>
      <radialGradient id="bg-grad" cx="50%" cy="50%" r="50%">
        <stop offset="0%" stop-color="rgba(74,222,128,0.025)" />
        <stop offset="100%" stop-color="transparent" />
      </radialGradient>
    </defs>

    <!-- Subtle background -->
    <rect width={w} height={h} fill="url(#bg-grad)" />

    <!-- Edges -->
    {#each simEdges as e (e.source + "-" + e.target)}
      <line
        x1={e.sourceNode.x}
        y1={e.sourceNode.y}
        x2={e.targetNode.x}
        y2={e.targetNode.y}
        stroke={e.kind === "linked_source" ? "rgba(74,222,128,0.2)" : "rgba(96,165,250,0.18)"}
        stroke-width="1.5"
        stroke-dasharray={e.kind === "backlink" ? "5 4" : ""}
      />
    {/each}

    <!-- Nodes -->
    {#each simNodes as node (node.id)}
      {@const color = nodeColor(node.source_type)}
      {@const selected = selectedId === node.id}
      {@const r = node.is_secondary ? 7 : selected ? 14 : 10}
      <g
        class="gnode"
        transform={`translate(${node.x},${node.y})`}
        on:mousedown={(e) => handleMouseDown(e, node)}
        on:mouseup={(e) => handleMouseUp(e, node)}
        on:mouseenter={() => (tooltip = node.title || node.id)}
        on:mouseleave={() => (tooltip = null)}
        role="button"
        tabindex="0"
        aria-label={node.title || node.id}
        on:keydown={(e) => e.key === "Enter" && onSelect(node.id)}
      >
        {#if selected}
          <circle r={r + 9} fill={color} opacity="0.08" />
          <circle r={r + 5} fill={color} opacity="0.12" />
        {/if}
        <circle
          {r}
          fill={color}
          filter={selected ? "url(#glow-lg)" : "url(#glow-sm)"}
          stroke={selected ? "rgba(255,255,255,0.7)" : "rgba(0,0,0,0.4)"}
          stroke-width={selected ? 2 : 1}
          opacity={node.is_secondary ? 0.7 : 0.92}
        />
        {#if !node.is_secondary || selected}
          <text
            dy={r + 15}
            text-anchor="middle"
            fill={selected ? "rgba(220,245,230,0.95)" : "rgba(143,184,158,0.75)"}
            font-size={node.is_secondary ? "10" : "11"}
            style="pointer-events:none;user-select:none;font-family:'IBM Plex Sans',system-ui,sans-serif"
          >
            {truncate(node.title || node.id, selected ? 28 : 22)}
          </text>
        {/if}
      </g>
    {/each}

    {#if simNodes.length === 0}
      <text
        x={w / 2}
        y={h / 2}
        text-anchor="middle"
        fill="rgba(74,120,90,0.4)"
        font-size="14"
        style="font-family:'IBM Plex Sans',system-ui,sans-serif"
      >
        search to populate the graph
      </text>
    {/if}
  </svg>

  {#if tooltip}
    <div class="graph-tooltip">{tooltip}</div>
  {/if}
</div>

<style>
  .graph-wrap {
    width: 100%;
    height: 100%;
    position: relative;
    overflow: hidden;
  }

  svg {
    display: block;
    cursor: default;
  }

  .gnode {
    cursor: pointer;
  }

  .graph-tooltip {
    position: absolute;
    bottom: 0.75rem;
    left: 50%;
    transform: translateX(-50%);
    background: rgba(12, 24, 18, 0.96);
    border: 1px solid rgba(74, 222, 128, 0.2);
    color: rgba(200, 230, 210, 0.9);
    padding: 0.35rem 0.7rem;
    border-radius: 0.45rem;
    font-size: 0.82rem;
    pointer-events: none;
    white-space: nowrap;
    max-width: 36ch;
    overflow: hidden;
    text-overflow: ellipsis;
    font-family: "IBM Plex Sans", system-ui, sans-serif;
  }
</style>
