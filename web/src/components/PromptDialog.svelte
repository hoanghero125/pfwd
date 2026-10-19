<script lang="ts">
  import { app } from "../lib/store.svelte";
  import type { Prompt } from "../lib/types";
  import Dialog from "./Dialog.svelte";

  // The daemon's questions, one at a time: a new host key, or a
  // passphrase, password or code.
  let { prompt }: { prompt: Prompt } = $props();
  let secret = $state("");

  const title = $derived.by(() => {
    switch (prompt.kind) {
      case "host_key":
        return `Trust the host key of ${prompt.host}?`;
      case "passphrase":
        return `Passphrase for ${prompt.prompt || "your key"}`;
      case "code":
        return `Code for ${prompt.host}`;
    }
    return `Password for ${prompt.prompt ? `${prompt.prompt}@${prompt.host}` : prompt.host}`;
  });

  function answer(body: { accept?: boolean; secret?: string; cancel?: boolean }) {
    void app.answer(prompt.id, body);
    secret = "";
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (prompt.kind === "host_key") answer({ accept: true });
    else answer({ secret });
  }
</script>

<Dialog {title} onclose={() => answer(prompt.kind === "host_key" ? { accept: false } : { cancel: true })}>
  <form onsubmit={submit}>
    {#if prompt.kind === "host_key"}
      <p class="muted">pfwd has never connected to {prompt.host}. Compare this fingerprint with the server's before you trust it.</p>
      <div class="fp">{prompt.key_type} {prompt.fingerprint}</div>
      <p class="muted">{prompt.saves ? "It will be saved to your known_hosts file." : "It is trusted for this connection only."}</p>
      <div class="btns">
        <button type="button" class="btn" onclick={() => answer({ accept: false })}>Cancel</button>
        <button type="submit" class="btn primary">Trust and connect</button>
      </div>
    {:else}
      {#if prompt.retry}<p class="problems-line" role="alert"><span aria-hidden="true">✗</span> That didn't work. Try again.</p>{/if}
      {#if prompt.instruction}<p class="muted">{prompt.instruction}</p>{/if}
      <div class="field">
        <label for="secret">{prompt.kind === "code" ? prompt.prompt || "Code" : prompt.kind === "passphrase" ? "Passphrase" : "Password"}</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="secret" type={prompt.echo ? "text" : "password"} bind:value={secret} autocomplete="off" autofocus />
      </div>
      <div class="btns">
        <button type="button" class="btn" onclick={() => answer({ cancel: true })}>Cancel</button>
        <button type="submit" class="btn primary">{prompt.kind === "passphrase" ? "Unlock" : "Log in"}</button>
      </div>
    {/if}
  </form>
</Dialog>
