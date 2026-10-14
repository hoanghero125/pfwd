<script lang="ts">
  import { localAddr } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app } from "../lib/store.svelte";
  import State from "./State.svelte";

  // Reverse forwards: a port on this machine, exposed on the server.
  let { host }: { host: string } = $props();
  let local = $state("");
  let remote = $state("");
  let busy = $state(false);

  const reverse = $derived(forwardsOf(app.model, host, "remote"));
  const connected = $derived(app.model.sessions[host]?.state === "connected");

  function port(s: string): number | null {
    const n = Number(s);
    return Number.isInteger(n) && n >= 1 && n <= 65535 ? n : null;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    const l = port(local);
    const r = remote ? port(remote) : l;
    if (!l || !r) return;
    busy = true;
    const f = await app.forward(host, { kind: "remote", local: l, remote_host: "localhost", remote: r });
    busy = false;
    if (f) local = remote = "";
  }
</script>

<p class="muted">Expose a port of this machine on {host}: programs there reach it at 127.0.0.1 on the server port.</p>
<form class="toolbar" onsubmit={submit}>
  <div class="field">
    <label for="rv-local">Local port</label>
    <input id="rv-local" type="text" inputmode="numeric" bind:value={local} aria-invalid={local !== "" && !port(local)} required />
  </div>
  <div class="field">
    <label for="rv-remote">Server port</label>
    <input id="rv-remote" type="text" inputmode="numeric" bind:value={remote} aria-invalid={remote !== "" && !port(remote)} placeholder="same" />
  </div>
  <div class="field">
    <span class="label" aria-hidden="true">&nbsp;</span>
    <button type="submit" class="btn primary" disabled={busy || !connected || !port(local)}>Expose</button>
  </div>
</form>

<h2 class="group">Exposed on {host} ({reverse.length})</h2>
{#if reverse.length}
<div class="rows" role="list" aria-label="Reverse forwards">
  {#each reverse as f (f.id)}
    <div class="row" role="listitem">
      <span class="st"><State state={f.state} /></span>
      <span class="patch">
        <span class="jack">{localAddr(f)}</span>
        <span class="cable g-{f.state} {f.state}" aria-hidden="true"></span>
        <span class="jack">{host}:{f.remote}</span>
      </span>
      <span class="proc">{f.state === "paused" ? f.pause : f.error}</span>
      <span class="label">{f.label}</span>
      <span class="traffic"></span>
      <span class="acts"><button class="btn small" onclick={() => app.stop(f)}>Stop</button></span>
    </div>
  {/each}
</div>
{:else}
  <p class="empty">Nothing exposed yet.</p>
{/if}
