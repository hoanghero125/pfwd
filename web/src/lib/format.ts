// Formatting shared by the views, with the TUI's words.

import type { Forward, Spec } from "./types";

// formatRate formats bytes (per second) with binary units, like the TUI:
// 512, 1.0K, 12K, 1.5M.
export function formatRate(b: number): string {
  if (b < 1024) return String(Math.floor(b));
  const units = ["K", "M", "G", "T"];
  let i = -1;
  while (b >= 1024 && i < units.length - 1) {
    b /= 1024;
    i++;
  }
  return b < 10 ? `${b.toFixed(1)}${units[i]}` : `${Math.round(b)}${units[i]}`;
}

const loopback = new Set(["", "127.0.0.1", "::1", "localhost"]);

// localAddr is where a forward is on this machine: where a local or SOCKS
// forward listens, or where a remote forward delivers.
export function localAddr(f: Pick<Forward, "kind" | "bind" | "remote_host" | "local"> | Spec): string {
  const host = f.kind === "remote" ? (f.remote_host ?? "") : (f.bind ?? "");
  if (loopback.has(host)) return `localhost:${f.local ?? 0}`;
  return host.includes(":") ? `[${host}]:${f.local}` : `${host}:${f.local}`;
}

// remoteAddr is the server side of a forward.
export function remoteAddr(f: Pick<Forward, "remote_host" | "remote">): string {
  const host = f.remote_host;
  if (loopback.has(host)) return `:${f.remote}`;
  return `${host}:${f.remote}`;
}

// url is the address a browser opens for a local forward.
export function url(f: Pick<Forward, "kind" | "bind" | "remote_host" | "local" | "scheme">): string {
  const scheme = f.scheme === "https" ? "https" : "http";
  return `${scheme}://${localAddr(f)}`;
}

// exposed is true for a forward that listens beyond this machine.
export function exposed(f: Pick<Forward, "kind" | "bind">): boolean {
  return f.kind !== "remote" && !loopback.has(f.bind);
}

// describe says what a forward does, as the TUI's status line does.
export function describe(f: Forward): string {
  switch (f.kind) {
    case "remote":
      return `Exposed ${localAddr(f)} on :${f.remote} of ${f.host}`;
    case "dynamic":
      return `SOCKS proxy on ${localAddr(f)}`;
  }
  let text = `Forwarded :${f.remote} to ${localAddr(f)}`;
  if (f.mapped_from) text += ` (${f.mapped_from} needs privileges here; using ${f.local})`;
  if (f.auto) text += " (auto)";
  return text;
}

// name is how a log line names a forward.
export function name(f: Forward): string {
  return f.kind === "dynamic" ? `the SOCKS proxy on ${localAddr(f)}` : `:${f.remote}`;
}

// ago says how long ago t was, in one unit.
export function ago(t: string | null, now: number): string {
  if (!t) return "never";
  const s = Math.max(0, Math.round((now - Date.parse(t)) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// clock is a time of day, 24-hour, like the TUI's log.
export function clock(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// pauseText words a pause reason with the engine's words: port closed,
// disconnected, port busy.
export function pauseText(reason: string): string {
  return reason || "paused";
}
