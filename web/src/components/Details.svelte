<script lang="ts">
  import { clock, formatRate, localAddr, remoteAddr } from "../lib/format";
  import { app } from "../lib/store.svelte";
  import type { Forward } from "../lib/types";
  import State from "./State.svelte";

  // The forward details drawer: mapping, state history, connections,
  // bytes and a five-minute rate sparkline.
  let { f, onclose }: { f: Forward; onclose: () => void } = $props();
  let closeButton: HTMLButtonElement;

  const rate = $derived(app.model.rates[f.id]);
  const history = $derived(app.model.history[f.id] ?? []);
  const points = $derived.by(() => {
    const h = rate?.history ?? [];
    if (h.length < 2) return "";
    const peak = Math.max(...h, 1);
    return h.map((v, i) => `${(i / (h.length - 1)) * 300},${62 - (v / peak) * 58}`).join(" ");
  });
  const peak = $derived(Math.max(0, ...(rate?.history ?? [0])));
  const origin = $derived([f.config ? "config.toml" : f.auto ? "auto-forward" : "manual", f.pinned ? "pinned" : ""].filter(Boolean).join(", "));

  $effect(() => closeButton.focus());

  function onkeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onclose();
  }
</script>

<svelte:window {onkeydown} />

<aside class="drawer" aria-label="Forward details">
  <div class="head">
    <h2>{f.kind === "dynamic" ? "SOCKS proxy" : `:${f.remote}`} on {f.host}</h2>
    <div class="tools"><button class="btn small" aria-label="Close details" bind:this={closeButton} onclick={onclose}>✕</button></div>
  </div>
  <span class="patch">
    <span class="jack">{f.host}{remoteAddr(f)}</span>
    <span class="cable g-{f.state} {f.state}" aria-hidden="true"></span>
    <span class="jack">{localAddr(f)}</span>
  </span>
  <dl>
    <dt>State</dt>
    <dd><State state={f.state} />{#if f.state === "paused" && f.pause}: {f.pause}{/if}</dd>
    {#if f.error}<dt>Error</dt><dd>{f.error}</dd>{/if}
    <dt>Connections</dt>
    <dd>{f.conns} open</dd>
    <dt>Received</dt>
    <dd class="mono">{formatRate(f.bytes_in)}</dd>
    <dt>Sent</dt>
    <dd class="mono">{formatRate(f.bytes_out)}</dd>
    <dt>Origin</dt>
    <dd>{origin}</dd>
    {#if f.label}<dt>Label</dt><dd>{f.label}</dd>{/if}
  </dl>
  <div>
    <div class="small-title">Traffic, last 5 minutes</div>
    <svg class="spark" viewBox="0 0 300 64" preserveAspectRatio="none" role="img" aria-label="Traffic rate; peak {formatRate(peak)}/s">
      {#if points}<polyline {points} />{/if}
    </svg>
  </div>
  <div>
    <div class="small-title">History</div>
    <ul class="hist">
      {#each history as h, i (i)}
        <li><span class="mono muted">{clock(h.at)}</span> {h.text}</li>
      {:else}
        <li class="muted">Changes show up here from now on.</li>
      {/each}
    </ul>
  </div>
  <div class="btns">
    {#if !f.config}<button class="btn" onclick={() => app.pin(f, !f.pinned)}>{f.pinned ? "Unpin" : "Pin"}</button>{/if}
    <button class="btn danger" onclick={() => (app.stop(f), onclose())}>Stop</button>
  </div>
</aside>
