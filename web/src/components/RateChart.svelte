<script lang="ts">
  import { formatRate } from "../lib/format";
  import type { Sample } from "../lib/reduce";

  // Traffic over the last minutes, a sample a second, newest at the right:
  // download as an area, upload as a line, on one rate axis. The span
  // grows with what has been measured, from a minute up to max seconds.
  // Hovering shows both rates at that second. The SVG is drawn at its real
  // width, so the lines and labels aren't stretched.
  let { series, height = 160, max = 300, what = "Traffic" }: { series: Sample[]; height?: number; max?: number; what?: string } = $props();
  const window = $derived(Math.min(max, Math.max(60, series.length)));

  let w = $state(600);
  const pad = { top: 12, bottom: 22 };
  const base = $derived(height - pad.bottom);
  let hover = $state(-1);

  const shown = $derived(series.slice(-window));
  const n = $derived(shown.length);
  const peak = $derived(Math.max(0, ...shown.map((s) => Math.max(s.in, s.out))));
  // The axis tops out at a round number: 1, 2 or 5 times a power of 1024
  // or of ten under it, so the grid labels read cleanly.
  const top = $derived.by(() => {
    if (peak <= 0) return 1024;
    let unit = 1;
    while (unit * 1024 <= peak) unit *= 1024;
    for (const step of [1, 2, 5, 10, 20, 50, 100, 200, 500, 1024]) if (step * unit >= peak) return step * unit;
    return 1024 * unit;
  });
  const x = (i: number) => (window < 2 ? w : (w * (window - n + i)) / (window - 1));
  const y = (v: number) => base - (v / top) * (base - pad.top);
  const path = (pick: (s: Sample) => number) => shown.map((s, i) => `${x(i).toFixed(1)},${y(pick(s)).toFixed(1)}`).join(" ");
  const down = $derived(path((s) => s.in));
  const up = $derived(path((s) => s.out));
  const now = $derived(shown[n - 1] ?? { in: 0, out: 0 });
  const label = $derived(
    n
      ? `${what} rate over the last ${n} seconds: download now ${formatRate(now.in)}/s, peak ${formatRate(Math.max(...shown.map((s) => s.in)))}/s; upload now ${formatRate(now.out)}/s`
      : `${what} rate: nothing measured yet`,
  );
  const since = $derived(window < 120 ? `${window} s ago` : `${Math.round(window / 60)} min ago`);

  function move(e: PointerEvent) {
    if (n < 2) return;
    const r = (e.currentTarget as SVGElement).getBoundingClientRect();
    const slot = Math.round(((e.clientX - r.left) / r.width) * (window - 1));
    const i = slot - (window - n);
    hover = i >= 0 && i < n ? i : -1;
  }
</script>

<div class="chart" bind:clientWidth={w}>
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions (hover only adds a label; the numbers are in the figures beside it) -->
  <svg viewBox="0 0 {w} {height}" width={w} {height} role="img" aria-label={label} onpointermove={move} onpointerleave={() => (hover = -1)}>
    <line class="grid" x1="0" y1={y(top)} x2={w} y2={y(top)} />
    <line class="grid" x1="0" y1={y(top / 2)} x2={w} y2={y(top / 2)} />
    <line class="axis" x1="0" y1={base} x2={w} y2={base} />
    <text class="tick" x="0" y={y(top) - 3}>{formatRate(top)}/s</text>
    <text class="tick" x="0" y={y(top / 2) - 3}>{formatRate(top / 2)}/s</text>
    <text class="tick" x="0" y={height - 5}>{since}</text>
    <text class="tick" x={w} y={height - 5} text-anchor="end">now</text>
    {#if n >= 2}
      <polygon class="down-area" points="{x(0).toFixed(1)},{base} {down} {x(n - 1).toFixed(1)},{base}" />
      <polyline class="down-line" points={down} />
      <polyline class="up-line" points={up} />
      {#if hover >= 0}
        <line class="cross" x1={x(hover)} y1={pad.top} x2={x(hover)} y2={base} />
        <circle class="dot down" cx={x(hover)} cy={y(shown[hover].in)} r="4" />
        <circle class="dot up" cx={x(hover)} cy={y(shown[hover].out)} r="3.5" />
      {/if}
    {:else}
      <text class="chart-empty" x={w / 2} y={(base + pad.top) / 2} text-anchor="middle">Traffic shows up here as it flows.</text>
    {/if}
  </svg>
  {#if hover >= 0}
    {@const ago = n - 1 - hover}
    <div class="tip" class:left={x(hover) > w - 180} style:left="{x(hover)}px">
      <span class="muted">{ago ? `${ago} s ago` : "now"}</span>
      <span><span class="key down" aria-hidden="true"></span>↓ {formatRate(shown[hover].in)}/s</span>
      <span><span class="key up" aria-hidden="true"></span>↑ {formatRate(shown[hover].out)}/s</span>
    </div>
  {/if}
</div>
