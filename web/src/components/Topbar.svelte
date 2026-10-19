<script lang="ts">
  import { glyph, themeState } from "../lib/glyph";
  import { forwardsOf } from "../lib/reduce";
  import { app, viewHash } from "../lib/store.svelte";
  import ConnectDialog from "./ConnectDialog.svelte";
  import Logo from "./Logo.svelte";

  // The top line, like the TUI's host bar: pfwd, the hosts with their
  // states, then the pages.
  let { onhelp }: { onhelp: () => void } = $props();
  let connecting = $state(false);

  // The daemon's hosts, in the order they were connected.
  const hosts = $derived(Object.keys(app.model.sessions));
  const current = $derived(app.view.kind === "host" ? app.view.host : "");
</script>

<nav class="top" aria-label="pfwd">
  <span class="brand"><Logo size={22} />pfwd</span>
  <span class="hosts">
    {#each hosts as host (host)}
      {@const s = app.model.sessions[host]}
      {@const n = forwardsOf(app.model, host).length}
      <a href={viewHash({ kind: "host", host, tab: "ports" })} aria-current={current === host ? "page" : undefined}>
        <span class="glyph g-{themeState(s.state)}" aria-hidden="true">{glyph(s.state)}</span>{host}<span class="visually-hidden">, {s.state}{n ? `, ${n} ${n === 1 ? "forward" : "forwards"}` : ""}</span>
      </a>
    {/each}
    <button class="link" aria-label="Connect a host" onclick={() => (connecting = true)}>+ connect</button>
  </span>
  <span class="grow"></span>
  <a class="page" href="#/events" aria-current={app.view.kind === "events" ? "page" : undefined}>events</a>
  <a class="page" href="#/settings" aria-current={app.view.kind === "settings" ? "page" : undefined}>settings</a>
  <button class="link" aria-label="Keyboard shortcuts" onclick={onhelp}>?</button>
  {#if app.version}<span class="version">{app.version}</span>{/if}
</nav>

{#if connecting}
  <ConnectDialog onclose={() => (connecting = false)} />
{/if}
