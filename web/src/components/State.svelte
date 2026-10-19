<script lang="ts">
  import { glyph, themeState } from "../lib/glyph";

  // A state as the TUI shows it: its glyph in the state's color, then the
  // word.
  let { state, word = state }: { state: string; word?: string } = $props();
</script>

<span class="st g-{themeState(state)}"><span class="glyph" aria-hidden="true">{glyph(state)}</span>{#if word}<span>{word}</span>{/if}</span>
