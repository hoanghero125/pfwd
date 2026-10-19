<script lang="ts">
  import { formatRate } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app, viewHash } from "../lib/store.svelte";
  import ForwardDialog from "./ForwardDialog.svelte";
  import PortsTable from "./PortsTable.svelte";
  import Reverse from "./Reverse.svelte";
  import Socks from "./Socks.svelte";
  import State from "./State.svelte";

  let { host, tab }: { host: string; tab: "ports" | "reverse" | "socks" } = $props();
  let adding = $state(false);
  let filter = $state("");

  const session = $derived(app.model.sessions[host]);
  const info = $derived(app.model.ports[host]);
  const auto = $derived(app.model.auto[host] ?? false);
  const retryIn = $derived(session?.retry_at ? Math.max(0, Math.ceil((Date.parse(session.retry_at) - app.now) / 1000)) : 0);
  const forwards = $derived(forwardsOf(app.model, host));
  const conns = $derived(forwards.reduce((n, f) => n + (f.conns || 0), 0));
  const rate = $derived(
    forwards.reduce(
      (t, f) => {
        const r = app.model.rates[f.id];
        return r ? { in: t.in + r.in, out: t.out + r.out } : t;
      },
      { in: 0, out: 0 },
    ),
  );

  function onkeydown(e: KeyboardEvent) {
    const t = e.target as HTMLElement | null;
    if (e.ctrlKey || e.metaKey || e.altKey || t?.closest("input, textarea, select, dialog")) return;
    if (e.key === "a" && session) {
      void app.setAuto(host, !auto);
      e.preventDefault();
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="head">
  <h1 class="host">{host}</h1>
  <div class="tools">
    {#if session && session.state !== "closed" && session.state !== "failed"}
      <button class="link" role="switch" aria-checked={auto} aria-label="Auto-forward" onclick={() => app.setAuto(host, !auto)}>auto-forward <b class:muted={!auto}>{auto ? "on" : "off"}</b></button>
      <button class="link go" onclick={() => (adding = true)}>+ new forward</button>
      <button class="link danger" onclick={() => app.disconnect(host)}>disconnect</button>
    {:else}
      <button class="btn primary" onclick={() => (session ? app.reconnect(host) : app.connectHost(host))}>connect</button>
    {/if}
  </div>
</div>

<div class="meta">
  {#if session}
    <State state={session.state} word={session.state === "reconnecting" && retryIn ? `reconnecting in ${retryIn} s` : session.state} />
  {:else}
    <span>not connected</span>
  {/if}
  {#if session?.state === "connected"}
    {#if info?.os}<span>{info.os.toLowerCase()}</span>{/if}
    {#if info?.strategy && info.strategy !== "none"}<span>ports from <span class="v">{info.strategy}</span></span>{/if}
    <span><span class="v">{forwards.length}</span> {forwards.length === 1 ? "forward" : "forwards"}</span>
    {#if conns}<span><span class="v">{conns}</span> open {conns === 1 ? "connection" : "connections"}</span>{/if}
    <span>↓ <span class="v">{formatRate(rate.in)}/s</span>&nbsp;&nbsp;↑ <span class="v">{formatRate(rate.out)}/s</span></span>
  {/if}
</div>

{#if session?.state === "failed" && session.error}
  <div class="notice" role="alert">
    <span class="mark" aria-hidden="true">✗</span>
    <span class="grow">{session.error} <span class="muted"><code>pfwd doctor {host}</code> checks each step.</span></span>
    <button class="link go" onclick={() => app.reconnect(host)}>try again</button>
  </div>
{:else if session?.state === "reconnecting"}
  <div class="notice warn">
    <span class="mark" aria-hidden="true">◌</span>
    <span class="grow">{session.error || "The connection was lost."} Forwards stay paused until it is back.</span>
    <button class="link go" onclick={() => app.reconnect(host)}>retry now</button>
  </div>
{/if}

<div class="tabsbar">
  <nav class="tabs" aria-label="Forward types">
    <a href={viewHash({ kind: "host", host, tab: "ports" })} aria-current={tab === "ports" ? "page" : undefined}>ports</a>
    <a href={viewHash({ kind: "host", host, tab: "reverse" })} aria-current={tab === "reverse" ? "page" : undefined}>reverse</a>
    <a href={viewHash({ kind: "host", host, tab: "socks" })} aria-current={tab === "socks" ? "page" : undefined}>socks</a>
  </nav>
  <span class="grow"></span>
  {#if tab === "ports"}
    <label class="filter">
      <kbd aria-hidden="true">/</kbd>
      <input type="search" placeholder="filter by port, process or label" aria-label="Filter ports" data-filter bind:value={filter} />
    </label>
  {/if}
</div>

{#if tab === "reverse"}
  <Reverse {host} />
{:else if tab === "socks"}
  <Socks {host} />
{:else}
  <PortsTable {host} {filter} />
{/if}

{#if adding}
  <ForwardDialog {host} onclose={() => (adding = false)} />
{/if}
