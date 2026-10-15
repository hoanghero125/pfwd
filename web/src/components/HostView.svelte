<script lang="ts">
  import { formatRate } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app, viewHash } from "../lib/store.svelte";
  import ForwardDialog from "./ForwardDialog.svelte";
  import Icon from "./Icon.svelte";
  import PortsTable from "./PortsTable.svelte";
  import Reverse from "./Reverse.svelte";
  import Socks from "./Socks.svelte";
  import State from "./State.svelte";

  let { host, tab }: { host: string; tab: "ports" | "reverse" | "socks" } = $props();
  let adding = $state(false);

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
  {#if session}
    <State state={session.state} chip word={session.state === "reconnecting" && retryIn ? `reconnecting in ${retryIn} s` : session.state} />
  {:else}
    <span class="chip g-closed">not connected</span>
  {/if}
  <div class="tools">
    {#if session && session.state !== "closed" && session.state !== "failed"}
      <button class="switch" role="switch" aria-checked={auto} onclick={() => app.setAuto(host, !auto)}>
        <span class="track" aria-hidden="true"></span> Auto-forward
      </button>
      <button class="btn ghost" onclick={() => app.disconnect(host)}><Icon name="disconnect" /> Disconnect</button>
      <button class="btn primary" onclick={() => (adding = true)}><Icon name="plus" /> New forward</button>
    {:else}
      <button class="btn primary" onclick={() => (session ? app.reconnect(host) : app.connectHost(host))}><Icon name="retry" /> Connect</button>
    {/if}
  </div>
</div>

{#if session?.state === "connected"}
  <div class="meta">
    {#if info?.os}<span><Icon name="terminal" size={14} /> {info.os}{#if info.strategy && info.strategy !== "none"}, ports from <span class="mono">{info.strategy}</span>{/if}</span>{/if}
    <span><span class="mono">{forwards.length}</span> {forwards.length === 1 ? "forward" : "forwards"}</span>
    {#if conns}<span><span class="mono">{conns}</span> open {conns === 1 ? "connection" : "connections"}</span>{/if}
    <span><Icon name="down" size={12} /><span class="mono">{formatRate(rate.in)}/s</span> <Icon name="up" size={12} /><span class="mono">{formatRate(rate.out)}/s</span></span>
  </div>
{/if}

{#if session?.state === "failed" && session.error}
  <div class="notice" role="alert">
    <Icon name="error" size={18} />
    <span class="grow">{session.error} <span class="muted"><code>pfwd doctor {host}</code> checks each step.</span></span>
    <button class="btn small" onclick={() => app.reconnect(host)}><Icon name="retry" size={14} /> Try again</button>
  </div>
{:else if session?.state === "reconnecting"}
  <div class="notice warn">
    <Icon name="warn" size={18} />
    <span class="grow">{session.error || "The connection was lost."} Forwards stay paused until it is back.</span>
    <button class="btn small" onclick={() => app.reconnect(host)}><Icon name="retry" size={14} /> Retry now</button>
  </div>
{/if}

<nav class="tabs" aria-label="Forward types">
  <a href={viewHash({ kind: "host", host, tab: "ports" })} aria-current={tab === "ports" ? "page" : undefined}>Ports</a>
  <a href={viewHash({ kind: "host", host, tab: "reverse" })} aria-current={tab === "reverse" ? "page" : undefined}>Reverse</a>
  <a href={viewHash({ kind: "host", host, tab: "socks" })} aria-current={tab === "socks" ? "page" : undefined}>SOCKS</a>
</nav>

{#if tab === "reverse"}
  <Reverse {host} />
{:else if tab === "socks"}
  <Socks {host} />
{:else}
  <PortsTable {host} />
{/if}

{#if adding}
  <ForwardDialog {host} onclose={() => (adding = false)} />
{/if}
