<script lang="ts">
  import type { Snippet } from "svelte";

  import type { IconName } from "../lib/icons";
  import Icon from "./Icon.svelte";

  // A modal dialog: it opens when shown, Escape calls onclose, and focus
  // goes back to where it was, or to returnTo (an element id) when what
  // opened it is gone, such as a menu item.
  let {
    title,
    onclose,
    children,
    returnTo = "",
    drawer = false,
    icon,
  }: { title: string; onclose: () => void; children: Snippet; returnTo?: string; drawer?: boolean; icon?: IconName } = $props();
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
    {#if icon}<span class="badge"><Icon name={icon} size={18} /></span>{/if}
    <h2 id="{id}-title">{title}</h2>
    {#if drawer}<button class="btn ghost icon-only" aria-label="Close" onclick={onclose}><Icon name="close" size={18} /></button>{/if}
  </div>
  {@render children()}
</dialog>
