<script lang="ts">
  import { clock } from "../lib/format";
  import { app } from "../lib/store.svelte";

  // The event log: the TUI's L pane, kept since this page opened.
  let filter = $state("");
  let level = $state<"all" | "problems">("all");
  const marks = { info: "·", ok: "✓", warn: "◌", error: "✗" } as const;
  const tone = { info: "g-closed", ok: "g-active", warn: "g-paused", error: "g-failed" } as const;

  const lines = $derived(
    app.model.log
      .filter((l) => level === "all" || l.level === "warn" || l.level === "error")
      .filter((l) => !filter || `${l.host} ${l.text}`.toLowerCase().includes(filter.toLowerCase()))
      .slice()
      .reverse(),
  );
</script>

<div class="head"><h1>Events</h1></div>
<p class="lead">What pfwd did since this page opened, newest first.</p>
<div class="tabsbar">
  <div class="seg" role="group" aria-label="Which events">
    <button aria-pressed={level === "all"} onclick={() => (level = "all")}>all events</button>
    <button aria-pressed={level === "problems"} onclick={() => (level = "problems")}>warnings and errors</button>
  </div>
  <span class="grow"></span>
  <label class="filter">
    <kbd aria-hidden="true">/</kbd>
    <input type="search" placeholder="filter events" aria-label="Filter events" data-filter bind:value={filter} />
  </label>
</div>
<div class="log" role="log" aria-label="Events, newest first">
  {#each lines as l (l.seq)}
    <div class="line">
      <span class="when">{clock(l.at)}</span>
      <span class="host">{l.host}</span>
      <span class="st {tone[l.level]}" aria-hidden="true"><span class="glyph">{marks[l.level]}</span></span>
      <span class="text">{l.text}</span>
    </div>
  {:else}
    <p class="empty">{filter || level !== "all" ? "No event matches." : "Events since this page opened show up here."}</p>
  {/each}
</div>
