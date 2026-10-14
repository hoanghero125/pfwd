<script lang="ts">
  import { localAddr } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app } from "../lib/store.svelte";
  import State from "./State.svelte";

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

<p class="muted">A SOCKS5 proxy on this machine sends each connection through {host}. Point a browser or <code>curl --socks5-hostname</code> at it.</p>

{#each proxies as f (f.id)}
  <div class="notice warn">
    <State state={f.state} />
    <span>SOCKS proxy on <span class="mono">{localAddr(f)}</span>{#if f.error}: {f.error}{/if}</span>
    <button class="btn small" onclick={() => app.copy(`socks5h://${localAddr(f)}`)}>Copy address</button>
    <button class="btn small danger" onclick={() => app.stop(f)}>Stop</button>
  </div>
{:else}
  <form class="toolbar" onsubmit={start}>
    <div class="field">
      <label for="socks-port">Local port</label>
      <input id="socks-port" type="text" inputmode="numeric" bind:value={port} aria-invalid={!valid} />
    </div>
    <div class="field">
      <span class="label" aria-hidden="true">&nbsp;</span>
      <button type="submit" class="btn primary" disabled={busy || !connected || !valid}>Start the proxy</button>
    </div>
  </form>
{/each}
