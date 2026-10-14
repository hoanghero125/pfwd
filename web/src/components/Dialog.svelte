<script lang="ts">
  import type { Snippet } from "svelte";

  // A modal dialog: it opens when shown, and Escape calls onclose.
  let { title, onclose, children }: { title: string; onclose: () => void; children: Snippet } = $props();
  const id = $props.id();
  let el: HTMLDialogElement;

  $effect(() => {
    const d = el;
    d.showModal();
    return () => {
      if (d.open) d.close();
    };
  });

  function cancel(e: Event) {
    e.preventDefault();
    onclose();
  }
</script>

<dialog bind:this={el} aria-labelledby="{id}-title" oncancel={cancel}>
  <h2 id="{id}-title">{title}</h2>
  {@render children()}
</dialog>
