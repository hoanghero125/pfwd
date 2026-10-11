# pfwd

The VS Code Ports panel for any terminal. Agentless, one binary, every OS.

[![CI](https://github.com/hoanghero125/pfwd/actions/workflows/ci.yml/badge.svg)](https://github.com/hoanghero125/pfwd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/hoanghero125/pfwd?include_prereleases&sort=semver)](https://github.com/hoanghero125/pfwd/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/hoanghero125/pfwd)](https://goreportcard.com/report/github.com/hoanghero125/pfwd)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`pfwd devbox` connects to a host from your `~/.ssh/config` and shows a live table of the TCP ports listening on it. Start `npm run dev` on the server, the port shows up a second or two later, and one key makes it `localhost:5173` on your laptop.

> **Status: v0.1.0, early.** One host at a time, Linux servers, local forwards. Auto-forward, reconnects, `-R`/`-D` forwards, macOS/BSD/Windows servers, scripting commands and a web UI come in later releases.

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
 pfwd  devbox ● connected                                                ? help
────────────────────────────────────────────────────────────────────────────────
  STATE    REMOTE  LOCAL            PROCESS              CONN  TRAFFIC
▶ ● fwd    :5173   localhost:5173   node                 2     ↓12K/s ↑1.0K/s
  ───────────────────────── detected, not forwarded ──────────────────────────
  —        :8888   —                jupyter-lab          —     —
```

| Key | Does |
| --- | --- |
| `↑` `↓` `j` `k`, `g` `G` | move, first, last |
| `enter` or `f` | forward the port to the same local port, or stop the forward |
| `F` | forward to a different local port |
| `x` | stop the forward |
| `?` | help |
| `q` or `ctrl+c` | quit (asks first while forwards are active) |

If the local port is taken, pfwd offers the next free one instead.

## How it works

pfwd resolves the host with `ssh -G`, so `Match`, `Include` and every default work exactly as they do for `ssh`; without OpenSSH it reads `~/.ssh/config` itself. It logs in with ssh-agent (including the Windows OpenSSH agent) and your `IdentityFile` keys, checks `known_hosts`, and always refuses a changed host key.

Nothing gets installed on the server. pfwd runs one probe, then a small `sh` loop on a single SSH channel that lists listening sockets with `ss` every 2 seconds, or reads `/proc/net/tcp` on busybox systems. It writes no files there and uploads no binary.

Forwards listen on `127.0.0.1` only, so your network can't reach them.

Logs go to `pfwd.log` in pfwd's state directory: `~/.local/state/pfwd` on Linux, `~/Library/Application Support/pfwd` on macOS, `%APPDATA%\pfwd` on Windows. Set `--log-level debug` or `PFWD_LOG=debug` for more detail.

## Not supported yet

- Passphrase-protected keys that aren't in ssh-agent: run `ssh-add` first. Passphrase and password prompts come in v0.3.0.
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
