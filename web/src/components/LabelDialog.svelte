<script lang="ts">
  import { app } from "../lib/store.svelte";
  import type { Forward } from "../lib/types";
  import Dialog from "./Dialog.svelte";

  let { f, onclose }: { f: Forward; onclose: () => void } = $props();
  // svelte-ignore state_referenced_locally
  let label = $state(f.label);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if ((await app.relabel(f, label.trim())) !== undefined) onclose();
  }
</script>

<Dialog title="Label :{f.remote}" {onclose}>
  <form onsubmit={submit}>
    <div class="field">
      <label for="label-text">Label</label>
      <input id="label-text" type="text" bind:value={label} maxlength="40" autocomplete="off" />
      <span class="hint">Shown next to the port, and remembered when the forward is pinned.</span>
    </div>
    <div class="btns">
      <button type="button" class="btn" onclick={onclose}>Cancel</button>
      <button type="submit" class="btn primary">Save label</button>
    </div>
  </form>
</Dialog>
