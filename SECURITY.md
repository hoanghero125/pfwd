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

## How pfwd keeps you safe

A tunnel tool is a security tool. These are the rules pfwd follows, each with the test that holds it to them (the release checklist goes through this list line by line).

| Rule | How | Checked by |
| --- | --- | --- |
| Forwards don't reach your network by default | Local forwards listen on 127.0.0.1 unless `forward.bind` or `--bind` says otherwise; the TUI and the web UI mark forwards that listen elsewhere | `TestListenDefaultsLoopback`, `TestBindBadge` |
| Host keys are always verified | `known_hosts` is checked for every hop, a changed key is a hard stop, and a new one is asked about (or trusted with `--accept-new` / `accept-new`). `ssh.InsecureIgnoreHostKey` is banned from the code by a check in `make lint` and CI | `TestDialChangedKey`, `TestDialUnknownHost`, `TestDialKnownHostDifferentAlgo` |
| Secrets stay in memory | Passphrases and passwords are kept only in memory, for reconnects, and zeroed when pfwd quits; nothing typed reaches `state.json`. The log takes out anything that looks like a password, passphrase, token or private key, at every level | `TestLogRedactsSecrets`, `TestRedact` |
| No command built from your input runs on the server | Only the fixed detection scripts run there; the one value they take is quoted by a single helper | `TestShQuote`, `TestScriptsHaveNoSingleQuotes`, `TestShellCommand` |
| ProxyCommand runs as your ssh config says | Exactly as OpenSSH would run it, never built from what you type in pfwd; a host name that could pass options to `ssh -G` (such as `-oProxyCommand=...`) is refused | `TestDialProxyCommand`, `TestExpandProxyCommand`, `TestValidateAlias` |
| Only you can talk to the daemon | A Unix socket with mode 0600 in a directory only you can enter, with the peer's user ID checked where the OS reports it; on Windows a named pipe that only you can open, whose owner the client checks before it trusts the server | `TestSocketIsPrivate`, `TestPeerRefused` |
| A SOCKS proxy stays on loopback | Any other address is refused unless `forward.allow_lan_socks` or `--allow-lan-socks` | `TestSOCKSDefaultsToLoopback` |
| The HTTP probe stays in the tunnel | One `HEAD /` with a `pfwd` user agent, through the SSH connection, never for database ports; `detect.http_probe = false` turns it off | `TestProbeSetsScheme`, `TestSkipHTTPProbe` |
| The web UI is yours only | It listens on 127.0.0.1 only. A login link works once, for 10 minutes, and sets an HttpOnly, SameSite=Strict cookie; each tab gets its own token, which every request and the event stream must carry, so the cookie alone (sent to every port of 127.0.0.1) opens nothing. Host, Origin and Sec-Fetch-Site are checked on every request, and a strict Content Security Policy forbids anything not served by pfwd | `TestListenLoopbackOnly`, `TestLoginFlow`, `TestAPINeedsSessionAndToken`, `TestSecondLoginKeepsOtherTabs`, `TestRequestsFromElsewhereRefused`, `TestSecurityHeaders`, `TestEventsStream` |
| Releases can be checked | Checksums signed with cosign (keyless, from the release workflow), an SBOM per archive, `-trimpath` builds, `govulncheck` in CI and Dependabot for dependencies; [docs/install.md](docs/install.md#checking-a-download) shows how to verify a download | the release workflow |
