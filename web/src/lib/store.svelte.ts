// The app's state: the model from the event stream, the connection to the
// daemon, the current view and the actions every view shares.

import * as api from "./api";
import { apply, emptyModel, startSync, type LogLine, type Model } from "./reduce";
import type { Forward, HostInfo, Spec } from "./types";

export type View =
  | { kind: "host"; host: string; tab: "ports" | "reverse" | "socks" }
  | { kind: "events" }
  | { kind: "settings" }
  | { kind: "home" };

export interface Toast {
  id: number;
  level: LogLine["level"];
  text: string;
}

// parseHash reads the view from the address: #/host/devbox/reverse,
// #/events, #/settings.
export function parseHash(hash: string): View {
  let parts: string[];
  try {
    parts = hash.replace(/^#\/?/, "").split("/").map(decodeURIComponent);
  } catch {
    return { kind: "home" }; // a malformed escape
  }
  switch (parts[0]) {
    case "host":
      if (parts[1]) {
        const tab = parts[2] === "reverse" || parts[2] === "socks" ? parts[2] : "ports";
        return { kind: "host", host: parts[1], tab };
      }
      break;
    case "events":
      return { kind: "events" };
    case "settings":
      return { kind: "settings" };
  }
  return { kind: "home" };
}

export function viewHash(v: View): string {
  switch (v.kind) {
    case "host":
      return `#/host/${encodeURIComponent(v.host)}${v.tab === "ports" ? "" : "/" + v.tab}`;
    case "events":
      return "#/events";
    case "settings":
      return "#/settings";
  }
  return "#/";
}

const notifyKey = "pfwd-notify";

class App {
  model = $state<Model>(emptyModel());
  // link is the event stream: up, retrying (with the time of the next
  // try), or ended because this tab's login is gone.
  link = $state<"connecting" | "up" | "retrying" | "login">("connecting");
  retryAt = $state(0);
  version = $state("");
  view = $state<View>({ kind: "home" });
  hosts = $state<HostInfo[]>([]);
  // hostsWarning says why the ssh config's hosts are missing, if they are.
  hostsWarning = $state("");
  toasts = $state<Toast[]>([]);
  now = $state(Date.now());
  // notify is the opt-in for browser notifications.
  notify = $state(false);

  #ws: WebSocket | null = null;
  #attempt = 0;
  #retryTimer = 0;
  #toastId = 0;
  #noticesSeen = 0;

  // ready settles once this tab has tried to log in; views that call the
  // API when they open wait for it, or their first call has no token.
  #loggedIn: () => void = () => {};
  ready = new Promise<void>((resolve) => (this.#loggedIn = resolve));

  // start logs in and opens the event stream.
  async start() {
    this.view = parseHash(location.hash);
    addEventListener("hashchange", () => (this.view = parseHash(location.hash)));
    setInterval(() => (this.now = Date.now()), 1000);
    try {
      this.notify = localStorage.getItem(notifyKey) === "1" && "Notification" in window && Notification.permission === "granted";
    } catch {
      this.notify = false;
    }
    try {
      const info = await api.session();
      this.version = info.version;
    } catch (e) {
      if (api.loginNeeded(e)) {
        this.link = "login";
        return;
      }
    } finally {
      this.#loggedIn();
    }
    this.connect();
    void this.loadHosts();
  }

  go(v: View) {
    location.hash = viewHash(v);
    this.view = v;
  }

  // connect opens the event stream; a lost stream retries with backoff.
  connect() {
    clearTimeout(this.#retryTimer);
    this.#ws?.close();
    const ws = new WebSocket(api.eventsURL());
    this.#ws = ws;
    ws.onopen = () => {
      // The daemon starts with what it knows now, up to a synced event;
      // the model keeps the log and history it has.
      startSync(this.model);
      this.link = "up";
      this.#attempt = 0;
    };
    ws.onmessage = (msg) => {
      try {
        apply(this.model, JSON.parse(String(msg.data)), Date.now());
      } catch {
        return;
      }
      this.#showNotices();
    };
    ws.onclose = () => {
      if (this.#ws !== ws) return;
      this.#ws = null;
      void this.#retry();
    };
  }

  async #retry() {
    // A stream that won't open may be a login that ended (the daemon
    // restarted): ask before trying again.
    try {
      await api.get("/api/session");
    } catch (e) {
      if (api.loginNeeded(e)) {
        this.link = "login";
        return;
      }
    }
    this.#attempt++;
    const wait = Math.min(10, 2 ** Math.min(this.#attempt - 1, 4)) * 1000;
    this.link = "retrying";
    this.retryAt = Date.now() + wait;
    this.#retryTimer = window.setTimeout(() => this.connect(), wait);
  }

  retryNow() {
    this.#attempt = 0;
    this.connect();
  }

  #showNotices() {
    const n = this.model.notices;
    while (this.#noticesSeen < n.length) {
      const line = n[this.#noticesSeen++];
      this.toast(line.level, line.text);
      if (this.notify && document.hidden) {
        try {
          new Notification("pfwd", { body: line.text });
        } catch {
          // Notifications can be blocked at any time.
        }
      }
    }
  }

  toast(level: Toast["level"], text: string) {
    const id = ++this.#toastId;
    this.toasts.push({ id, level, text });
    if (this.toasts.length > 4) this.toasts.splice(0, this.toasts.length - 4);
    setTimeout(() => (this.toasts = this.toasts.filter((t) => t.id !== id)), 5000);
  }

  async setNotify(on: boolean) {
    if (on && "Notification" in window && Notification.permission !== "granted") {
      on = (await Notification.requestPermission()) === "granted";
    }
    this.notify = on;
    try {
      localStorage.setItem(notifyKey, on ? "1" : "0");
    } catch {
      // Kept for this page only.
    }
  }

  async loadHosts() {
    try {
      const res = await api.get<{ hosts: HostInfo[]; warning?: string }>("/api/hosts");
      this.hosts = res.hosts;
      this.hostsWarning = res.warning ?? "";
    } catch (e) {
      this.fail(e);
    }
  }

  // fail shows an action's error as a toast.
  fail(e: unknown) {
    if (api.loginNeeded(e)) {
      this.link = "login";
      return;
    }
    this.toast("error", e instanceof Error ? e.message : String(e));
  }

  // run performs an action and reports its error.
  async run<T>(action: () => Promise<T>): Promise<T | undefined> {
    try {
      return await action();
    } catch (e) {
      this.fail(e);
      return undefined;
    }
  }

  connectHost(host: string) {
    return this.run(async () => {
      const res = await api.post<{ host: string }>(`${api.hostPath(host)}/connect`);
      this.go({ kind: "host", host: res.host, tab: "ports" });
      void this.loadHosts();
    });
  }

  disconnect(host: string) {
    return this.run(() => api.post(`${api.hostPath(host)}/disconnect`));
  }

  reconnect(host: string) {
    return this.run(() => api.post(`${api.hostPath(host)}/reconnect`));
  }

  setAuto(host: string, on: boolean) {
    return this.run(() => api.patch(`${api.hostPath(host)}/settings`, { auto: on }));
  }

  forward(host: string, spec: Spec) {
    return this.run(() => api.post<Forward>("/api/forwards", { host, spec }));
  }

  stop(f: Forward) {
    return this.run(() => api.del(`/api/forwards/${encodeURIComponent(f.id)}`));
  }

  pin(f: Forward, pinned: boolean) {
    return this.run(() => api.patch(`/api/forwards/${encodeURIComponent(f.id)}`, { pinned }));
  }

  relabel(f: Forward, label: string) {
    return this.run(() => api.patch(`/api/forwards/${encodeURIComponent(f.id)}`, { label }));
  }

  async answer(id: string, body: { accept?: boolean; secret?: string; cancel?: boolean }) {
    // The prompt leaves the queue at once; prompt_done confirms it. When the
    // answer doesn't arrive, it comes back, unless another client answered.
    const prompt = this.model.prompts.find((p) => p.id === id);
    this.model.prompts = this.model.prompts.filter((p) => p.id !== id);
    try {
      await api.post(`/api/prompts/${encodeURIComponent(id)}`, body);
    } catch (e) {
      const gone = e instanceof api.ApiError && e.status === 404;
      if (prompt && !gone && !this.model.prompts.some((p) => p.id === id)) this.model.prompts.unshift(prompt);
      this.fail(e);
    }
  }

  async copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      this.toast("ok", `Copied ${text}`);
    } catch {
      this.toast("error", "The browser didn't allow copying; select the address and copy it yourself.");
    }
  }
}

export const app = new App();
