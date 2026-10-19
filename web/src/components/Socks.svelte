<script lang="ts">
  import { localAddr } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app } from "../lib/store.svelte";
  import Tunnel from "./Tunnel.svelte";

  // The host's SOCKS proxy: apps that speak SOCKS5 reach anything the
  // server can.
  let { host }: { host: string } = $props();
  let port = $state("1080");
  let busy = $state(false);

  const proxies = $derived(forwardsOf(app.model, host, "dynamic"));
  const connected = $derived(app.model.sessions[host]?.state === "connected");
  const valid = $derived(Number.isInteger(Number(port)) && Number(port) >= 1 && Number(port) <= 65535);

  async function start(e: SubmitEvent) {
    e.preventDefault();
    if (!valid) return;
    busy = true;
    await app.forward(host, { kind: "dynamic", local: Number(port) });
    busy = false;
  }
</script>

<p class="lead">A SOCKS5 proxy on this machine sends each connection through {host}. Point a browser or <code>curl --socks5-hostname</code> at it.</p>

{#if proxies.length}
  <section class="stage" aria-label="SOCKS proxy">
    <div class="ends" aria-hidden="true">
      <span class="from">{host}</span>
      <span class="mid"></span>
      <span class="to">this computer</span>
    </div>
    <div class="rows" role="list" aria-label="SOCKS proxies">
      {#each proxies as f (f.id)}
        <div class="row proxy" role="listitem">
          <span class="port">any</span>
          <span class="proc">SOCKS proxy</span>
          <Tunnel {f} />
          <span class="addr-cell"><span class="addr" class:on={f.state === "active"}>socks5h://{localAddr(f)}</span></span>
          <span class="acts">
            <button class="link go" onclick={() => app.copy(`socks5h://${localAddr(f)}`)}>copy address</button>
            <button class="link danger" onclick={() => app.stop(f)}>stop</button>
          </span>
        </div>
      {/each}
    </div>
  </section>
{:else}
  <form class="inline-form" onsubmit={start}>
    <div class="field">
      <label for="socks-port">Local port</label>
      <input id="socks-port" type="text" inputmode="numeric" bind:value={port} aria-invalid={!valid} />
    </div>
    <button type="submit" class="btn primary" disabled={busy || !connected || !valid}>start the proxy</button>
  </form>
{/if}
