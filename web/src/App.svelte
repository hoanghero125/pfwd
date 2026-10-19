<script lang="ts">
  import Events from "./components/Events.svelte";
  import Help from "./components/Help.svelte";
  import Home from "./components/Home.svelte";
  import HostView from "./components/HostView.svelte";
  import PromptDialog from "./components/PromptDialog.svelte";
  import Settings from "./components/Settings.svelte";
  import Topbar from "./components/Topbar.svelte";
  import { app } from "./lib/store.svelte";

  let help = $state(false);
  const marks = { info: "·", ok: "✓", warn: "◌", error: "✗" } as const;
  const prompt = $derived(app.model.prompts[0]);
  const retryIn = $derived(Math.max(0, Math.ceil((app.retryAt - app.now) / 1000)));

  // typing reports whether a key goes to a text field or a dialog.
  function typing(e: KeyboardEvent): boolean {
    const t = e.target as HTMLElement | null;
    return !!t?.closest("input, textarea, select, dialog, [contenteditable]");
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.ctrlKey || e.metaKey || e.altKey || typing(e)) return;
    if (e.key === "?") {
      help = true;
      e.preventDefault();
    } else if (e.key === "/") {
      const filter = document.querySelector<HTMLInputElement>("[data-filter]");
      if (filter) {
        filter.focus();
        e.preventDefault();
      }
    }
  }
</script>

<svelte:window {onkeydown} />

{#if app.link === "login"}
  <div class="banner info" role="alert">
    <span class="mark" aria-hidden="true">·</span>
    <span>This tab isn't logged in to pfwd, or the daemon restarted. Run <code>pfwd ui</code> in a terminal for a new login link.</span>
  </div>
{:else if app.link === "retrying"}
  <div class="banner">
    <span class="mark" aria-hidden="true">◌</span>
    <span role="alert">Lost the connection to the pfwd daemon.</span>
    <span class="muted">Retrying in {retryIn} s.</span>
    <button class="link go" onclick={() => app.retryNow()}>retry now</button>
  </div>
{/if}

<div class="shell" inert={app.link !== "up"}>
  <Topbar onhelp={() => (help = true)} />
  <main>
    {#if app.view.kind === "host"}
      {#key app.view.host}
        <HostView host={app.view.host} tab={app.view.tab} />
      {/key}
    {:else if app.view.kind === "events"}
      <Events />
    {:else if app.view.kind === "settings"}
      <Settings />
    {:else}
      <Home />
    {/if}
  </main>
</div>

{#if prompt && app.link === "up"}
  {#key prompt.id}
    <PromptDialog {prompt} />
  {/key}
{/if}

{#if help}
  <Help onclose={() => (help = false)} />
{/if}

<div class="toasts" role="status" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast {t.level}"><span class="mark" aria-hidden="true">{marks[t.level]}</span><span>{t.text}</span></div>
  {/each}
</div>
