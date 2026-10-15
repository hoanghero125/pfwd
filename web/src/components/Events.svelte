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
<div class="toolbar">
  <input type="search" class="filter" placeholder="Filter events" aria-label="Filter events" data-filter bind:value={filter} />
  <select aria-label="Which events" bind:value={level}>
    <option value="all">All events</option>
    <option value="problems">Warnings and errors</option>
  </select>
</div>
<div class="log rows" role="log" aria-label="Events, newest first">
  {#each lines as l (l.seq)}
    <div class="line">
      <span class="muted">{clock(l.at)}</span>
      <span>{l.host}</span>
      <span><span class={tone[l.level]} aria-hidden="true">{marks[l.level]}</span> {l.text}</span>
    </div>
  {:else}
    <p class="empty">{filter || level !== "all" ? "No event matches." : "Events since this page opened show up here."}</p>
  {/each}
</div>
