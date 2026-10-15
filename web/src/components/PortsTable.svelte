<script lang="ts">
  import { exposed, formatRate, localAddr, pauseText, portLabel, portTarget, target } from "../lib/format";
  import { forwardsOf } from "../lib/reduce";
  import { app } from "../lib/store.svelte";
  import type { Forward, Port } from "../lib/types";
  import Details from "./Details.svelte";
  import ForwardDialog from "./ForwardDialog.svelte";
  import LabelDialog from "./LabelDialog.svelte";
  import Icon from "./Icon.svelte";
  import Sparkline from "./Sparkline.svelte";
  import State from "./State.svelte";

  let { host }: { host: string } = $props();

  type Row =
    | { key: string; kind: "forward"; f: Forward; port?: Port }
    | { key: string; kind: "detected"; port: Port };

  let filter = $state("");
  let showHidden = $state(false);
  // The cursor: the row's key, and its port and place, so it stays on the
  // port when the row moves between the groups (as forwarding does), and
  // on the same line when its row goes.
  let sel = $state({ key: "", port: 0, index: 0 });
  let menu = $state("");
  let details = $state("");
  let custom = $state<Port | null>(null);
  // back is the "More actions" button focus returns to after a dialog.
  let back = $state("");
  let labeling = $state<Forward | null>(null);

  const session = $derived(app.model.sessions[host]);
  const info = $derived(app.model.ports[host]);
  const connected = $derived(session?.state === "connected");
  const ports = $derived(connected ? (info?.ports ?? []) : []);
  const forwards = $derived(forwardsOf(app.model, host, "local"));

  function local(remoteHost: string): boolean {
    return ["", "127.0.0.1", "::1", "localhost", "0.0.0.0", "::"].includes(remoteHost);
  }

  function matches(text: string): boolean {
    const q = filter.trim().toLowerCase();
    return !q || text.toLowerCase().includes(q);
  }

  const forwarded = $derived<Row[]>(
    forwards
      .map((f): Row => ({ key: `f${f.id}`, kind: "forward", f, port: ports.find((p) => serves(f, p)) }))
      .filter((r) => r.kind === "forward" && matches(`:${r.f.remote} ${r.f.local} ${r.f.remote_host} ${r.port?.process ?? ""} ${r.f.label || r.port?.label || ""}`)),
  );
  const taken = (p: Port) => forwards.some((f) => serves(f, p));
  const hiddenCount = $derived(ports.filter((p) => p.ignored && !taken(p)).length);
  const detected = $derived<Row[]>(
    ports
      .filter((p) => !taken(p) && (showHidden || !p.ignored))
      .sort((a, b) => a.port - b.port)
      .map((p): Row => ({ key: `p${p.port}`, kind: "detected", port: p }))
      .filter((r) => r.kind === "detected" && matches(`${portLabel(r.port.port, r.port.addrs)} ${r.port.process} ${r.port.label}`)),
  );
  const rows = $derived([...forwarded, ...detected]);
  const current = $derived(
    rows.find((r) => r.key === sel.key) ??
      (sel.port ? rows.find((r) => portOf(r) === sel.port) : undefined) ??
      rows[Math.min(sel.index, rows.length - 1)],
  );

  // serves reports whether forward f reaches detected port p: the same
  // port, at the address the port listens on (any loopback name counts).
  function serves(f: Forward, p: Port): boolean {
    if (f.remote !== p.port) return false;
    const at = portTarget(p.addrs);
    return local(f.remote_host) ? at === "127.0.0.1" || at === "::1" : f.remote_host === at;
  }

  // portOf is the server port a row stands for: a detected port, or a
  // forward of one. Forwards to other machines have none.
  function portOf(r: Row): number {
    return r.kind === "detected" ? r.port.port : (r.port?.port ?? 0);
  }

  function select(r: Row) {
    sel = { key: r.key, port: portOf(r), index: Math.max(0, rows.indexOf(r)) };
  }
  const detailed = $derived(details ? app.model.forwards[details] : undefined);

  // Rows that appear after the table first drew fade in.
  const born = new Map<string, number>();
  const mountedAt = Date.now();
  function isNew(key: string): boolean {
    if (!born.has(key)) born.set(key, Date.now());
    return (born.get(key) ?? 0) - mountedAt > 1000;
  }

  function traffic(f: Forward): string {
    switch (f.state) {
      case "paused":
        return pauseText(f.pause);
      case "failed":
        return f.error || "failed";
      case "starting":
        return "starting";
    }
    const r = app.model.rates[f.id];
    if (!r) return "↓0 ↑0";
    return `↓${formatRate(r.in)}/s ↑${formatRate(r.out)}/s`;
  }

  function forwardPort(p: Port) {
    return app.forward(host, { kind: "local", remote_host: portTarget(p.addrs), remote: p.port, label: p.label });
  }

  function open(f: Forward) {
    const t = target(f);
    if (t.web) window.open(t.text, "_blank", "noopener,noreferrer");
  }

  function primary(r: Row) {
    if (r.kind === "detected") void forwardPort(r.port);
    else void app.stop(r.f);
  }

  function move(by: number) {
    if (!rows.length) return;
    const i = Math.max(0, rows.findIndex((r) => r.key === current?.key));
    const next = rows[Math.min(rows.length - 1, Math.max(0, i + by))];
    select(next);
    document.getElementById(`row-${next.key}`)?.scrollIntoView({ block: "nearest" });
  }

  function onkeydown(e: KeyboardEvent) {
    const t = e.target as HTMLElement | null;
    // A dialog open above the menu takes Escape for itself.
    if (e.key === "Escape" && menu && !document.querySelector("dialog[open]")) {
      const opener = menu;
      menu = "";
      document.getElementById(`more-${opener}`)?.focus();
      e.preventDefault();
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey || t?.closest("input, textarea, select, dialog, .menu-list") || detailed) return;
    const r = current;
    switch (e.key) {
      case "j":
      case "ArrowDown":
        move(1);
        break;
      case "k":
      case "ArrowUp":
        move(-1);
        break;
      case "g":
      case "Home":
        move(-rows.length);
        break;
      case "G":
      case "End":
        move(rows.length);
        break;
      case "Enter":
      case "f":
        if (!r || t?.closest("button, a")) return;
        primary(r);
        break;
      case "x":
        if (r?.kind === "forward") void app.stop(r.f);
        break;
      case "o":
        if (r?.kind === "forward") open(r.f);
        break;
      case "y":
        if (r?.kind === "forward") void app.copy(target(r.f).text);
        break;
      case "p":
        if (r?.kind === "forward" && !r.f.config) void app.pin(r.f, !r.f.pinned);
        break;
      case "F":
        if (r?.kind === "detected") custom = r.port;
        break;
      case "h":
        showHidden = !showHidden;
        break;
      default:
        return;
    }
    e.preventDefault();
  }

  function closeMenu(e: MouseEvent) {
    if (menu && !(e.target as HTMLElement).closest(".menu")) menu = "";
  }
</script>

<svelte:window {onkeydown} onclick={closeMenu} />

<div class="toolbar">
  <label class="search">
    <Icon name="search" />
    <input type="search" placeholder="Filter by port, process or label" aria-label="Filter ports" data-filter bind:value={filter} />
    <kbd aria-hidden="true">/</kbd>
  </label>
</div>

{#snippet forwardRow(r: Extract<Row, { kind: "forward" }>)}
  {@const f = r.f}
  {@const t = target(f)}
  {@const rate = app.model.rates[f.id]}
  {@const live = f.state === "active" && !!rate && rate.in + rate.out > 0}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions (j/k select rows from the keyboard) -->
  <div
    id="row-{r.key}"
    class="row"
    class:new={isNew(r.key)}
    role="listitem"
    aria-current={current?.key === r.key ? "true" : undefined}
    onclick={() => select(r)}
    onfocusin={() => select(r)}
  >
    <State state={f.state} />
    <span class="patch">
      <span class="jack">{f.remote_host && !local(f.remote_host) ? `${f.remote_host}:${f.remote}` : `:${f.remote}`}</span>
      <span class="cable g-{f.state} {f.state}" class:live aria-hidden="true"></span>
      <span class="jack local">{localAddr(f)}</span>
    </span>
    <span class="what">
      <span class="proc" class:muted={!r.port?.process}>{r.port?.process || "unknown process"}</span>
      <span class="label">
        {#if f.label || r.port?.label}<span>{f.label || r.port?.label}</span>{/if}
        {#if f.pinned}<span class="tag"><Icon name="pin" size={12} /> pinned</span>{/if}
        {#if f.auto}<span class="tag">auto</span>{/if}
        {#if exposed(f)}<span class="tag lan" title="Reachable from your network"><Icon name="lan" size={12} /> LAN</span>{/if}
      </span>
    </span>
    <span class="traffic" title={f.state === "failed" ? f.error : undefined}>
      {#if f.state === "active"}
        <Sparkline values={rate?.history ?? []} />
        <span class="rate"><b>↓{formatRate(rate?.in ?? 0)}/s</b> ↑{formatRate(rate?.out ?? 0)}/s</span>
      {:else}
        <span class="rate">{traffic(f)}</span>
      {/if}
    </span>
    <span class="acts">
      {#if (f.state === "active" || f.state === "paused") && t.web}
        <button class="btn ghost icon-only wide" aria-label="Open" title="Open {t.text}" onclick={() => open(f)}><Icon name="open" /></button>
      {/if}
      {#if f.state === "active" || f.state === "paused"}
        <button class="btn ghost icon-only wide" aria-label={t.web ? "Copy URL" : "Copy address"} title="Copy {t.text}" onclick={() => app.copy(t.text)}><Icon name="copy" /></button>
      {/if}
      <span class="menu">
        <button id="more-{r.key}" class="btn ghost icon-only" aria-label="More actions for :{f.remote}" aria-haspopup="menu" aria-expanded={menu === r.key} onclick={() => (menu = menu === r.key ? "" : r.key)}><Icon name="more" size={18} /></button>
        {#if menu === r.key}
          <span class="menu-list" role="menu">
            <button role="menuitem" onclick={() => ((details = f.id), (back = `more-${r.key}`), (menu = ""))}><Icon name="info" /> Details</button>
            {#if t.web}<button role="menuitem" onclick={() => (open(f), (menu = ""))}><Icon name="open" /> Open in the browser</button>{/if}
            <button role="menuitem" onclick={() => (app.copy(t.text), (menu = ""))}><Icon name="copy" /> {t.web ? "Copy URL" : "Copy address"}</button>
            {#if !f.config}
              <button role="menuitem" onclick={() => (app.pin(f, !f.pinned), (menu = ""))}><Icon name="pin" /> {f.pinned ? "Unpin" : "Pin"}</button>
              <button role="menuitem" onclick={() => ((labeling = f), (back = `more-${r.key}`), (menu = ""))}><Icon name="tag" /> Label…</button>
            {/if}
            <hr />
            <button role="menuitem" class="danger" onclick={() => (app.stop(f), (menu = ""))}><Icon name="close" /> Stop</button>
          </span>
        {/if}
      </span>
    </span>
  </div>
{/snippet}

{#snippet detectedRow(r: Extract<Row, { kind: "detected" }>)}
  {@const p = r.port}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions (j/k select rows from the keyboard) -->
  <div
    id="row-{r.key}"
    class="row detected"
    class:new={isNew(r.key)}
    role="listitem"
    aria-current={current?.key === r.key ? "true" : undefined}
    onclick={() => select(r)}
    onfocusin={() => select(r)}
  >
    <span class="st muted"><span aria-hidden="true">—</span><span class="visually-hidden">not forwarded</span></span>
    <span class="patch"><span class="jack">{portLabel(p.port, p.addrs)}</span></span>
    <span class="what">
      <span class="proc" class:muted={!p.process}>{p.process || "unknown process"}</span>
      <span class="label">
        {#if p.label}<span>{p.label}</span>{/if}
        {#if p.ignored}<span class="tag">hidden</span>{/if}
      </span>
    </span>
    <span class="traffic"></span>
    <span class="acts">
      <button class="btn soft small" onclick={() => forwardPort(p)}>Forward <Icon name="arrow" size={14} /></button>
      <span class="menu">
        <button id="more-{r.key}" class="btn ghost icon-only" aria-label="More actions for :{p.port}" aria-haspopup="menu" aria-expanded={menu === r.key} onclick={() => (menu = menu === r.key ? "" : r.key)}><Icon name="more" size={18} /></button>
        {#if menu === r.key}
          <span class="menu-list" role="menu">
            <button role="menuitem" onclick={() => ((custom = p), (back = `more-${r.key}`), (menu = ""))}><Icon name="arrow" /> Forward to another local port…</button>
          </span>
        {/if}
      </span>
    </span>
  </div>
{/snippet}

{#if !session}
  <p class="empty">pfwd isn't connected to {host}. Connect it to see its ports.</p>
{:else if session.state !== "connected" && !forwards.length}
  <p class="empty">Ports show up once pfwd is connected to {host}.</p>
{:else}
  {#if info?.error && connected}
    <div class="notice warn">
      <Icon name="warn" size={18} />
      <span class="grow">Detection unavailable: {info.error}. You can still add forwards with New forward; <code>pfwd doctor {host}</code> says more.</span>
    </div>
  {/if}

  <h2 class="group">Forwarded <span class="n">{forwarded.length}</span></h2>
  {#if forwarded.length}
    <div class="rows" role="list" aria-label="Forwarded ports">
      {#each forwarded as r (r.key)}
        {#if r.kind === "forward"}{@render forwardRow(r)}{/if}
      {/each}
    </div>
  {:else}
    <p class="empty">{filter ? "No forward matches the filter." : "Nothing forwarded yet. Forward a detected port below, or add one with New forward."}</p>
  {/if}

  <h2 class="group">Detected, not forwarded <span class="n">{detected.length}</span></h2>
  {#if detected.length}
    <div class="rows" role="list" aria-label="Detected ports">
      {#each detected as r (r.key)}
        {#if r.kind === "detected"}{@render detectedRow(r)}{/if}
      {/each}
    </div>
  {:else}
    <p class="empty">
      {#if filter}No port matches the filter.{:else if connected && info}No other listening ports on {host}. Start a server there and it shows up here.{:else}Looking for listening ports…{/if}
    </p>
  {/if}
  {#if hiddenCount}
    <p class="more">
      <button class="btn ghost small" onclick={() => (showHidden = !showHidden)}>{showHidden ? "Hide" : "Show"} {hiddenCount} hidden {hiddenCount === 1 ? "port" : "ports"}</button>
    </p>
  {/if}
{/if}

{#if detailed}
  <Details f={detailed} onclose={() => (details = "")} returnTo={back} />
{/if}
{#if custom}
  <ForwardDialog {host} port={custom} onclose={() => (custom = null)} returnTo={back} />
{/if}
{#if labeling}
  <LabelDialog f={labeling} onclose={() => (labeling = null)} returnTo={back} />
{/if}
