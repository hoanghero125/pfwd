// A mocked pfwd daemon for Playwright: the web API answered in the browser
// and an event stream the test can push to.

import type { Page, Route, WebSocketRoute } from "@playwright/test";

export interface Call {
  method: string;
  path: string;
  body: unknown;
}

export const forward = (over: Record<string, unknown>) => ({
  id: "1", host: "devbox", kind: "local", bind: "127.0.0.1", local: 5173, remote_host: "127.0.0.1", remote: 5173,
  state: "active", pause: "", error: "", pinned: false, auto: true, config: false, label: "Vite", scheme: "http",
  mapped_from: 0, conns: 2, bytes_in: 3_400_000, bytes_out: 210_000, started_at: "2026-10-15T09:12:04Z", ...over,
});

const port = (port: number, process: string, label = "", ignored = false) => ({ port, addrs: ["127.0.0.1"], process, pid: 100 + port, label, ignored });

// snapshot is what a daemon with two hosts knows.
export function snapshot() {
  return [
    { event: "session_changed", data: { host: "devbox", state: "connected", error: "", retry_at: null, attempt: 0 } },
    { event: "auto_changed", data: { host: "devbox", on: true } },
    {
      event: "ports_changed",
      data: {
        host: "devbox", strategy: "ss", os: "Linux", added: [], removed: [], error: "",
        ports: [port(5173, "node", "Vite"), port(5432, "postgres", "Postgres"), port(6379, "redis-server", "Redis"), port(8888, "jupyter-lab", "Jupyter"), port(9090, "", "Prometheus"), port(22, "sshd", "", true)],
      },
    },
    { event: "session_changed", data: { host: "gpu-node", state: "reconnecting", error: "connection to gpu-node was lost (broken pipe)", retry_at: new Date(Date.now() + 4000).toISOString(), attempt: 1 } },
    { event: "forward_changed", data: forward({}) },
    { event: "forward_changed", data: forward({ id: "2", local: 8888, remote: 8888, label: "Jupyter", auto: false, pinned: true, conns: 0, bytes_in: 0, bytes_out: 0 }) },
    { event: "forward_changed", data: forward({ id: "3", local: 15432, remote: 5432, label: "Postgres", state: "paused", pause: "port closed", auto: false, scheme: "none" }) },
  ];
}

export class Daemon {
  calls: Call[] = [];
  ws: WebSocketRoute | null = null;
  claimed = false;
  loggedIn = true;

  constructor(readonly page: Page) {}

  async install(events = snapshot()) {
    await this.page.route("**/api/**", (route) => this.answer(route));
    await this.page.routeWebSocket(/\/api\/events/, (ws) => {
      this.ws = ws;
      for (const e of events) ws.send(JSON.stringify(e));
      ws.send(JSON.stringify({ event: "synced" }));
    });
  }

  push(event: string, data: unknown) {
    this.ws?.send(JSON.stringify({ event, data }));
  }

  last(method: string, path: string): Call | undefined {
    return this.calls.filter((c) => c.method === method && c.path === path).at(-1);
  }

  async answer(route: Route) {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    const method = req.method();
    let body: unknown = undefined;
    try {
      body = req.postDataJSON();
    } catch {
      body = req.postData();
    }
    this.calls.push({ method, path, body });
    const json = (data: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(data) });
    if (!this.loggedIn) return json({ error: { code: "login", message: "This page isn't logged in." } }, 401);
    if (path === "/api/session") {
      const first = !this.claimed;
      this.claimed = true;
      return json(first ? { version: "v0.6.0", token: "test-token" } : { version: "v0.6.0" });
    }
    if (path === "/api/hosts" && method === "GET") {
      return json({
        hosts: [
          { host: "devbox", state: "connected", last_used: new Date().toISOString(), remembered: 2, forwards: 3 },
          { host: "gpu-node", state: "reconnecting", last_used: new Date(Date.now() - 2 * 3600e3).toISOString(), remembered: 1, forwards: 0 },
          { host: "nas", state: "", last_used: null, remembered: 0, forwards: 0 },
        ],
      });
    }
    if (path === "/api/config" && method === "GET") {
      return json({
        path: "/home/dev/.config/pfwd/config.toml",
        text: "# pfwd settings\n[forward]\nauto = true\n",
        values: {
          "forward.auto": true, "forward.auto_ports": [], "detect.ignore_ports": [22], "forward.on_conflict": "ask",
          "forward.auto_expire": "10m", "detect.interval": "2s", "ui.notifications": true,
        },
      });
    }
    if (path === "/api/config" && method === "PATCH") {
      const values = (body as { values: Record<string, unknown> }).values;
      if (JSON.stringify(values).includes("99999")) {
        return json({ saved: false, problems: [{ line: 3, col: 14, key: "forward.auto_ports", message: "99999 is not a port from 1 to 65535" }] });
      }
      return json({ saved: true, problems: [], values });
    }
    if (path === "/api/forwards" && method === "POST") {
      const b = body as { host: string; spec: { remote: number; local?: number } };
      return json(forward({ id: "9", host: b.host, remote: b.spec.remote, local: b.spec.local || b.spec.remote, state: "starting", auto: false }));
    }
    const connect = path.match(/^\/api\/hosts\/([^/]+)\/connect$/);
    if (connect && method === "POST") return json({ host: decodeURIComponent(connect[1]) });
    return json({ ok: true });
  }
}
