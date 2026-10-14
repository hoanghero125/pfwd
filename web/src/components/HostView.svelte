<script lang="ts">
  import { app, viewHash } from "../lib/store.svelte";
  import ForwardDialog from "./ForwardDialog.svelte";
  import PortsTable from "./PortsTable.svelte";
  import Reverse from "./Reverse.svelte";
  import Socks from "./Socks.svelte";
  import State from "./State.svelte";

  let { host, tab }: { host: string; tab: "ports" | "reverse" | "socks" } = $props();
  let adding = $state(false);

  const session = $derived(app.model.sessions[host]);
  const auto = $derived(app.model.auto[host] ?? false);
  const retryIn = $derived(session?.retry_at ? Math.max(0, Math.ceil((Date.parse(session.retry_at) - app.now) / 1000)) : 0);

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
  <h1>{host}</h1>
  {#if session}
    <span class="state">
      <State state={session.state} />{#if session.state === "reconnecting" && retryIn}&nbsp;(retry in {retryIn} s){/if}
    </span>
  {:else}
    <span class="state">not connected</span>
  {/if}
  <div class="tools">
    {#if session && session.state !== "closed" && session.state !== "failed"}
      <button class="switch" role="switch" aria-checked={auto} onclick={() => app.setAuto(host, !auto)}>
        <span class="track" aria-hidden="true"></span> Auto-forward
      </button>
      <button class="btn" onclick={() => (adding = true)}>New forward</button>
      <button class="btn" onclick={() => app.disconnect(host)}>Disconnect</button>
    {:else}
      <button class="btn primary" onclick={() => (session ? app.reconnect(host) : app.connectHost(host))}>Connect</button>
    {/if}
  </div>
</div>

{#if session?.state === "failed" && session.error}
  <div class="notice" role="alert">
    <span>{session.error}</span>
    <span class="muted">pfwd doctor {host} checks each step.</span>
    <button class="btn small" onclick={() => app.reconnect(host)}>Try again</button>
  </div>
{:else if session?.state === "reconnecting"}
  <div class="notice warn">
    <span>{session.error || "The connection was lost."} Forwards stay paused until it is back.</span>
    <button class="btn small" onclick={() => app.reconnect(host)}>Retry now</button>
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
