<script lang="ts">
  import { glyph, themeState } from "../lib/glyph";

  // A state as the TUI shows it: a colored glyph and the word.
  let { state, word = state }: { state: string; word?: string } = $props();
</script>

<span class="g-{themeState(state)}"><span aria-hidden="true">{glyph(state)}</span> {word}</span>
