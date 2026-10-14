import { describe as group, expect, it } from "vitest";

import { formatRate, localAddr, target } from "./format";
import { apply, emptyModel, forwardsOf, historyPoints, startSync } from "./reduce";
import type { Forward } from "./types";

function fwd(over: Partial<Forward>): Forward {
  return {
    id: "1", host: "devbox", kind: "local", bind: "127.0.0.1", local: 5173, remote_host: "127.0.0.1", remote: 5173,
    state: "active", pause: "", error: "", pinned: false, auto: false, config: false, label: "", scheme: "http",
    mapped_from: 0, conns: 0, bytes_in: 0, bytes_out: 0, started_at: "2026-10-15T00:00:00Z", ...over,
  };
}

group("apply", () => {
  it("tracks sessions and logs their changes once", () => {
    const m = emptyModel();
    apply(m, { event: "session_changed", data: { host: "devbox", state: "connected", error: "", retry_at: null, attempt: 0 } }, 1);
    apply(m, { event: "session_changed", data: { host: "devbox", state: "connected", error: "", retry_at: null, attempt: 0 } }, 2);
    apply(m, { event: "session_changed", data: { host: "devbox", state: "reconnecting", error: "broken pipe", retry_at: null, attempt: 1 } }, 3);
    apply(m, { event: "session_changed", data: { host: "devbox", state: "reconnecting", error: "broken pipe", retry_at: null, attempt: 2 } }, 4);
    expect(m.sessions.devbox.state).toBe("reconnecting");
    expect(m.log.map((l) => l.text)).toEqual(["Connected to devbox", "Lost the connection to devbox; reconnecting"]);
  });

  it("keeps a forward's life and drops it when stopped", () => {
    const m = emptyModel();
    apply(m, { event: "forward_changed", data: fwd({ state: "starting" }) }, 1);
    apply(m, { event: "forward_changed", data: fwd({ state: "active", auto: true }) }, 2);
    expect(m.notices.map((n) => n.text)).toEqual(["Forwarded :5173 to localhost:5173 (auto)"]);
    apply(m, { event: "forward_changed", data: fwd({ state: "paused", pause: "port closed" }) }, 3);
    apply(m, { event: "forward_changed", data: fwd({ state: "active" }) }, 4);
    expect(m.history["1"].map((h) => h.text)).toEqual(["active", "paused: port closed", "active", "starting"]);
    expect(m.log.at(-1)?.text).toBe("Resumed :5173");
    apply(m, { event: "forward_changed", data: fwd({ state: "stopped" }) }, 5);
    expect(m.forwards["1"]).toBeUndefined();
    expect(m.log.at(-1)?.text).toBe("Stopped :5173");
  });

  it("notices failures", () => {
    const m = emptyModel();
    apply(m, { event: "forward_changed", data: fwd({ state: "failed", error: "server disallows TCP forwarding" }) }, 1);
    apply(m, { event: "auto_forward_failed", data: { host: "devbox", port: 8080, error: "port in use" } }, 2);
    expect(m.notices.map((n) => n.level)).toEqual(["error", "error"]);
    expect(m.notices[1].text).toBe("Couldn't auto-forward :8080: port in use");
  });

  it("queues prompts and drops answered ones", () => {
    const m = emptyModel();
    apply(m, { event: "prompt", data: { id: "1", session: "devbox", host: "devbox", kind: "host_key" } }, 1);
    apply(m, { event: "prompt", data: { id: "2", session: "gpu", host: "gpu", kind: "password" } }, 2);
    apply(m, { event: "prompt", data: { id: "1", session: "devbox", host: "devbox", kind: "host_key" } }, 3);
    expect(m.prompts.map((p) => p.id)).toEqual(["1", "2"]);
    apply(m, { event: "prompt_done", data: { id: "1" } }, 4);
    expect(m.prompts.map((p) => p.id)).toEqual(["2"]);
  });

  it("turns counters into rates and a bounded history", () => {
    const m = emptyModel();
    apply(m, { event: "forward_changed", data: fwd({}) }, 0);
    apply(m, { event: "stats", data: { forwards: [{ id: "1", conns: 1, bytes_in: 1000, bytes_out: 10 }] } }, 1000);
    apply(m, { event: "stats", data: { forwards: [{ id: "1", conns: 2, bytes_in: 3048, bytes_out: 20 }] } }, 2000);
    expect(m.rates["1"].in).toBe(2048);
    expect(m.rates["1"].out).toBe(10);
    expect(m.forwards["1"].conns).toBe(2);
    for (let i = 3; i < historyPoints + 20; i++) {
      apply(m, { event: "stats", data: { forwards: [{ id: "1", conns: 2, bytes_in: 3048 + i, bytes_out: 20 }] } }, i * 1000);
    }
    expect(m.rates["1"].history.length).toBe(historyPoints);
  });

  it("lists a host's forwards by port", () => {
    const m = emptyModel();
    apply(m, { event: "forward_changed", data: fwd({ id: "2", remote: 8888, local: 8888 }) }, 0);
    apply(m, { event: "forward_changed", data: fwd({ id: "3", remote: 22, host: "gpu" }) }, 0);
    apply(m, { event: "forward_changed", data: fwd({ id: "1" }) }, 0);
    expect(forwardsOf(m, "devbox").map((f) => f.id)).toEqual(["1", "2"]);
  });
});

group("format", () => {
  it("formats rates like the TUI", () => {
    expect([512, 1024, 12 * 1024, 1.5 * 1024 * 1024].map(formatRate)).toEqual(["512", "1.0K", "12K", "1.5M"]);
  });
  it("names local addresses", () => {
    expect(localAddr(fwd({}))).toBe("localhost:5173");
    expect(localAddr(fwd({ bind: "0.0.0.0", local: 8080 }))).toBe("0.0.0.0:8080");
    expect(target(fwd({ scheme: "https", local: 8443 }))).toEqual({ text: "https://localhost:8443", web: true });
    expect(target(fwd({ scheme: "none", local: 15432 }))).toEqual({ text: "localhost:15432", web: false });
    expect(target(fwd({ bind: "0.0.0.0", local: 8080 }))).toEqual({ text: "http://localhost:8080", web: true });
    expect(target(fwd({ kind: "dynamic", local: 1080 }))).toEqual({ text: "socks5h://localhost:1080", web: false });
  });
});

group("snapshots", () => {
  it("takes the first snapshot quietly", () => {
    const m = emptyModel();
    startSync(m);
    apply(m, { event: "session_changed", data: { host: "devbox", state: "connected", error: "", retry_at: null, attempt: 0 } }, 1);
    apply(m, { event: "forward_changed", data: fwd({ state: "active", auto: true }) }, 1);
    apply(m, { event: "forward_changed", data: fwd({ id: "2", state: "failed", error: "refused" }) }, 1);
    apply(m, { event: "synced", data: null }, 1);
    expect(m.log).toEqual([]);
    expect(m.notices).toEqual([]);
    expect(Object.keys(m.forwards)).toEqual(["1", "2"]);
    expect(m.history["1"].map((h) => h.text)).toEqual(["active"]);
    apply(m, { event: "forward_changed", data: fwd({ state: "paused", pause: "port closed" }) }, 2);
    expect(m.log.map((l) => l.text)).toEqual(["Paused :5173: port closed"]);
  });

  it("keeps the log over a reconnect and drops what ended meanwhile", () => {
    const m = emptyModel();
    apply(m, { event: "forward_changed", data: fwd({ state: "active", auto: true }) }, 1);
    apply(m, { event: "forward_changed", data: fwd({ id: "2", remote: 8888, local: 8888 }) }, 1);
    apply(m, { event: "prompt", data: { id: "7", session: "devbox", host: "devbox", kind: "password" } }, 1);
    const logged = m.log.length;
    const noticed = m.notices.length;
    startSync(m);
    apply(m, { event: "forward_changed", data: fwd({ state: "active", auto: true }) }, 2);
    apply(m, { event: "synced", data: null }, 2);
    expect(Object.keys(m.forwards)).toEqual(["1"]);
    expect(m.prompts).toEqual([]);
    expect(m.log.length).toBe(logged);
    expect(m.notices.length).toBe(noticed);
  });
});
