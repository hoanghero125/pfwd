// The page's model of the daemon, built from its event stream. apply is a
// plain function over a plain object, so tests drive it directly; the app
// keeps the object in Svelte state.

import { cleanAll, describe, name, pauseText } from "./format";
import type { Forward, PortsInfo, Prompt, Session, Stat, WireEvent } from "./types";

export type Level = "info" | "ok" | "warn" | "error";

export interface LogLine {
  // seq numbers the lines, for keys.
  seq?: number;
  at: number;
  host: string;
  level: Level;
  text: string;
}

// Rate is one forward's traffic: bytes per second now, and one point a
// second for the last five minutes.
// Sample is a second's traffic, in bytes per second each way.
export interface Sample {
  in: number;
  out: number;
}

export interface Rate {
  in: number;
  out: number;
  lastIn: number;
  lastOut: number;
  lastAt: number;
  history: Sample[];
}

export interface Model {
  sessions: Record<string, Session>;
  ports: Record<string, PortsInfo>;
  forwards: Record<string, Forward>;
  auto: Record<string, boolean>;
  prompts: Prompt[];
  log: LogLine[];
  rates: Record<string, Rate>;
  // history is each forward's state changes, newest first.
  history: Record<string, { at: number; text: string }[]>;
  // notices are what a toast shows: auto-forwards and failures.
  notices: LogLine[];
  // syncing is true while the daemon replays what it knows, from the
  // stream's start to its synced event: that is not news, so it isn't
  // logged. seen keeps what the replay named, so what ended while the
  // page was away can go.
  syncing: boolean;
  seen: { forwards: Record<string, true>; prompts: Record<string, true> };
  // seq is the last log line's number.
  seq: number;
}

export const maxLog = 500;
export const historyPoints = 300;

export function emptyModel(): Model {
  return {
    sessions: {}, ports: {}, forwards: {}, auto: {}, prompts: [], log: [], rates: {}, history: {}, notices: [],
    syncing: false, seen: { forwards: {}, prompts: {} }, seq: 0,
  };
}

// startSync begins a replay: the event stream (re)opened.
export function startSync(m: Model) {
  m.syncing = true;
  m.seen = { forwards: {}, prompts: {} };
}

function log(m: Model, line: LogLine, notice = false) {
  if (m.syncing) return;
  line.seq = ++m.seq;
  m.log.push(line);
  if (m.log.length > maxLog) m.log.splice(0, m.log.length - maxLog);
  if (notice) m.notices.push(line);
}

function stateText(f: Forward): string {
  switch (f.state) {
    case "paused":
      return `paused: ${pauseText(f.pause)}`;
    case "failed":
      return f.error ? `failed: ${f.error}` : "failed";
  }
  return f.state;
}

// apply records one event. now is the time it arrived, in milliseconds.
// Every string in it is cleaned first: process names, uname output and
// errors come from the server.
export function apply(m: Model, ev: WireEvent, now: number) {
  ev = { ...ev, data: cleanAll(ev.data) };
  switch (ev.event) {
    case "session_changed": {
      const s = ev.data as Session;
      const before = m.sessions[s.host];
      m.sessions[s.host] = s;
      if (before?.state === s.state && before?.error === s.error) return;
      switch (s.state) {
        case "connected":
          log(m, { at: now, host: s.host, level: "ok", text: `Connected to ${s.host}` });
          break;
        case "reconnecting":
          if (before?.state !== "reconnecting") {
            log(m, { at: now, host: s.host, level: "warn", text: `Lost the connection to ${s.host}; reconnecting` });
          }
          break;
        case "failed":
          log(m, { at: now, host: s.host, level: "error", text: s.error || `The connection to ${s.host} failed` }, true);
          break;
        case "closed":
          log(m, { at: now, host: s.host, level: "info", text: `Disconnected from ${s.host}` });
          break;
      }
      return;
    }
    case "ports_changed": {
      const p = ev.data as PortsInfo;
      m.ports[p.host] = p;
      return;
    }
    case "auto_changed": {
      const d = ev.data as { host: string; on: boolean };
      m.auto[d.host] = d.on;
      return;
    }
    case "forward_changed": {
      const f = ev.data as Forward;
      if (m.syncing) m.seen.forwards[f.id] = true;
      const before = m.forwards[f.id];
      const changed = !before || before.state !== f.state || before.pause !== f.pause || before.error !== f.error;
      if (changed) {
        // Read the list back from the model: in the app the model is a
        // Svelte state proxy, and only changes made through it show.
        if (!m.history[f.id]) m.history[f.id] = [];
        const h = m.history[f.id];
        h.unshift({ at: now, text: stateText(f) + (f.auto && !before && !m.syncing ? " (auto-forward)" : "") });
        if (h.length > 50) h.length = 50;
      }
      if (f.state === "stopped") {
        delete m.forwards[f.id];
        delete m.rates[f.id];
        delete m.history[f.id];
        log(m, { at: now, host: f.host, level: "info", text: `Stopped ${name(f)}` });
        return;
      }
      m.forwards[f.id] = f;
      if (!changed) return;
      switch (f.state) {
        case "active":
          if (before?.state === "paused") {
            log(m, { at: now, host: f.host, level: "ok", text: `Resumed ${name(f)}` });
          } else if (before?.state !== "active") {
            log(m, { at: now, host: f.host, level: "ok", text: describe(f) }, f.auto);
          }
          break;
        case "paused":
          log(m, { at: now, host: f.host, level: "warn", text: `Paused ${name(f)}: ${pauseText(f.pause)}` });
          break;
        case "failed":
          log(m, { at: now, host: f.host, level: "error", text: `${name(f)} failed: ${f.error}` }, true);
          break;
      }
      return;
    }
    case "auto_forward_failed": {
      const d = ev.data as { host: string; port: number; error: string };
      log(m, { at: now, host: d.host, level: "error", text: `Couldn't auto-forward :${d.port}: ${d.error}` }, true);
      return;
    }
    case "restore_failed": {
      const d = ev.data as { host: string; spec: { remote: number }; error: string };
      const what = d.spec?.remote ? `:${d.spec.remote}` : "the remembered forwards";
      log(m, { at: now, host: d.host, level: "error", text: `Couldn't restore ${what}: ${d.error}` }, true);
      return;
    }
    case "prompt": {
      const p = ev.data as Prompt;
      if (m.syncing) m.seen.prompts[p.id] = true;
      if (!m.prompts.some((q) => q.id === p.id)) m.prompts.push(p);
      return;
    }
    case "synced": {
      for (const id of Object.keys(m.forwards)) {
        if (!m.seen.forwards[id]) {
          delete m.forwards[id];
          delete m.rates[id];
          delete m.history[id];
        }
      }
      m.prompts = m.prompts.filter((p) => m.seen.prompts[p.id]);
      m.syncing = false;
      return;
    }
    case "prompt_done": {
      const d = ev.data as { id: string };
      m.prompts = m.prompts.filter((q) => q.id !== d.id);
      return;
    }
    case "stats": {
      const d = ev.data as { forwards: Stat[] };
      for (const s of d.forwards ?? []) {
        const f = m.forwards[s.id];
        if (f) {
          f.conns = s.conns;
          f.bytes_in = s.bytes_in;
          f.bytes_out = s.bytes_out;
        }
        rate(m, s, now);
      }
      return;
    }
  }
}

// rate updates a forward's rate from its counters.
function rate(m: Model, s: Stat, now: number) {
  const r = m.rates[s.id];
  if (!r) {
    m.rates[s.id] = { in: 0, out: 0, lastIn: s.bytes_in, lastOut: s.bytes_out, lastAt: now, history: [] };
    return;
  }
  const secs = (now - r.lastAt) / 1000;
  if (secs <= 0) return;
  r.in = Math.max(0, (s.bytes_in - r.lastIn) / secs);
  r.out = Math.max(0, (s.bytes_out - r.lastOut) / secs);
  r.lastIn = s.bytes_in;
  r.lastOut = s.bytes_out;
  r.lastAt = now;
  r.history.push({ in: r.in, out: r.out });
  if (r.history.length > historyPoints) r.history.splice(0, r.history.length - historyPoints);
}

// trafficOf is host's traffic, a sample a second, oldest first: its
// forwards' rates added up, lined up at the newest second.
export function trafficOf(m: Model, host: string): Sample[] {
  const all = forwardsOf(m, host).map((f) => m.rates[f.id]?.history ?? []);
  const n = Math.max(0, ...all.map((h) => h.length));
  const sum = Array.from({ length: n }, (): Sample => ({ in: 0, out: 0 }));
  for (const h of all) {
    h.forEach((s, i) => {
      const t = sum[n - h.length + i];
      t.in += s.in;
      t.out += s.out;
    });
  }
  return sum;
}

// forwardsOf returns host's forwards of kind, by remote port.
export function forwardsOf(m: Model, host: string, kind?: Forward["kind"]): Forward[] {
  return Object.values(m.forwards)
    .filter((f) => f.host === host && (!kind || f.kind === kind))
    .sort((a, b) => a.remote - b.remote || Number(a.id) - Number(b.id));
}
