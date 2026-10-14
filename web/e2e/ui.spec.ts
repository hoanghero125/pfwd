// The web UI against a mocked daemon: every screen, the main flows, the
// keyboard, accessibility (axe, WCAG 2.2 AA) and screenshots in light and
// dark at 1280 and 390 px.

import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { Daemon, forward } from "./mock";

async function open(page: Page, hash = "#/host/devbox") {
  const d = new Daemon(page);
  await d.install();
  await page.goto("/" + hash);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  return d;
}

async function axe(page: Page, what: string) {
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
  expect(r.violations.map((v) => `${what}: ${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
}

test("the ports view shows forwarded and detected ports", async ({ page }) => {
  await open(page);
  await expect(page.getByRole("heading", { name: "devbox" })).toBeVisible();
  await expect(page.getByText("Forwarded (3)")).toBeVisible();
  await expect(page.getByText("Detected, not forwarded (2)")).toBeVisible();
  const forwarded = page.getByRole("list", { name: "Forwarded ports" });
  await expect(forwarded.getByRole("listitem")).toHaveCount(3);
  await expect(forwarded.getByText("localhost:15432")).toBeVisible();
  await expect(forwarded.getByText("port closed")).toBeVisible();
  // ignored ports stay hidden until asked for.
  await expect(page.getByText(":22", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Show 1 hidden port" }).click();
  await expect(page.getByText(":22", { exact: true })).toBeVisible();
});

test("forwarding a detected port", async ({ page }) => {
  const d = await open(page);
  const detected = page.getByRole("list", { name: "Detected ports" });
  await detected.getByRole("listitem").filter({ hasText: "redis-server" }).getByRole("button", { name: "Forward" }).click();
  await expect.poll(() => d.last("POST", "/api/forwards")?.body).toEqual({ host: "devbox", spec: { kind: "local", remote: 6379, label: "Redis" } });
  d.push("forward_changed", forward({ id: "9", local: 6379, remote: 6379, label: "Redis", auto: false, scheme: "" }));
  await expect(page.getByRole("list", { name: "Forwarded ports" }).getByText("localhost:6379")).toBeVisible();
  await expect(detected.getByText("redis-server")).toHaveCount(0);
});

test("stop, pin and label from the row menu", async ({ page }) => {
  const d = await open(page);
  const row = page.getByRole("list", { name: "Forwarded ports" }).getByRole("listitem").filter({ hasText: "localhost:5173" });
  await row.getByRole("button", { name: "More actions for :5173" }).click();
  await page.getByRole("menuitem", { name: "Pin" }).click();
  await expect.poll(() => d.last("PATCH", "/api/forwards/1")?.body).toEqual({ pinned: true });
  await row.getByRole("button", { name: "More actions for :5173" }).click();
  await page.getByRole("menuitem", { name: "Label…" }).click();
  await page.getByLabel("Label", { exact: true }).fill("Frontend");
  await page.getByRole("button", { name: "Save label" }).click();
  await expect.poll(() => d.last("PATCH", "/api/forwards/1")?.body).toEqual({ label: "Frontend" });
  await row.getByRole("button", { name: "More actions for :5173" }).click();
  await page.getByRole("menuitem", { name: "Stop" }).click();
  await expect.poll(() => d.last("DELETE", "/api/forwards/1")).toBeTruthy();
});

test("the details drawer", async ({ page }) => {
  const d = await open(page);
  for (let i = 1; i <= 5; i++) d.push("stats", { forwards: [{ id: "1", conns: 2, bytes_in: 3_400_000 + i * 40_000, bytes_out: 210_000 + i * 1000 }] });
  const row = page.getByRole("list", { name: "Forwarded ports" }).getByRole("listitem").filter({ hasText: "localhost:5173" });
  await row.getByRole("button", { name: "More actions for :5173" }).click();
  await page.getByRole("menuitem", { name: "Details" }).click();
  const drawer = page.getByRole("dialog", { name: ":5173 on devbox" });
  await expect(drawer.getByText("2 open")).toBeVisible();
  await expect(drawer.getByRole("img", { name: /Traffic rate/ })).toBeVisible();
  await expect(drawer.getByRole("listitem").filter({ hasText: /\d active$/ })).toBeVisible(); // the history from the snapshot
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await expect(row.getByRole("button", { name: "More actions for :5173" })).toBeFocused();
});

test("host key and password prompts are answered in the page", async ({ page }) => {
  const d = await open(page);
  d.push("prompt", { id: "1", session: "gpu-node", host: "gpu-node", kind: "host_key", key_type: "ssh-ed25519", fingerprint: "SHA256:2k3Yq6l9cR4m5xN0vB8wZtE1aF7gH3jK9pL2qS4uV6w", saves: true });
  d.push("prompt", { id: "2", session: "gpu-node", host: "gpu-node", kind: "password", prompt: "alice" });
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Trust the host key of gpu-node?" })).toBeVisible();
  await dialog.getByRole("button", { name: "Trust and connect" }).click();
  await expect.poll(() => d.last("POST", "/api/prompts/1")?.body).toEqual({ accept: true });
  await expect(page.getByRole("dialog").getByRole("heading", { name: "Password for alice@gpu-node" })).toBeVisible();
  await page.getByLabel("Password", { exact: true }).fill("hunter2");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect.poll(() => d.last("POST", "/api/prompts/2")?.body).toEqual({ secret: "hunter2" });
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("a prompt another client answered goes away", async ({ page }) => {
  const d = await open(page);
  d.push("prompt", { id: "5", session: "devbox", host: "devbox", kind: "code", prompt: "Verification code:" });
  await expect(page.getByRole("dialog")).toBeVisible();
  d.push("prompt_done", { id: "5" });
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("the keyboard mirrors the TUI", async ({ page }) => {
  const d = await open(page);
  const current = page.locator('.row[aria-current="true"]');
  await expect(current).toContainText(":5173");
  await page.keyboard.press("j");
  await expect(current).toContainText(":5432");
  await page.keyboard.press("G");
  await expect(current).toContainText(":9090");
  await page.keyboard.press("k");
  await expect(current).toContainText("redis-server");
  await page.keyboard.press("Enter");
  await expect.poll(() => d.last("POST", "/api/forwards")?.body).toMatchObject({ spec: { remote: 6379 } });
  await page.keyboard.press("a");
  await expect.poll(() => d.last("PATCH", "/api/hosts/devbox/settings")?.body).toEqual({ auto: false });
  await page.keyboard.press("/");
  await expect(page.getByRole("searchbox", { name: "Filter ports" })).toBeFocused();
  await page.keyboard.type("jupyter");
  await expect(page.getByRole("list", { name: "Forwarded ports" }).getByRole("listitem")).toHaveCount(1);
  await page.keyboard.press("Escape");
  await page.locator("main").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("?");
  await expect(page.getByRole("dialog").getByRole("heading", { name: "Keyboard shortcuts" })).toBeVisible();
});

test("live updates don't move the row under the cursor", async ({ page }) => {
  const d = await open(page);
  await page.keyboard.press("j");
  const current = page.locator('.row[aria-current="true"]');
  await expect(current).toContainText(":5432");
  d.push("forward_changed", forward({ id: "7", local: 3000, remote: 3000, label: "API" }));
  await expect(page.getByText("localhost:3000", { exact: true })).toBeVisible();
  await expect(current).toContainText(":5432");
});

test("a banner when the daemon goes away, and back", async ({ page }) => {
  const d = await open(page);
  await d.ws?.close();
  await expect(page.getByRole("alert").filter({ hasText: "Lost the connection to the pfwd daemon" })).toBeVisible();
  await axe(page, "banner");
  await expect(page.getByText("localhost:5173", { exact: true })).toBeVisible(); // the last state stays on screen
  await page.getByRole("button", { name: "Retry now" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toHaveCount(0);
});

test("a tab without a login says how to get one", async ({ page }) => {
  const d = new Daemon(page);
  d.loggedIn = false;
  await d.install();
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("pfwd ui");
  await axe(page, "logged out");
});

test("the cursor stays on a port the keyboard forwards", async ({ page }) => {
  const d = await open(page);
  const current = page.locator('.row[aria-current="true"]');
  await page.keyboard.press("G");
  await page.keyboard.press("k");
  await expect(current).toContainText("redis-server");
  await page.keyboard.press("Enter");
  await expect.poll(() => d.last("POST", "/api/forwards")?.body).toMatchObject({ spec: { remote: 6379 } });
  d.push("forward_changed", forward({ id: "9", local: 6379, remote: 6379, label: "Redis", auto: false, scheme: "none" }));
  await expect(current).toContainText("localhost:6379");
  await page.keyboard.press("x");
  await expect.poll(() => d.last("DELETE", "/api/forwards/9")).toBeTruthy();
  expect(d.last("DELETE", "/api/forwards/1")).toBeUndefined();
});

test("focus inside a row selects it", async ({ page }) => {
  const d = await open(page);
  await page.getByRole("button", { name: "More actions for :8888" }).focus();
  await page.keyboard.press("x");
  await expect.poll(() => d.last("DELETE", "/api/forwards/2")).toBeTruthy();
  expect(d.last("DELETE", "/api/forwards/1")).toBeUndefined();
});

test("Open only for web servers, at localhost", async ({ page }) => {
  const d = await open(page);
  await page.evaluate(() => {
    const w = window as unknown as { opened: string[] };
    w.opened = [];
    window.open = (u?: string | URL) => (w.opened.push(String(u)), null);
  });
  const postgres = page.getByRole("list", { name: "Forwarded ports" }).getByRole("listitem").filter({ hasText: "localhost:15432" });
  await expect(postgres.getByRole("button", { name: "Open" })).toHaveCount(0);
  await expect(postgres.getByRole("button", { name: "Copy address" })).toBeVisible();
  d.push("forward_changed", forward({ id: "8", bind: "0.0.0.0", local: 8080, remote: 8080, label: "", auto: false }));
  const lan = page.getByRole("list", { name: "Forwarded ports" }).getByRole("listitem").filter({ hasText: "0.0.0.0:8080" });
  await lan.getByRole("button", { name: "Open" }).click();
  await expect.poll(() => page.evaluate(() => (window as unknown as { opened: string[] }).opened)).toEqual(["http://localhost:8080"]);
});

test("a reconnect keeps the log and doesn't replay old news", async ({ page }) => {
  const d = await open(page);
  await expect(page.locator(".toast", { hasText: "Forwarded :5173" })).toHaveCount(0);
  d.push("forward_changed", forward({ state: "paused", pause: "port closed" }));
  await expect(page.locator(".toast")).toHaveCount(0);
  await d.ws?.close();
  await page.getByRole("button", { name: "Retry now" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toHaveCount(0);
  await page.goto("/#/events");
  const log = page.getByRole("log");
  await expect(log.getByText("Paused :5173: port closed")).toHaveCount(1);
  await expect(log.getByText(/Forwarded :5173/)).toHaveCount(0);
  await expect(page.locator(".toast")).toHaveCount(0);
});

test("reverse and SOCKS", async ({ page }) => {
  const d = await open(page, "#/host/devbox/reverse");
  await page.getByLabel("Local port").fill("9000");
  await page.getByRole("button", { name: "Expose" }).click();
  await expect.poll(() => d.last("POST", "/api/forwards")?.body).toEqual({ host: "devbox", spec: { kind: "remote", local: 9000, remote_host: "localhost", remote: 9000 } });
  await page.getByRole("link", { name: "SOCKS" }).click();
  await page.getByRole("button", { name: "Start the proxy" }).click();
  await expect.poll(() => d.last("POST", "/api/forwards")?.body).toEqual({ host: "devbox", spec: { kind: "dynamic", local: 1080 } });
});

test("settings: the form and its problems", async ({ page }) => {
  const d = await open(page, "#/settings");
  await expect(page.getByText("/home/dev/.config/pfwd/config.toml")).toBeVisible();
  await page.getByLabel("Only auto-forward these ports").fill("3000-3999, 99999");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByText("99999 is not a port from 1 to 65535")).toBeVisible();
  await page.getByLabel("Only auto-forward these ports").fill("3000-3999, 5173");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect.poll(() => d.last("PATCH", "/api/config")?.body).toEqual({ values: { "forward.auto_ports": ["3000-3999", 5173] } });
  await expect(page.getByText("The daemon uses the new settings after a restart", { exact: false })).toBeVisible();
});

test("connect a host from ssh config", async ({ page }) => {
  const d = await open(page, "#/");
  await page.getByRole("button", { name: "Connect a host" }).first().click();
  await page.getByRole("searchbox").fill("na");
  await page.getByRole("button", { name: /^nas/ }).click();
  await expect.poll(() => d.last("POST", "/api/hosts/nas/connect")).toBeTruthy();
});

test("first paint under a second", async ({ page }) => {
  await open(page);
  const fcp = await page.evaluate(() => performance.getEntriesByName("first-contentful-paint")[0]?.startTime ?? 0);
  expect(fcp).toBeGreaterThan(0);
  expect(fcp).toBeLessThan(1000);
});

for (const scheme of ["light", "dark"] as const) {
  for (const width of [1280, 390]) {
    test(`screens and accessibility, ${scheme}, ${width} px`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      await page.setViewportSize({ width, height: width > 600 ? 800 : 844 });
      const shot = async (name: string) => {
        await page.screenshot({ path: `${test.info().project.outputDir}/screens/${name}-${scheme}-${width}.png`, fullPage: true });
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        expect(overflow, `${name} scrolls sideways`).toBeLessThanOrEqual(0);
        await axe(page, name);
      };
      const d = await open(page);
      d.push("forward_changed", forward({ id: "4", local: 8080, remote: 8080, state: "failed", error: "server disallows TCP forwarding", label: "", auto: false }));
      await expect(page.getByText("failed").first()).toBeVisible();
      await shot("ports");
      await page.getByRole("button", { name: "More actions for :5173" }).click();
      await page.getByRole("menuitem", { name: "Details" }).click();
      await shot("details");
      await page.keyboard.press("Escape");
      await page.goto("/#/host/gpu-node");
      await shot("reconnecting");
      d.push("prompt", { id: "1", session: "gpu-node", host: "gpu-node", kind: "host_key", key_type: "ssh-ed25519", fingerprint: "SHA256:2k3Yq6l9cR4m5xN0vB8wZtE1aF7gH3jK9pL2qS4uV6w", saves: true });
      await expect(page.getByRole("dialog")).toBeVisible();
      await shot("prompt");
      await page.getByRole("button", { name: "Cancel" }).click();
      await page.goto("/#/host/devbox/reverse");
      await shot("reverse");
      await page.goto("/#/host/devbox/socks");
      await shot("socks");
      await page.goto("/#/events");
      await shot("events");
      await page.goto("/#/settings");
      await expect(page.getByLabel("Only auto-forward these ports")).toBeVisible();
      await shot("settings");
      await page.getByRole("link", { name: "config.toml" }).click();
      await shot("settings-file");
    });
  }
}
