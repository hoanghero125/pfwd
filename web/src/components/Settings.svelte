<script lang="ts">
  import * as api from "../lib/api";
  import { app } from "../lib/store.svelte";
  import type { Problem } from "../lib/types";
  import Icon from "./Icon.svelte";

  // Settings: a form for the common keys of config.toml, and the file
  // itself. Nothing is written until the whole file is valid.
  type Values = Record<string, unknown>;
  type Saved = { saved: boolean; problems: Problem[]; values?: Values };

  let tab = $state<"form" | "file">("form");
  let path = $state("");
  let text = $state("");
  let values = $state<Values>({});
  let form = $state<Record<string, string | boolean>>({});
  let problems = $state<Problem[]>([]);
  let loaded = $state(false);
  let busy = $state(false);

  const lists = ["forward.auto_ports", "detect.ignore_ports"];
  type Field = { key: string; label: string; hint: string; kind: "bool" | "text" | "list" | "choice"; choices?: [string, string][] };
  const fields: Field[] = [
    { key: "forward.auto", label: "Forward new ports automatically", hint: "Hosts can override it in their [hosts.NAME] table.", kind: "bool" },
    { key: "forward.auto_ports", label: "Only auto-forward these ports", hint: "Ports and ranges, like 3000-3999, 5173. Empty means any port.", kind: "list" },
    { key: "detect.ignore_ports", label: "Never show these ports", hint: "Ports and ranges, like 22, 6000-6100.", kind: "list" },
    {
      key: "forward.on_conflict", label: "When the local port is taken", hint: "", kind: "choice",
      choices: [["ask", "Ask (the next free port without a terminal)"], ["next", "Use the next free port"], ["random", "Use a random port"], ["fail", "Fail"]],
    },
    { key: "forward.auto_expire", label: "Stop unused auto-forwards after", hint: "A duration like 10m or 1h.", kind: "text" },
    { key: "detect.interval", label: "Look for new ports every", hint: "A duration like 2s.", kind: "text" },
    { key: "ui.notifications", label: "Desktop notifications from pfwd", hint: "When a port is auto-forwarded.", kind: "bool" },
  ];
  // The form's panels, by the table their keys are in.
  const panels: { title: string; about: string; table: string }[] = [
    { title: "Forwarding", about: "What pfwd forwards on its own, and what it does when a local port is taken.", table: "forward" },
    { title: "Detection", about: "How pfwd finds listening ports on the server.", table: "detect" },
    { title: "Notifications", about: "When pfwd tells you about new forwards and failures.", table: "ui" },
  ];

  function fill(v: Values) {
    values = v;
    const f: Record<string, string | boolean> = {};
    for (const field of fields) {
      const x = v[field.key];
      f[field.key] = field.kind === "bool" ? Boolean(x) : Array.isArray(x) ? x.join(", ") : String(x ?? "");
    }
    form = f;
  }

  async function load() {
    try {
      const c = await api.get<{ path: string; text: string; values: Values }>("/api/config");
      path = c.path;
      text = c.text;
      fill(c.values);
      loaded = true;
    } catch (e) {
      app.fail(e);
    }
  }
  void app.ready.then(load);

  function parseList(s: string): unknown[] {
    return s
      .split(/[\s,]+/)
      .filter(Boolean)
      .map((x) => (/^\d+$/.test(x) ? Number(x) : x));
  }

  function changed(): Values {
    const out: Values = {};
    for (const field of fields) {
      const now = form[field.key];
      const value = lists.includes(field.key) ? parseList(String(now)) : now;
      if (JSON.stringify(value) !== JSON.stringify(values[field.key])) out[field.key] = value;
    }
    return out;
  }

  function done(r: Saved) {
    problems = r.problems;
    if (r.saved) {
      app.toast("ok", "Saved. The daemon uses the new settings after a restart: pfwd daemon stop, then pfwd ui.");
      void load();
    }
  }

  async function saveForm(e: SubmitEvent) {
    e.preventDefault();
    const diff = changed();
    if (!Object.keys(diff).length) {
      app.toast("info", "Nothing changed.");
      return;
    }
    busy = true;
    const r = await app.run(() => api.patch<Saved>("/api/config", { values: diff }));
    busy = false;
    if (r) done(r);
  }

  async function saveFile(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    const r = await app.run(() => api.put<Saved>("/api/config", { text }));
    busy = false;
    if (r) done(r);
  }

  function problemFor(key: string): string {
    return problems.find((p) => p.key === key)?.message ?? "";
  }
</script>

{#snippet input(field: Field)}
  {@const err = problemFor(field.key)}
  <div class="field">
    {#if field.kind === "bool"}
      <label class="check"><input type="checkbox" bind:checked={form[field.key] as boolean} aria-describedby="hint-{field.key}" /> {field.label}</label>
    {:else if field.kind === "choice"}
      <label for="set-{field.key}">{field.label}</label>
      <select id="set-{field.key}" bind:value={form[field.key]}>
        {#each field.choices ?? [] as [value, text] (value)}<option {value}>{text}</option>{/each}
      </select>
    {:else}
      <label for="set-{field.key}">{field.label}</label>
      <input id="set-{field.key}" type="text" bind:value={form[field.key]} aria-invalid={err ? "true" : undefined} aria-describedby="hint-{field.key}" />
    {/if}
    {#if err}<span class="err" role="alert">{err}</span>{/if}
    <span class="hint" class:indent={field.kind === "bool"} id="hint-{field.key}">{field.hint} <span class="mono">{field.key}</span></span>
  </div>
{/snippet}

<div class="head">
  <h1>Settings</h1>
</div>
{#if path}<p class="meta"><span><Icon name="settings" size={14} /> <span class="mono">{path}</span></span></p>{/if}
<nav class="tabs" aria-label="Settings views">
  <a href="#/settings" aria-current={tab === "form" ? "page" : undefined} onclick={(e) => (e.preventDefault(), (tab = "form"))}>Common settings</a>
  <a href="#/settings" aria-current={tab === "file" ? "page" : undefined} onclick={(e) => (e.preventDefault(), (tab = "file"))}>config.toml</a>
</nav>

{#if !loaded}
  <p class="empty">Loading the settings…</p>
{:else if tab === "form"}
  <form onsubmit={saveForm}>
    {#each panels as panel (panel.table)}
      <section class="panel" aria-labelledby="panel-{panel.table}">
        <h2 id="panel-{panel.table}">{panel.title}</h2>
        <p class="muted">{panel.about}</p>
        <div class="form">
          {#each fields.filter((f) => f.key.startsWith(`${panel.table}.`)) as field (field.key)}{@render input(field)}{/each}
          {#if panel.table === "ui"}
            <div class="field">
              <label class="check"><input type="checkbox" checked={app.notify} onchange={(e) => app.setNotify(e.currentTarget.checked)} aria-describedby="hint-browser" /> Browser notifications from this tab</label>
              <span class="hint indent" id="hint-browser">For auto-forwards and failures while the tab is in the background. Kept in this browser only.</span>
            </div>
          {/if}
        </div>
      </section>
    {/each}
    {#if problems.length && !problems.every((p) => fields.some((f) => f.key === p.key))}
      <ul class="problems" role="alert">{#each problems as p, i (i)}<li>{p.key}: {p.message}</li>{/each}</ul>
    {/if}
    <div class="save-bar">
      <span class="muted">The daemon uses saved settings after a restart.</span>
      <button type="submit" class="btn primary" disabled={busy}>Save settings</button>
    </div>
  </form>
{:else}
  <form onsubmit={saveFile}>
    <section class="panel">
      <div class="field">
        <label for="toml">config.toml</label>
        <textarea id="toml" class="toml" spellcheck="false" bind:value={text}></textarea>
      </div>
      {#if problems.length}
        <ul class="problems" role="alert">
          {#each problems as p, i (i)}<li>{#if p.line}Line {p.line}: {/if}{#if p.key}{p.key}: {/if}{p.message}</li>{/each}
        </ul>
      {/if}
    </section>
    <div class="save-bar">
      <span class="muted">Nothing is saved until the whole file is valid.</span>
      <button type="submit" class="btn primary" disabled={busy}>Save config.toml</button>
    </div>
  </form>
{/if}
