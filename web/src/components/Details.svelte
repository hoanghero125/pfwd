<script lang="ts">
  import { clock, formatRate, localAddr, remoteAddr } from "../lib/format";
  import { glyph, themeState } from "../lib/glyph";
  import { app } from "../lib/store.svelte";
  import type { Forward } from "../lib/types";
  import Dialog from "./Dialog.svelte";
  import State from "./State.svelte";
  import TrafficChart from "./TrafficChart.svelte";
  import Tunnel from "./Tunnel.svelte";

  // The forward details drawer: mapping, state history, connections,
  // bytes and a five-minute rate chart. It is a modal dialog, so the page
  // under it can't take focus.
  let { f, onclose, returnTo = "" }: { f: Forward; onclose: () => void; returnTo?: string } = $props();

  const rate = $derived(app.model.rates[f.id]);
  const history = $derived(app.model.history[f.id] ?? []);
  const origin = $derived([f.config ? "config.toml" : f.auto ? "auto-forward" : "manual", f.pinned ? "pinned" : ""].filter(Boolean).join(", "));
</script>

<Dialog title="{f.kind === 'dynamic' ? 'SOCKS proxy' : `:${f.remote}`} on {f.host}" {onclose} {returnTo} drawer>
  <div class="route">
    <Tunnel {f} />
    <div class="route-ends">
      <span>{remoteAddr(f).startsWith(":") ? `${f.host}${remoteAddr(f)}` : `${remoteAddr(f)} via ${f.host}`}</span>
      <span>{localAddr(f)}</span>
    </div>
  </div>
  <dl class="stats">
    <div><dt>connections</dt><dd>{f.conns}</dd></div>
    <div><dt>received</dt><dd>{formatRate(f.bytes_in)}</dd></div>
    <div><dt>sent</dt><dd>{formatRate(f.bytes_out)}</dd></div>
  </dl>
  <div>
    <div class="small-title">
      <span>traffic, last 5 minutes</span>
      {#if rate}<span>↓ <span class="v">{formatRate(rate.in)}/s</span>&nbsp;&nbsp;↑ {formatRate(rate.out)}/s</span>{/if}
    </div>
    <TrafficChart values={rate?.history ?? []} />
  </div>
  <dl class="facts">
    <dt>State</dt>
    <dd><State state={f.state} />{#if f.state === "paused" && f.pause}<span class="muted">, {f.pause}</span>{/if}</dd>
    {#if f.error}<dt>Error</dt><dd>{f.error}</dd>{/if}
    <dt>Origin</dt>
    <dd>{origin}</dd>
    {#if f.label}<dt>Label</dt><dd>{f.label}</dd>{/if}
  </dl>
  <div>
    <div class="small-title"><span>history</span></div>
    <ul class="hist">
      {#each history as h, i (i)}
        <li>
          <span class="g-{themeState(h.text.split(/[: ]/)[0])}" aria-hidden="true">{glyph(h.text.split(/[: ]/)[0])}</span>
          <span class="when">{clock(h.at)}</span>
          <span>{h.text}</span>
        </li>
      {:else}
        <li class="muted">Changes show up here from now on.</li>
      {/each}
    </ul>
  </div>
  <div class="btns">
    {#if !f.config}<button class="link go" onclick={() => app.pin(f, !f.pinned)}>{f.pinned ? "unpin" : "pin"}</button>{/if}
    <button class="link danger" onclick={() => (app.stop(f), onclose())}>stop</button>
  </div>
</Dialog>
