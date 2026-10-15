<script lang="ts">
  import { formatRate } from "../lib/format";

  // A forward's traffic over the last minutes, one point a second, as an
  // area under a line. Hovering shows the rate at a moment. The SVG is
  // drawn at its real width, so the hover label isn't stretched.
  let { values }: { values: number[] } = $props();
  let w = $state(400);
  const h = 110;
  const base = h - 4;
  const top = 22; // room for the hover label
  let hover = $state(-1);

  const n = $derived(values.length);
  const peak = $derived(Math.max(1, ...values));
  const x = (i: number) => (n < 2 ? 0 : (i / (n - 1)) * w);
  const y = (v: number) => base - (v / peak) * (base - top);
  const line = $derived(values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" "));
  const label = $derived(n ? `Traffic rate over the last ${n} seconds: peak ${formatRate(peak)}/s, now ${formatRate(values[n - 1] ?? 0)}/s` : "Traffic rate: nothing measured yet");

  function move(e: PointerEvent) {
    if (n < 2) return;
    const r = (e.currentTarget as SVGElement).getBoundingClientRect();
    hover = Math.max(0, Math.min(n - 1, Math.round(((e.clientX - r.left) / r.width) * (n - 1))));
  }

  function tip(i: number): { x: number; text: string; width: number } {
    const ago = n - 1 - i;
    const text = `${formatRate(values[i])}/s, ${ago ? `${ago} s ago` : "now"}`;
    const width = text.length * 7 + 14;
    return { x: Math.max(0, Math.min(w - width, x(i) - width / 2)), text, width };
  }
</script>

<div class="chart" bind:clientWidth={w}>
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions (hover only adds a label; the numbers are in the stats above) -->
  <svg viewBox="0 0 {w} {h}" width={w} height={h} role="img" aria-label={label} onpointermove={move} onpointerleave={() => (hover = -1)}>
    <line class="grid" x1="0" y1={base} x2={w} y2={base} />
    {#if n >= 2}
      <polygon class="spark-area" points="0,{base} {line} {w},{base}" />
      <polyline class="spark-line" points={line} />
      {#if hover >= 0}
        {@const t = tip(hover)}
        <line class="cross" x1={x(hover)} y1={top - 4} x2={x(hover)} y2={base} />
        <circle class="dot" cx={x(hover)} cy={y(values[hover])} r="4" />
        <rect class="tip-bg" x={t.x} y="0" width={t.width} height="18" rx="5" />
        <text class="tip-text" x={t.x + t.width / 2} y="12.5" text-anchor="middle">{t.text}</text>
      {/if}
    {:else}
      <text class="chart-empty" x={w / 2} y={h / 2} text-anchor="middle">Traffic shows up here as it flows.</text>
    {/if}
  </svg>
</div>
