// The web UI served by a real pfwd ui against an sshd container. Go's
// TestE2EWebUI starts everything and sets PFWD_UI_URL (the login link),
// PFWD_E2E_HOST and PFWD_E2E_PORT (a port listening on the server); it
// curls the forward afterwards.

import { expect, test } from "@playwright/test";

test("forward a detected port from the browser", async ({ page }) => {
  const login = process.env.PFWD_UI_URL;
  const host = process.env.PFWD_E2E_HOST;
  const port = process.env.PFWD_E2E_PORT;
  test.skip(!login || !host || !port, "run by test/e2e's TestE2EWebUI");
  await page.goto(login!);
  await page.goto(`${new URL(login!).origin}/#/host/${encodeURIComponent(host!)}`);
  const detected = page.getByRole("list", { name: "Detected ports" });
  const row = detected.getByRole("listitem").filter({ hasText: `:${port}` });
  await expect(row).toBeVisible({ timeout: 20_000 });
  await row.getByRole("button", { name: "Forward" }).click();
  const forwarded = page.getByRole("list", { name: "Forwarded ports" }).getByRole("listitem").filter({ hasText: `:${port}` });
  await expect(forwarded).toContainText("active", { timeout: 20_000 });
});
