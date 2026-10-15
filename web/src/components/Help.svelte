<script lang="ts">
  import Dialog from "./Dialog.svelte";

  let { onclose }: { onclose: () => void } = $props();
  const keys: [string, string][] = [
    ["j / k, ↓ / ↑", "Move down, up"],
    ["g / G", "Top, bottom"],
    ["Enter, f", "Forward the port, or stop its forward"],
    ["F", "Forward to another local port"],
    ["x", "Stop the forward"],
    ["p", "Pin or unpin"],
    ["o", "Open in the browser"],
    ["y", "Copy the URL"],
    ["a", "Auto-forward on or off for this host"],
    ["h", "Show or hide hidden ports"],
    ["/", "Filter"],
    ["?", "These keys"],
  ];
</script>

<Dialog title="Keyboard shortcuts" icon="keyboard" {onclose}>
  <div class="keys">
    {#each keys as [k, what] (k)}
      <span class="k">{#each k.split(", ") as group, i (group)}{#if i}<span class="muted">or</span>{/if}{#each group.split(" / ") as key, j (key)}{#if j}<span class="muted">/</span>{/if}<kbd>{key}</kbd>{/each}{/each}</span>
      <span>{what}</span>
    {/each}
  </div>
  <div class="btns"><button class="btn" onclick={onclose}>Close</button></div>
</Dialog>
