<script lang="ts">
  // pfwd's mark (assets/logo): an open ring, the line, a solid dot, in the
  // page's ink and active green. Under 48 px it is the small cut, with no
  // packet on the line.
  let { size = 22 }: { size?: number } = $props();
  const small = $derived(size < 48);
</script>

<svg class="logo" viewBox="0 0 256 256" width={size} height={size} aria-hidden="true">
  {#if small}
    <path class="ink" fill-rule="evenodd" d="M29.67,180.33a46,46 0 1,0 92,0a46,46 0 1,0 -92,0ZM53.67,180.33a22,22 0 1,0 44,0a22,22 0 1,0 -44,0Z" />
    <path class="ink" d="M98.3,140.73L140.73,98.3L157.7,115.27L115.27,157.7Z" />
    <path class="go" d="M134.33,75.67a46,46 0 1,0 92,0a46,46 0 1,0 -92,0Z" />
  {:else}
    <path class="ink" fill-rule="evenodd" d="M34.26,181.74a40,40 0 1,0 80,0a40,40 0 1,0 -80,0ZM54.26,181.74a20,20 0 1,0 40,0a20,20 0 1,0 -40,0Z" />
    <path class="ink" d="M96.89,150.63L150.63,96.89L159.11,105.37L105.37,159.11Z" />
    <path class="go" d="M111,128a17,17 0 1,0 34,0a17,17 0 1,0 -34,0Z" />
    <path class="go" d="M141.74,74.26a40,40 0 1,0 80,0a40,40 0 1,0 -80,0Z" />
  {/if}
</svg>
