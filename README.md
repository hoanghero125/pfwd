# pfwd

The VS Code Ports panel for any terminal. Agentless, one binary, every OS.

[![CI](https://github.com/hoanghero125/pfwd/actions/workflows/ci.yml/badge.svg)](https://github.com/hoanghero125/pfwd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/hoanghero125/pfwd?include_prereleases&sort=semver)](https://github.com/hoanghero125/pfwd/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/hoanghero125/pfwd)](https://goreportcard.com/report/github.com/hoanghero125/pfwd)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`pfwd devbox` connects to a host from your `~/.ssh/config` and shows a live table of the TCP ports listening on it. Start `npm run dev` on the server and, a few seconds later, `localhost:5173` on your laptop reaches it. You don't press anything.

> **Status: v0.2.0, early.** One host at a time, Linux servers, local forwards. Reconnects, `-R`/`-D` forwards, macOS/BSD/Windows servers, scripting commands and a web UI come in later releases.

## Install

| Where | Command |
| --- | --- |
| macOS, Linux (Homebrew) | `brew install hoanghero125/tap/pfwd` |
| Windows (Scoop) | `scoop bucket add pfwd https://github.com/hoanghero125/scoop-bucket` then `scoop install pfwd/pfwd` |
| Debian, Ubuntu, Fedora, Alpine | `.deb`, `.rpm` and `.apk` packages on the [releases page](https://github.com/hoanghero125/pfwd/releases) |
| Any OS with Go 1.26+ | `go install github.com/hoanghero125/pfwd/cmd/pfwd@latest` |

Release archives come with a `checksums.txt` signed by cosign (keyless, from the release workflow) and an SBOM. To build from a clone you need git and GNU make: `make build`. On Windows, run make from Git Bash; [CONTRIBUTING.md](CONTRIBUTING.md) has the setup.

## Quick start

```sh
pfwd devbox            # an alias from ~/.ssh/config
pfwd alice@10.0.0.5    # or user@host, like ssh
pfwd devbox -p 2222 -i ~/.ssh/id_work -F ./ssh_config
```

The first time you connect to a server, pfwd shows its host key fingerprint and asks before saving it to `known_hosts`, the way `ssh` does. Then the table fills in:

```
 pfwd  devbox ● connected                                     auto: on   ? help
────────────────────────────────────────────────────────────────────────────────
  STATE   REMOTE  LOCAL           PROCESS      LABEL        CONN TRAFFIC
▶ ● fwd   :5173   localhost:5173  node         Vite http    2    ↓12K/s ↑1.0K/s
  ● fwd   :8888   localhost:8888  jupyter-lab  Jupyter http 0    ↓0 ↑0
  ───────────────────────── detected, not forwarded ──────────────────────────
  —       :6379   —               redis-server Redis        —    —
  —       :9090   —               ?            Prometheus   —    —
─ 1 ignored hidden, h shows ────────────────────────────────────────────────────
 ✓ Forwarded :5173 to localhost:5173 (auto)
 enter stop  o open  y copy  a auto  / filter  L log  ? help  q quit
```

| Key | Does |
| --- | --- |
| `↑` `↓` `j` `k`, `g` `G` | move, first, last |
| `enter` or `f` | forward the port to the same local port, or stop the forward |
| `F` | forward to a different local port |
| `x` | stop the forward |
| `o` | open the forward in your browser (`https://` when the server speaks TLS) |
| `y` | copy its URL |
| `a` | turn auto-forward on or off for this host |
| `/` | filter rows by port, process or label; `esc` clears the filter |
| `h` | show or hide ignored ports |
| `L` | event log |
| `?` | help |
| `q` or `ctrl+c` | quit (asks first while forwards are active) |

If the local port is taken, pfwd offers the next free one instead. With `ui.mouse` on (the default), a click selects a row and the wheel scrolls.

## Auto-forward

A port that starts listening while pfwd is connected gets forwarded to the same local port, with no key press, once two snapshots in a row have seen it. A desktop notification tells you, unless you set `ui.notifications = false`. Ports that were already listening when pfwd connected stay in the table, unforwarded, until you press `enter`.

Some ports are never auto-forwarded and are hidden from the table: port 22 and processes such as `sshd`, `systemd-resolved` and `containerd`. The view line under the table counts them; `h` shows them. `detect.ignore_ports` and `detect.ignore_processes` replace these lists, so keep `22` and `sshd` in yours if you still want ssh hidden; `forward.auto_ports` limits auto-forward to the ports you name.

A port you stop with `x` stays stopped for the rest of the session, and so does one whose auto-forward failed. Press `enter` on it to forward it by hand. Turn auto-forward off for a host with `a`, for one run with `--no-auto`, or for good with `forward.auto = false`.

Each new forward is probed once, through the SSH connection, to see whether it speaks HTTP or HTTPS. The LABEL column shows the result next to the service name. Database ports (Postgres, MySQL, Redis, MongoDB) are not probed.

## Configuration

pfwd works without a config file. To change its defaults, run `pfwd config edit`: it creates the file with every setting at its default, commented, and opens it in `$VISUAL`, `$EDITOR`, or notepad on Windows and vi elsewhere. `pfwd config validate` checks the file and points at the line and column of anything it doesn't accept; pfwd refuses to start with a broken config, so you never get silent defaults.

The file lives at `~/.config/pfwd/config.toml` on Linux, `~/Library/Application Support/pfwd/config.toml` on macOS and `%AppData%\pfwd\config.toml` on Windows. `pfwd config path` prints it. `PFWD_CONFIG` or `--config` points pfwd at another file.

```toml
[detect]
interval = "2s"            # how often to list the server's ports
ignore_ports = [22]        # ports or ranges like "9000-9100"

[forward]
auto = true                # forward new ports automatically
auto_ports = []            # if not empty, only auto-forward these, e.g. ["3000-3999", 5173]
bind = "127.0.0.1"         # 0.0.0.0 exposes forwards to your network
on_conflict = "ask"        # when a local port is taken: ask, next, random or fail

[ui]
theme = "auto"             # auto, dark or light
notifications = true       # desktop notification when a port is auto-forwarded

[labels]
8000 = "API"

[hosts.devbox]             # overrides for one host, by ssh config alias
auto = false
```

A few settings are accepted now but take effect in later releases: `forward.auto_expire`, `forward.restore` and `ssh.keepalive_*`, `ssh.reconnect_max_backoff` in v0.3.0, along with `hosts.<alias>.forwards`; `forward.privileged_offset` in v0.4.0. `pfwd config validate` says so when you set one.

These flags override the file for one run: `--auto` and `--no-auto`, `--bind`, `--interval` and `--no-color`. pfwd also honors `NO_COLOR`, and draws ASCII instead of Unicode symbols in the old Windows console (`ui.glyphs` changes that).

## How it works

pfwd resolves the host with `ssh -G`, so `Match`, `Include` and every default work exactly as they do for `ssh`; without OpenSSH it reads `~/.ssh/config` itself. It logs in with ssh-agent (including the Windows OpenSSH agent) and your `IdentityFile` keys, checks `known_hosts`, and always refuses a changed host key.

Nothing gets installed on the server. pfwd runs one probe, then a small `sh` loop on a single SSH channel that lists listening sockets with `ss` every 2 seconds, or reads `/proc/net/tcp` on busybox systems. It writes no files there and uploads no binary.

Forwards listen on `127.0.0.1` unless you change `forward.bind` or pass `--bind`, so your network can't reach them. When they listen on another address, the top bar says so.

Logs go to `pfwd.log` in pfwd's state directory: `~/.local/state/pfwd` on Linux, `~/Library/Application Support/pfwd` on macOS, `%APPDATA%\pfwd` on Windows. Set `--log-level debug` or `PFWD_LOG=debug` for more detail.

## Not supported yet

- Password and one-time-code (2FA, keyboard-interactive) logins, and passphrase-protected keys that aren't in ssh-agent (run `ssh-add` first). Prompts for all three come in v0.3.0; until then pfwd says which one the server asked for.
- `ProxyJump` and `ProxyCommand` hosts (v0.3.0).
- macOS, BSD and Windows servers (v0.4.0). The machine running pfwd can be macOS, Linux or Windows today.
- Reconnecting after a dropped connection (v0.3.0): restart pfwd for now.

## Why pfwd

- It needs no agent on the server, so it works on locked-down, shared and university machines where you can't upload anything.
- It is one static Go binary for Linux, macOS and Windows, and doesn't need OpenSSH at runtime.
- Scripting commands (`pfwd fwd`, `pfwd ls --json`) and several hosts in one window are planned for v0.5.0.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
