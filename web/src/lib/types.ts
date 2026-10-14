// The daemon's JSON, as the API and the event stream send it. The shapes
// are pfwd ls --json's and pfwd daemon's; keys are snake_case.

export type ForwardKind = "local" | "remote" | "dynamic";

export interface Forward {
  id: string;
  host: string;
  kind: ForwardKind;
  bind: string;
  local: number;
  remote_host: string;
  remote: number;
  state: string; // starting, active, paused, failed, stopped
  pause: string;
  error: string;
  pinned: boolean;
  auto: boolean;
  config: boolean;
  label: string;
  scheme: string; // http, https or ""
  mapped_from: number;
  conns: number;
  bytes_in: number;
  bytes_out: number;
  started_at: string;
}

export interface Port {
  port: number;
  addrs: string[];
  process: string;
  pid: number;
  label: string;
  ignored: boolean;
}

export interface Session {
  host: string;
  state: string; // connecting, connected, reconnecting, failed, closed
  error: string;
  retry_at: string | null;
  attempt: number;
}

export interface PortsInfo {
  host: string;
  strategy: string;
  os: string;
  ports: Port[] | null;
  added: number[] | null;
  removed: number[] | null;
  error: string;
}

export interface Prompt {
  id: string;
  session: string;
  host: string;
  kind: "host_key" | "passphrase" | "password" | "code";
  key_type?: string;
  fingerprint?: string;
  saves?: boolean;
  prompt?: string;
  instruction?: string;
  echo?: boolean;
  retry?: boolean;
}

export interface Stat {
  id: string;
  conns: number;
  bytes_in: number;
  bytes_out: number;
}

export interface HostInfo {
  host: string;
  state: string;
  last_used: string | null;
  remembered: number;
  forwards: number;
}

export interface Spec {
  kind: ForwardKind;
  bind?: string;
  local?: number;
  remote_host?: string;
  remote?: number;
  label?: string;
  pinned?: boolean;
}

export interface WireEvent {
  event: string;
  data: unknown;
}

export interface Problem {
  line: number;
  col: number;
  key: string;
  message: string;
}
