<script lang="ts">
  import { glyph, themeState } from "../lib/glyph";

  // A state as the TUI shows it: its glyph and word in the state's color;
  // as a chip, on a tint of that color.
  let { state, word = state, chip = false }: { state: string; word?: string; chip?: boolean } = $props();
</script>

<span class="st g-{themeState(state)}" class:chip><span class="glyph" aria-hidden="true">{glyph(state)}</span>{#if word}<span>{word}</span>{/if}</span>
