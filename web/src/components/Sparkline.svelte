<script lang="ts">
  // A rate over time, as a thin line on a faint area: the last values of a
  // forward's traffic, one a second. Decorative next to its numbers; the
  // label says what it shows.
  let { values, width = 72, height = 22, label = "" }: { values: number[]; width?: number; height?: number; label?: string } = $props();

  const points = $derived.by(() => {
    const v = values.slice(-Math.max(2, Math.floor(width / 2)));
    if (v.length < 2) return "";
    const peak = Math.max(...v, 1);
    const step = width / (v.length - 1);
    return v.map((x, i) => `${(i * step).toFixed(1)},${(height - 1 - (x / peak) * (height - 3)).toFixed(1)}`).join(" ");
  });
  const idle = $derived(!values.some((v) => v > 0));
</script>

<svg class="spark" class:idle viewBox="0 0 {width} {height}" width={width} height={height} role={label ? "img" : undefined} aria-label={label || undefined} aria-hidden={label ? undefined : "true"}>
  {#if points}
    <polygon class="spark-area" points="0,{height} {points} {width},{height}" />
    <polyline class="spark-line" points={points} />
  {:else}
    <line class="spark-base" x1="0" y1={height - 1} x2={width} y2={height - 1} />
  {/if}
</svg>
