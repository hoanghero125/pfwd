<script lang="ts">
  import { ago } from "../lib/format";
  import { app } from "../lib/store.svelte";
  import Dialog from "./Dialog.svelte";

  // Connect a host: ssh config's hosts, most recently used first, or any
  // user@host:port typed in.
  let { onclose }: { onclose: () => void } = $props();
  let query = $state("");

  void app.loadHosts();
  const choices = $derived(
    app.hosts.filter((h) => !app.model.sessions[h.host] || ["closed", "failed"].includes(app.model.sessions[h.host].state))
      .filter((h) => h.host.toLowerCase().includes(query.trim().toLowerCase())),
  );

  function pick(host: string) {
    if (!host.trim()) return;
    void app.connectHost(host.trim());
    onclose();
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    pick(choices[0]?.host ?? query);
  }
</script>

<Dialog title="Connect a host" {onclose}>
  <form onsubmit={submit}>
    <div class="field">
      <label for="connect-q">Host from your ssh config, or user@host:port</label>
      <!-- svelte-ignore a11y_autofocus -->
      <input id="connect-q" type="search" bind:value={query} autocomplete="off" autofocus />
    </div>
    {#if choices.length}
      <ul class="pick">
        {#each choices as h (h.host)}
          <li>
            <button type="button" onclick={() => pick(h.host)}>
              <span class="mono">{h.host}</span>
              <span class="muted">{ago(h.last_used, app.now)}{#if h.remembered} · {h.remembered} remembered{/if}</span>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="empty">{query ? `Press Enter to connect ${query}.` : "No hosts in your ssh config; type one."}</p>
    {/if}
    <div class="btns">
      <button type="button" class="btn" onclick={onclose}>Cancel</button>
      <button type="submit" class="btn primary" disabled={!choices.length && !query.trim()}>Connect</button>
    </div>
  </form>
</Dialog>
