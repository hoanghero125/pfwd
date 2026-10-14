# Installing pfwd

pfwd is one static binary. It needs nothing on the server, and on your machine it doesn't need OpenSSH.

## Homebrew (macOS, Linux)

```sh
brew install hoanghero125/tap/pfwd
brew upgrade pfwd          # later, for a new release
```

## Scoop (Windows)

```powershell
scoop bucket add pfwd https://github.com/hoanghero125/scoop-bucket
scoop install pfwd/pfwd
scoop update pfwd          # later, for a new release
```

On Windows, stop the daemon before you update (`pfwd daemon stop`): Windows won't replace a binary that is running.

## After an update

A daemon started by `pfwd fwd --detach`, `pfwd daemon start` or `pfwd ui` keeps running the release it started with. Run `pfwd daemon stop` after updating; the next `pfwd ui` or `pfwd fwd --detach` starts the new one. `pfwd ui` and `pfwd attach` warn when the running daemon is from another release.

## Linux packages

The [releases page](https://github.com/hoanghero125/pfwd/releases) has `.deb`, `.rpm` and `.apk` packages for amd64 and arm64:

```sh
sudo apt install ./pfwd_<version>_amd64.deb       # Debian, Ubuntu
sudo dnf install ./pfwd-<version>-1.x86_64.rpm    # Fedora, RHEL, Rocky
sudo apk add --allow-untrusted ./pfwd_<version>_x86_64.apk   # Alpine
```

## Archives

Every release also has a `.tar.gz` (Linux, macOS, FreeBSD) or `.zip` (Windows) per platform. Unpack it and put `pfwd` somewhere on your `PATH`.

## With Go

```sh
go install github.com/hoanghero125/pfwd/cmd/pfwd@latest
```

This needs Go 1.26 or newer. The binary includes the web UI: its built files are part of the source.

## Checking a download

Each release has a `checksums.txt` signed with [cosign](https://docs.sigstore.dev/) in the release workflow (keyless, through GitHub's OIDC), and an SBOM per archive. To check an archive:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/hoanghero125/pfwd/\.github/workflows/release\.yml@refs/tags/v'
sha256sum --ignore-missing -c checksums.txt
```

## Building from source

You need Go 1.26 or newer, git and GNU make (on Windows, from Git Bash):

```sh
git clone https://github.com/hoanghero125/pfwd
cd pfwd
make build      # bin/pfwd, with version information
```

Changing the web UI also needs Node.js 22; see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Where pfwd keeps its files

| What | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Settings (`config.toml`) | `~/.config/pfwd` | `~/Library/Application Support/pfwd` | `%APPDATA%\pfwd` |
| Remembered forwards (`state.json`) and the log (`pfwd.log`) | `~/.local/state/pfwd` | `~/Library/Application Support/pfwd` | `%APPDATA%\pfwd` |

`pfwd config path` prints the settings file's path. pfwd writes nothing on the servers it connects to.

## Uninstalling

Remove the binary the way you installed it (`brew uninstall pfwd`, `scoop uninstall pfwd`, your package manager, or delete the file), then delete the directories above if you want your settings and remembered forwards gone too.
