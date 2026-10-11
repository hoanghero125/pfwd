# Contributing to pfwd

Thanks for helping. pfwd is built milestone by milestone from [docs/SPEC.md](docs/SPEC.md); read the spec section for the area you are changing first.

## Development setup

- Go 1.26 or newer. CI tests the latest two Go releases.
- GNU make and a POSIX shell. On Windows, run make from Git Bash.
- A C compiler for the race detector used by `make test`: gcc on Linux, the Xcode command line tools on macOS, MSYS2 mingw-w64 gcc on Windows.
- `make lint` downloads the pinned golangci-lint into `bin/` the first time it runs.
- GoReleaser v2 for `make snapshot`: `go install github.com/goreleaser/goreleaser/v2@latest`.

## Everyday commands

| Command | What it does |
| --- | --- |
| `make build` | Builds `bin/pfwd` with version information |
| `make test` | Runs `go test -race ./...` |
| `make lint` | Banned-call check, golangci-lint (with staticcheck), govulncheck |
| `make snapshot` | GoReleaser build of every platform into `dist/` |

## Making a change

1. Branch from `main`.
2. Write the failing test first. Every bug fix comes with a test that fails before the fix.
3. Run `make lint test` until both pass. CI runs the same checks on Linux, macOS and Windows.
4. Open a pull request. Its title must follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) (`feat:`, `fix:`, `docs:`, …) because pull requests are squash-merged and the title becomes the commit message.

## Rules CI enforces

- `ssh.InsecureIgnoreHostKey` never appears in Go code: host keys are always verified.
- Every package and exported identifier has a doc comment.
- Text files use LF line endings (see `.gitattributes`).

## Reporting problems

Use the issue forms for bugs and feature requests. Report security problems privately as described in [SECURITY.md](SECURITY.md), never in a public issue.

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE) and you follow the [Code of Conduct](CODE_OF_CONDUCT.md).
