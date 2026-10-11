# pfwd build tasks. Run make from a POSIX shell (Git Bash on Windows).
# Tool versions are pinned here and in .github/workflows/ci.yml.

MODULE                := github.com/hoanghero125/pfwd
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.9.0

ifeq ($(OS),Windows_NT)
EXE := .exe
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

GOLANGCI_LINT_DIR := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT     := $(GOLANGCI_LINT_DIR)/golangci-lint$(EXE)

.PHONY: build web test lint e2e demo snapshot release-dry-run

# Static binary with version info, like a release build.
build: export CGO_ENABLED := 0
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pfwd$(EXE) ./cmd/pfwd

# Web UI bundle for go:embed. A no-op until web/ exists (M6).
web:
ifneq ($(wildcard web/package.json),)
	cd web && pnpm install --frozen-lockfile && pnpm build
else
	@echo "web/ does not exist yet (added in M6); nothing to build"
endif

# The race detector needs cgo and a C compiler.
test: export CGO_ENABLED := 1
test:
	go test -race ./...

# Banned-call check, golangci-lint (includes staticcheck and govet), govulncheck.
lint: $(GOLANGCI_LINT)
	@if git grep -n -I -F --untracked 'InsecureIgnoreHostKey' -- '*.go' ':(exclude)docs/**'; then \
		echo "error: ssh.InsecureIgnoreHostKey is banned; host keys must always be verified" >&2; \
		exit 1; \
	fi
	$(GOLANGCI_LINT) config verify
	$(GOLANGCI_LINT) run ./...
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

$(GOLANGCI_LINT):
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(GOLANGCI_LINT_DIR)" $(GOLANGCI_LINT_VERSION)

e2e:
	@echo "no end-to-end tests yet (added in M1)"

demo:
	@echo "no demo tapes yet (added in M2)"

# Every platform into dist/. Signing is skipped: cosign keyless only works in release.yml.
snapshot:
	goreleaser check
	goreleaser release --snapshot --clean --skip=sign

# Full release build for the tag on HEAD, without publishing. Run it after
# tagging locally and before pushing the tag.
release-dry-run:
	goreleaser check
	goreleaser release --clean --skip=publish,announce,sign,sbom
