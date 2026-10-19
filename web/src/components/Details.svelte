<script lang="ts">
  import { clock, formatRate } from "../lib/format";
  import { glyph, themeState } from "../lib/glyph";
  import { app } from "../lib/store.svelte";
  import type { Forward } from "../lib/types";
  import RateChart from "./RateChart.svelte";
  import State from "./State.svelte";

  // A forward's details, opened under its row: its traffic over the last
  // five minutes, its counters and where it came from, the changes to its
  // state, and what can be done with it.
  let { f, onclose, onlabel }: { f: Forward; onclose: () => void; onlabel: () => void } = $props();

  const rate = $derived(app.model.rates[f.id]);
  const history = $derived(app.model.history[f.id] ?? []);
  const origin = $derived(f.config ? "config.toml" : f.auto ? "auto-forward" : "you");
</script>

<section class="expand" aria-label="Details of :{f.remote}">
  <div class="expand-chart">
    <div class="legend">
      <span><span class="key down" aria-hidden="true"></span>download <b>{formatRate(rate?.in ?? 0)}/s</b></span>
      <span><span class="key up" aria-hidden="true"></span>upload <b>{formatRate(rate?.out ?? 0)}/s</b></span>
    </div>
    <RateChart series={rate?.history ?? []} height={150} what="Traffic of :{f.remote}" />
  </div>
  <div class="expand-side">
    <dl class="facts">
      <dt>connections</dt><dd>{f.conns}</dd>
      <dt>received</dt><dd>{formatRate(f.bytes_in)}</dd>
      <dt>sent</dt><dd>{formatRate(f.bytes_out)}</dd>
      <dt>state</dt><dd><State state={f.state} word={f.state === "paused" && f.pause ? `paused, ${f.pause}` : f.state} /></dd>
      {#if f.error}<dt>error</dt><dd>{f.error}</dd>{/if}
      <dt>started by</dt><dd>{origin}{f.pinned ? ", pinned" : ""}</dd>
      {#if f.label}<dt>label</dt><dd>{f.label}</dd>{/if}
    </dl>
    <ul class="hist" aria-label="History of :{f.remote}">
      {#each history.slice(0, 5) as h, i (i)}
        <li>
          <span class="g-{themeState(h.text.split(/[: ]/)[0])}" aria-hidden="true">{glyph(h.text.split(/[: ]/)[0])}</span>
          <span class="when">{clock(h.at)}</span>
          <span>{h.text}</span>
        </li>
      {:else}
        <li class="muted">Changes show up here from now on.</li>
      {/each}
    </ul>
    <div class="btns">
      {#if !f.config}
        <button class="link go" onclick={() => app.pin(f, !f.pinned)}>{f.pinned ? "unpin" : "pin"}</button>
        <button class="link" onclick={onlabel}>label…</button>
      {/if}
      <button class="link danger" onclick={() => app.stop(f)}>stop</button>
      <span class="grow"></span>
      <button class="link" aria-label="Close the details" onclick={onclose}>close <kbd>esc</kbd></button>
    </div>
  </div>
</section>
