<script lang="ts">
  import { app } from "../lib/store.svelte";
  import type { Port } from "../lib/types";
  import Dialog from "./Dialog.svelte";

  // A local forward with every field: a new one, or a detected port on
  // another local port.
  let { host, port, onclose }: { host: string; port?: Port; onclose: () => void } = $props();
  // The fields start from the port; the dialog doesn't follow it later.
  // svelte-ignore state_referenced_locally
  let local = $state(port ? String(port.port) : "");
  let remoteHost = $state("localhost");
  // svelte-ignore state_referenced_locally
  let remote = $state(port ? String(port.port) : "");
  let busy = $state(false);

  const remoteOK = $derived(valid(remote));
  const localOK = $derived(local === "" || valid(local));

  function valid(s: string): boolean {
    const n = Number(s);
    return Number.isInteger(n) && n >= 1 && n <= 65535;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!remoteOK || !localOK) return;
    busy = true;
    const f = await app.forward(host, {
      kind: "local",
      local: local ? Number(local) : 0,
      remote_host: remoteHost.trim() || "localhost",
      remote: Number(remote),
      label: port?.label,
    });
    busy = false;
    if (f) onclose();
  }
</script>

<Dialog title={port ? `Forward :${port.port} to another local port` : `New forward on ${host}`} {onclose}>
  <form onsubmit={submit}>
    <div class="fields">
      {#if !port}
        <div class="field">
          <label for="fd-rhost">Server address</label>
          <input id="fd-rhost" type="text" bind:value={remoteHost} autocomplete="off" />
        </div>
        <div class="field">
          <label for="fd-remote">Server port</label>
          <input id="fd-remote" type="text" inputmode="numeric" bind:value={remote} aria-invalid={remote !== "" && !remoteOK} required />
        </div>
      {/if}
      <div class="field">
        <label for="fd-local">Local port</label>
        <input id="fd-local" type="text" inputmode="numeric" bind:value={local} aria-invalid={!localOK} placeholder="same as the server's" />
      </div>
    </div>
    <p class="hint muted">The server address is resolved on {host}: localhost is {host} itself, and a name like db.internal reaches a machine behind it.</p>
    <div class="btns">
      <button type="button" class="btn" onclick={onclose}>Cancel</button>
      <button type="submit" class="btn primary" disabled={busy || !remoteOK || !localOK}>Forward</button>
    </div>
  </form>
</Dialog>
