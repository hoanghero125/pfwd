<script lang="ts">
  import { forwardsOf } from "../lib/reduce";
  import { app, viewHash } from "../lib/store.svelte";
  import ConnectDialog from "./ConnectDialog.svelte";
  import Icon from "./Icon.svelte";
  import State from "./State.svelte";

  let { onhelp }: { onhelp: () => void } = $props();
  let open = $state(false);
  let connecting = $state(false);

  // The daemon's hosts, in the order they were connected.
  const hosts = $derived(Object.keys(app.model.sessions));
  const current = $derived(app.view.kind === "host" ? app.view.host : "");
</script>

<nav class="side" class:open aria-label="pfwd">
  <div class="brand"><Icon name="brand" size={20} /> pfwd</div>
  <button class="btn small menu-button" aria-expanded={open} onclick={() => (open = !open)}>
    {current || "Hosts"} <Icon name="caret" size={12} />
  </button>
  <div class="nav">
    <div class="side-title">Hosts</div>
    {#each hosts as host (host)}
      {@const s = app.model.sessions[host]}
      {@const n = forwardsOf(app.model, host).length}
      <a
        class="side-link"
        href={viewHash({ kind: "host", host, tab: "ports" })}
        aria-current={current === host ? "page" : undefined}
        onclick={() => (open = false)}
      >
        <State state={s.state} word="" />
        <span class="name">{host}</span>
        <span class="visually-hidden">{s.state}</span>
        {#if n}<span class="count" aria-label="{n} forwards">{n}</span>{/if}
      </a>
    {/each}
    <button class="side-link add" onclick={() => (connecting = true)}><Icon name="plus" size={14} /> Connect a host</button>
    <div class="grow"></div>
    <div class="foot">
      <a class="side-link" href="#/events" aria-current={app.view.kind === "events" ? "page" : undefined} onclick={() => (open = false)}><Icon name="events" /> Events</a>
      <a class="side-link" href="#/settings" aria-current={app.view.kind === "settings" ? "page" : undefined} onclick={() => (open = false)}><Icon name="settings" /> Settings</a>
      <button class="side-link" onclick={onhelp}><Icon name="keyboard" /> Keyboard shortcuts</button>
      {#if app.version}<span class="version">pfwd {app.version}</span>{/if}
    </div>
  </div>
</nav>

{#if connecting}
  <ConnectDialog onclose={() => (connecting = false)} />
{/if}
