<script lang="ts">
  import type { Snippet } from "svelte";

  // A modal dialog: it opens when shown, Escape calls onclose, and focus
  // goes back to where it was, or to returnTo (an element id) when what
  // opened it is gone, such as a menu item.
  let {
    title,
    onclose,
    children,
    returnTo = "",
    drawer = false,
  }: { title: string; onclose: () => void; children: Snippet; returnTo?: string; drawer?: boolean } = $props();
  const id = $props.id();
  let el: HTMLDialogElement;

  $effect(() => {
    const d = el;
    const opener = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null;
    d.showModal();
    return () => {
      if (d.open) d.close();
      setTimeout(() => {
        const back = (opener?.isConnected ? opener : null) ?? (returnTo ? document.getElementById(returnTo) : null);
        back?.focus();
      });
    };
  });

  function cancel(e: Event) {
    e.preventDefault();
    onclose();
  }
</script>

<dialog bind:this={el} class:drawer aria-labelledby="{id}-title" oncancel={cancel}>
  <div class="dialog-head">
    <h2 id="{id}-title">{title}</h2>
    {#if drawer}<button class="link" aria-label="Close" onclick={onclose}>close <kbd>esc</kbd></button>{/if}
  </div>
  {@render children()}
</dialog>
