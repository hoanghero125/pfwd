import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vitest/config";

// The build goes to dist/, which the pfwd binary embeds. Everything is a
// file: no inline scripts or styles, so the server's CSP can forbid them.
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: "assets",
    cssCodeSplit: false,
    modulePreload: { polyfill: false },
    reportCompressedSize: false,
    target: "es2022",
  },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
