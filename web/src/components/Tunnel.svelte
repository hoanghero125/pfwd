<script lang="ts">
  import { formatRate, pauseText } from "../lib/format";
  import { app } from "../lib/store.svelte";
  import type { Forward } from "../lib/types";

  // A forward as the TUI draws it: a tunnel from an open end (the port it
  // forwards) to a solid one (where the port arrives; a reverse forward
  // runs the other way), with its state on the line. A working tunnel is
  // solid, the others broken. While bytes flow, packets move along it,
  // faster when more flow.
  let { f }: { f: Forward } = $props();

  const rate = $derived(app.model.rates[f.id]);
  const total = $derived(rate ? rate.in + rate.out : 0);
  const busy = $derived(f.state === "active" && total > 0);
  const speed = $derived(total > 4 << 20 ? "0.45s" : total > 256 << 10 ? "0.7s" : "1.2s");
</script>

<span class="tunnel {f.state}" class:busy class:reverse={f.kind === "remote"} style:--speed={speed} title={f.state === "failed" ? f.error : undefined}>
  <span class="wire" aria-hidden="true"></span>
  <span class="visually-hidden">{f.state}, </span>
  <span class="say"
    >{#if f.state === "active"}{#if busy}<span class="v">↓ {formatRate(rate.in)}/s</span>{"  "}↑ {formatRate(rate.out)}/s{#if f.conns}{`  ${f.conns} ${f.conns === 1 ? "conn" : "conns"}`}{/if}{:else}idle{/if}{:else if f.state === "paused"}paused{#if f.pause}{`, ${pauseText(f.pause)}`}{/if}{:else if f.state === "failed"}failed{#if f.error}{`: ${f.error}`}{/if}{:else}starting…{/if}</span
  >
</span>
