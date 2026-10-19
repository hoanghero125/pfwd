<script lang="ts">
  import { localAddr } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app } from "../lib/store.svelte";
  import Tunnel from "./Tunnel.svelte";

  // Reverse forwards: a port on this machine, exposed on the server. Their
  // tunnels run from this computer to the server.
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

<p class="lead">Expose a port of this machine on {host}: programs there reach it at 127.0.0.1 on the server port.</p>
<form class="inline-form" onsubmit={submit}>
  <div class="field">
    <label for="rv-local">Local port</label>
    <input id="rv-local" type="text" inputmode="numeric" bind:value={local} aria-invalid={local !== "" && !port(local)} required />
  </div>
  <div class="field">
    <label for="rv-remote">Server port</label>
    <input id="rv-remote" type="text" inputmode="numeric" bind:value={remote} aria-invalid={remote !== "" && !port(remote)} placeholder="same" />
  </div>
  <button type="submit" class="btn primary" disabled={busy || !connected || !port(local)}>expose</button>
</form>

<section class="stage" aria-labelledby="reverse-title">
  <h2 id="reverse-title" class="group">exposed on {host} <span class="n">{reverse.length}</span></h2>
  <div class="ends" aria-hidden="true">
    <span class="from">{host}</span>
    <span class="mid"></span>
    <span class="to">this computer</span>
  </div>
  {#if reverse.length}
    <div class="rows" role="list" aria-label="Reverse forwards">
      {#each reverse as f (f.id)}
        <div class="row" role="listitem">
          <span class="port">:{f.remote}</span>
          <span class="proc">{#if f.label}<span class="label">{f.label}</span>{:else}reverse forward{/if}</span>
          <Tunnel {f} />
          <span class="addr-cell"><span class="addr" class:on={f.state === "active"}>{localAddr(f)}</span></span>
          <span class="acts"><button class="link danger" onclick={() => app.stop(f)}>stop</button></span>
        </div>
      {/each}
    </div>
  {:else}
    <p class="empty">Nothing exposed yet.</p>
  {/if}
</section>
