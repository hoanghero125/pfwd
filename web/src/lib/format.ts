// Formatting shared by the views, with the TUI's words.

import type { Forward, Spec } from "./types";

// clean makes text from the server safe to show, like the TUI's
// safetext.Clean: control characters and bidi overrides become U+FFFD, and
// tabs and line breaks become spaces.
export function clean(s: string): string {
  return s.replace(/[\t\n\r]/g, " ").replace(/[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/g, "\ufffd");
}

// cleanAll returns v with every string in it cleaned.
export function cleanAll<T>(v: T): T {
  if (typeof v === "string") return clean(v) as T;
  if (Array.isArray(v)) return v.map(cleanAll) as T;
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, cleanAll(x)])) as T;
  return v;
}

// formatRate formats bytes (per second) with binary units in at most four
// characters, like the TUI: 512, 1.0K, 12K, 1.5M. A value that would round
// to 1000 or more of a unit moves up to the next one.
export function formatRate(b: number): string {
  if (b < 1024) return String(Math.floor(b));
  const units = ["K", "M", "G", "T"];
  b /= 1024;
  let i = 0;
  while (b >= 999.5 && i < units.length - 1) {
    b /= 1024;
    i++;
  }
  return b < 9.95 ? `${b.toFixed(1)}${units[i]}` : `${Math.round(b)}${units[i]}`;
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

// target is what Copy URL copies and Open opens, as in the TUI: a web
// address when the forward may serve HTTP (web is true), else the bare
// address. A forward listening on every address is reached at localhost.
export function target(f: Pick<Forward, "kind" | "bind" | "remote_host" | "local" | "scheme">): { text: string; web: boolean } {
  const b = f.bind ?? "";
  const host = loopback.has(b) || b === "0.0.0.0" || b === "::" ? "localhost" : b;
  const addr = host.includes(":") ? `[${host}]:${f.local}` : `${host}:${f.local}`;
  switch (f.kind) {
    case "dynamic":
      return { text: `socks5h://${addr}`, web: false };
    case "remote":
      return { text: localAddr(f), web: false };
  }
  switch (f.scheme) {
    case "none":
      return { text: addr, web: false };
    case "https":
      return { text: `https://${addr}`, web: true };
  }
  return { text: `http://${addr}`, web: true };
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

// portTarget is the server address a detected port is reached at, as the
// TUI picks it: loopback when the port listens on every address or on
// loopback, else the one address it listens on.
export function portTarget(addrs: string[]): string {
  if (addrs.some((a) => a === "0.0.0.0" || a === "*" || a === "127.0.0.1")) return "127.0.0.1";
  if (addrs.includes("::1") || addrs.includes("::")) return "::1";
  return addrs[0] ?? "127.0.0.1";
}

// portLabel names a detected port: :3000, or 10.1.2.3:9000 when it listens
// on one other address only.
export function portLabel(port: number, addrs: string[]): string {
  const host = portTarget(addrs);
  if (loopback.has(host)) return `:${port}`;
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`;
}
