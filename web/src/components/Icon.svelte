<script lang="ts">
  import { icons, type IconName } from "../lib/icons";

  // A Phosphor icon, decorative: whatever it labels says it in text or in
  // an aria-label on its control. Sizes are classes (the CSP forbids
  // inline styles).
  let { name, size = 16 }: { name: IconName; size?: 12 | 14 | 16 | 18 | 20 | 24 } = $props();
</script>

<span class="icon i{size}" aria-hidden="true">{@html icons[name]}</span>
