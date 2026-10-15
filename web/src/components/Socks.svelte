<script lang="ts">
  import { localAddr } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app } from "../lib/store.svelte";
  import Icon from "./Icon.svelte";
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

<p class="lead">A SOCKS5 proxy on this machine sends each connection through {host}. Point a browser or <code>curl --socks5-hostname</code> at it.</p>

{#each proxies as f (f.id)}
  <div class="proxy">
    <span class="badge"><Icon name="socks" size={20} /></span>
    <div class="grow">
      <div class="addr">socks5h://{localAddr(f)}</div>
      <div class="sub"><State state={f.state} />{#if f.error}<span class="muted">{f.error}</span>{:else if f.conns}<span class="muted">{f.conns} open {f.conns === 1 ? "connection" : "connections"}</span>{/if}</div>
    </div>
    <button class="btn" onclick={() => app.copy(`socks5h://${localAddr(f)}`)}><Icon name="copy" /> Copy address</button>
    <button class="btn ghost danger" onclick={() => app.stop(f)}><Icon name="close" /> Stop</button>
  </div>
{:else}
  <form class="inline-form" onsubmit={start}>
    <div class="field">
      <label for="socks-port">Local port</label>
      <input id="socks-port" type="text" inputmode="numeric" bind:value={port} aria-invalid={!valid} />
    </div>
    <button type="submit" class="btn primary" disabled={busy || !connected || !valid}><Icon name="socks" /> Start the proxy</button>
  </form>
{/each}
