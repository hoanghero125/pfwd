# Contributing to pfwd

Thanks for helping. Before you change an area, read its package doc comment and tests; they describe how it is meant to behave.

## Development setup

- Go 1.26 or newer. CI tests the latest two Go releases.
- GNU make and a POSIX shell. On Windows, use Git Bash and install make separately (Git for Windows doesn't include it), for example with `scoop install make` or MSYS2's `pacman -S make`.
- A C compiler for the race detector used by `make test`: gcc on Linux, the Xcode command line tools on macOS, MSYS2 mingw-w64 gcc on Windows (with `C:\msys64\mingw64\bin` on Git Bash's PATH).
- `make lint` downloads the pinned golangci-lint into `bin/` the first time it runs.
- GoReleaser v2 for `make snapshot`: `go install github.com/goreleaser/goreleaser/v2@latest`.

## Everyday commands

| Command | What it does |
| --- | --- |
| `make build` | Builds `bin/pfwd` with version information |
| `make test` | Runs `go test -race ./...` |
| `make lint` | Banned-call check, golangci-lint (with staticcheck), govulncheck |
| `make e2e` | End-to-end tests against a real sshd in Docker (needs Docker with Compose) |
| `make soak` | An hour of forwards kept up through connection drops, against the same sshd; nightly CI runs it. `PFWD_SOAK_DURATION=3m PFWD_SOAK_DROP_EVERY=20s make soak` is a quick local run |
| `make snapshot` | GoReleaser build of every platform into `dist/` |

## Making a change

1. Fork the repository and create a branch from `main` (maintainers can branch in the main repository).
2. Write the failing test first. Every bug fix comes with a test that fails before the fix.
3. Run `make lint test` until both pass. CI runs the same checks on Linux, macOS and Windows. If you change the SSH, detection, forwarding or engine code, run `make e2e` too; CI runs it on Linux.
4. Open a pull request. Its title must follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) (`feat:`, `fix:`, `docs:`, …) because pull requests are squash-merged and the title becomes the commit message.

## Rules CI enforces

- `ssh.InsecureIgnoreHostKey` never appears in Go code: host keys are always verified.
- Every package and exported identifier has a doc comment.
- Text files use LF line endings (see `.gitattributes`).

## Reporting problems

Use the issue forms for bugs and feature requests. Report security problems privately as described in [SECURITY.md](SECURITY.md), never in a public issue.

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE) and you follow the [Code of Conduct](CODE_OF_CONDUCT.md).
