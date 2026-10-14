<script lang="ts">
  import { app, viewHash } from "../lib/store.svelte";
  import ConnectDialog from "./ConnectDialog.svelte";
  import State from "./State.svelte";

  let connecting = $state(false);
  const hosts = $derived(Object.values(app.model.sessions));
</script>

<div class="head"><h1>pfwd</h1></div>
{#if hosts.length}
  <p class="empty">Pick a host on the left, or connect another one.</p>
  <ul class="pick">
    {#each hosts as s (s.host)}
      <li>
        <a class="side-link" href={viewHash({ kind: "host", host: s.host, tab: "ports" })}>
          <State state={s.state} /> <span class="mono">{s.host}</span>
        </a>
      </li>
    {/each}
  </ul>
{:else if app.link === "up"}
  <p class="empty">pfwd isn't connected to any host yet. Connect one and its listening ports show up here, ready to forward.</p>
{/if}
<p><button class="btn primary" onclick={() => (connecting = true)}>Connect a host</button></p>

{#if connecting}
  <ConnectDialog onclose={() => (connecting = false)} />
{/if}
