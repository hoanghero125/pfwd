<script lang="ts">
  import { app, viewHash } from "../lib/store.svelte";
  import ConnectDialog from "./ConnectDialog.svelte";
  import State from "./State.svelte";

  let connecting = $state(false);
  const hosts = $derived(Object.values(app.model.sessions));
</script>

<div class="welcome">
  <h1>{hosts.length ? "Pick a host" : "Connect a host to start"}</h1>
  {#if hosts.length}
    <p>Choose one to see its ports, or connect another from your ssh config.</p>
    <ul class="pick">
      {#each hosts as s (s.host)}
        <li>
          <a href={viewHash({ kind: "host", host: s.host, tab: "ports" })}>
            <span class="mono">{s.host}</span>
            <State state={s.state} />
          </a>
        </li>
      {/each}
    </ul>
  {:else if app.link === "up"}
    <p>pfwd isn't connected to any host yet. Connect one and its listening ports show up here, ready to forward.</p>
  {/if}
  <button class="btn primary" onclick={() => (connecting = true)}>+ connect a host</button>
</div>

{#if connecting}
  <ConnectDialog onclose={() => (connecting = false)} />
{/if}
