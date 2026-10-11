# pfwd

The VS Code Ports panel for any terminal. Agentless, one binary, every OS.

[![CI](https://github.com/hoanghero125/pfwd/actions/workflows/ci.yml/badge.svg)](https://github.com/hoanghero125/pfwd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/hoanghero125/pfwd?include_prereleases&sort=semver)](https://github.com/hoanghero125/pfwd/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/hoanghero125/pfwd)](https://goreportcard.com/report/github.com/hoanghero125/pfwd)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> **Status: pre-alpha.** This is the project scaffold (milestone M0); no forwarding works yet. The [build spec](docs/SPEC.md) describes what is coming and in which milestone.

`pfwd devbox` connects to a host from your `~/.ssh/config`, shows a live table of the TCP ports listening on it, and forwards them to `localhost` with one key or automatically. Start `npm run dev` on the server and `localhost:5173` works on your laptop a second later.

## Why pfwd

- **Agentless.** Detection uses tools already on the server (`ss`, `netstat`, `lsof`, `/proc/net/tcp`). Nothing is uploaded, so it works on locked-down and shared servers.
- **Native everywhere.** One static Go binary for Linux, macOS and Windows, with no OpenSSH dependency.
- **Multi-host and scriptable.** Several hosts in one TUI, plus `pfwd fwd` and `pfwd ls --json` for scripts.

## Install

Packages for Homebrew, Scoop, winget, AUR and deb/rpm/apk start with v0.1.0. Until then, build from source with Go 1.26 or newer:

    git clone https://github.com/hoanghero125/pfwd && cd pfwd && make build

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
