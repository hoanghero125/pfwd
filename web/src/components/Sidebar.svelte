<script lang="ts">
  import { forwardsOf } from "../lib/reduce";
  import { app, viewHash } from "../lib/store.svelte";
  import ConnectDialog from "./ConnectDialog.svelte";
  import State from "./State.svelte";

  let { onhelp }: { onhelp: () => void } = $props();
  let open = $state(false);
  let connecting = $state(false);

  // The daemon's hosts, in the order they were connected.
  const hosts = $derived(Object.keys(app.model.sessions));
  const current = $derived(app.view.kind === "host" ? app.view.host : "");
</script>

<nav class="side" class:open aria-label="pfwd">
  <div class="brand">pfwd</div>
  <button class="btn small menu-button" aria-expanded={open} onclick={() => (open = !open)}>
    {current || "Hosts"} <span aria-hidden="true">▾</span>
  </button>
  <div class="nav">
    {#each hosts as host (host)}
      {@const s = app.model.sessions[host]}
      <a
        class="side-link"
        href={viewHash({ kind: "host", host, tab: "ports" })}
        aria-current={current === host ? "page" : undefined}
        onclick={() => (open = false)}
      >
        <State state={s.state} word="" />
        <span class="name">{host}</span>
        <span class="visually-hidden">{s.state}</span>
        <span class="count" aria-label="forwards">{forwardsOf(app.model, host).length || ""}</span>
      </a>
    {/each}
    <button class="side-link muted" onclick={() => (connecting = true)}>+ Connect a host</button>
    <div class="grow"></div>
    <div class="sep"></div>
    <a class="side-link" href="#/events" aria-current={app.view.kind === "events" ? "page" : undefined} onclick={() => (open = false)}>Events</a>
    <a class="side-link" href="#/settings" aria-current={app.view.kind === "settings" ? "page" : undefined} onclick={() => (open = false)}>Settings</a>
    <button class="side-link" onclick={onhelp}>Keyboard shortcuts</button>
    {#if app.version}<span class="muted mono side-link" aria-label="version">{app.version}</span>{/if}
  </div>
</nav>

{#if connecting}
  <ConnectDialog onclose={() => (connecting = false)} />
{/if}
