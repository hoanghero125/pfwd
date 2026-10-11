# Security Policy

## Supported versions

pfwd is pre-1.0. Only the latest release gets security fixes, so please upgrade before reporting.

| Version | Supported |
| ------- | --------- |
| Latest `0.x` release | Yes |
| Older releases | No |
| Unreleased `main` builds | Best effort |

## Reporting a vulnerability

**Do not report security problems in public issues, discussions or pull requests.**

Use GitHub's private vulnerability reporting: open the [Security tab](https://github.com/hoanghero125/pfwd/security), click **Report a vulnerability**, or go straight to <https://github.com/hoanghero125/pfwd/security/advisories/new>.

If you can't use GitHub, email **dekthedev.business@gmail.com** with `[pfwd security]` in the subject.

Please include as much of this as you can:

- pfwd version (`pfwd version`), OS and architecture
- the affected part (CLI, TUI, daemon IPC, web UI, SOCKS listener, release artifacts)
- steps to reproduce or a proof of concept
- the impact you expect

## What to expect

- An acknowledgement within **7 days**.
- Updates at least every 7 days until the issue is fixed or declined.
- When a fix ships, a GitHub Security Advisory (with a CVE where appropriate) that credits you, unless you prefer to stay anonymous.
- Please give us time to ship a fix before disclosing publicly.

## Scope

In scope: the `pfwd` binary and daemon, its IPC socket or named pipe, the local web UI, the SOCKS listener, and the binaries, checksums and signatures published on GitHub Releases.

Out of scope: bugs in OpenSSH, the remote SSH server or the operating system, and attacks that need control of your local user account.
