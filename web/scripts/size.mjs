// Fails when the built app is over its budget: 150 KB gzipped in all.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const budget = 150 * 1024;
let total = 0;
for (const dir of ["dist", "dist/assets"]) {
  for (const name of readdirSync(dir, { withFileTypes: true })) {
    if (name.isFile()) total += gzipSync(readFileSync(join(dir, name.name)), { level: 9 }).length;
  }
}
console.log(`web/dist: ${(total / 1024).toFixed(1)} KB gzipped; the budget is ${budget / 1024} KB`);
if (total > budget) process.exit(1);
