// Calls to the daemon's web API. The session's token, which the page gets
// once after login, goes on every call; it lives in sessionStorage, so it
// stays with this tab and this port.

const tokenKey = "pfwd-token";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

// LoginNeeded means this tab has no working login: run pfwd ui again.
export function loginNeeded(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 401 || e.code === "login");
}

function stored(): string {
  try {
    return sessionStorage.getItem(tokenKey) ?? "";
  } catch {
    return "";
  }
}

let token = "";

export function currentToken(): string {
  return token || stored();
}

async function send(method: string, path: string, body?: unknown): Promise<Response> {
  const headers: Record<string, string> = { "X-PFWD-Token": currentToken() };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  try {
    return await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: "same-origin" });
  } catch {
    throw new ApiError(0, "offline", "The pfwd daemon doesn't answer. Is it still running? pfwd daemon status shows it.");
  }
}

async function result<T>(resp: Response): Promise<T> {
  const text = await resp.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    // Not JSON: a plain-text error from the server.
  }
  if (!resp.ok) {
    const err = (data as { error?: { code: string; message: string } })?.error;
    throw new ApiError(resp.status, err?.code ?? "internal", err?.message ?? (text.trim() || `HTTP ${resp.status}`));
  }
  return data as T;
}

// session logs this tab in: it takes the token the first time, and checks
// the stored one after a reload.
export async function session(): Promise<{ version: string }> {
  const info = await result<{ version: string; token?: string }>(await send("GET", "/api/session"));
  if (info.token) {
    token = info.token;
    try {
      sessionStorage.setItem(tokenKey, token);
    } catch {
      // Private mode without storage: the token lives in memory.
    }
  }
  return info;
}

export async function get<T>(path: string): Promise<T> {
  return result<T>(await send("GET", path));
}

export async function post<T>(path: string, body?: unknown): Promise<T> {
  return result<T>(await send("POST", path, body ?? {}));
}

export async function patch<T>(path: string, body: unknown): Promise<T> {
  return result<T>(await send("PATCH", path, body));
}

export async function put<T>(path: string, body: unknown): Promise<T> {
  return result<T>(await send("PUT", path, body));
}

export async function del<T>(path: string): Promise<T> {
  return result<T>(await send("DELETE", path));
}

// hostPath is the API path of a host.
export function hostPath(host: string): string {
  return `/api/hosts/${encodeURIComponent(host)}`;
}

// eventsURL is the event stream's address.
export function eventsURL(): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/api/events?token=${encodeURIComponent(currentToken())}`;
}
