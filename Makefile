# pfwd build tasks. Run make from a POSIX shell (Git Bash on Windows).
# Tool versions are pinned here and in .github/workflows/*.yml (golangci-lint,
# govulncheck, GoReleaser). Dependabot does not bump these: update them together.

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

.PHONY: build web web-e2e test lint e2e soak perf demo snapshot release-dry-run

# Static binary with version info, like a release build.
build: export CGO_ENABLED := 0
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pfwd$(EXE) ./cmd/pfwd

# Web UI: install, check, unit-test and build web/dist, which the binary
# embeds and git keeps (commit it with the source change). Needs Node.
web:
	cd web && npm ci && npm run check && npm test && npm run build && npm run size

# Web UI in a browser against a mocked daemon: screens, keys, axe, budgets.
# PW_CHANNEL=chrome uses the installed Chrome.
web-e2e:
	cd web && npx playwright test --project=mock

# The race detector needs cgo and a C compiler.
test: export CGO_ENABLED := 1
test:
	go test -race ./...

# Banned-call check, golangci-lint (includes staticcheck and govet), govulncheck.
lint: $(GOLANGCI_LINT)
	@git grep -n -I -F --untracked 'InsecureIgnoreHostKey' -- '*.go' ':(exclude)docs/**'; status=$$?; \
	if [ $$status -eq 0 ]; then \
		echo "error: ssh.InsecureIgnoreHostKey is banned; host keys must always be verified" >&2; exit 1; \
	elif [ $$status -ne 1 ]; then \
		echo "error: git grep failed (exit $$status), so the InsecureIgnoreHostKey ban could not be checked" >&2; exit 1; \
	fi
	$(GOLANGCI_LINT) config verify
	$(GOLANGCI_LINT) run ./...
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

$(GOLANGCI_LINT):
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh | sh -s -- -b "$(GOLANGCI_LINT_DIR)" $(GOLANGCI_LINT_VERSION)

# End-to-end tests against sshd containers; needs Docker with the compose plugin.
e2e:
	go test -tags e2e -count=1 -timeout 10m -v ./test/e2e/...

# Soak test: forwards kept up through connection drops, an hour by default.
# PFWD_SOAK_DURATION and PFWD_SOAK_DROP_EVERY shorten it for a local run.
soak:
	go test -tags 'e2e soak' -run TestSoak -count=1 -timeout 75m -v ./test/e2e/...

# Performance budgets against the same sshd containers (Linux): first
# frame, first detected ports over a 50 ms RTT link, idle CPU and memory
# with 3 hosts and 20 forwards, throughput against ssh -L.
perf:
	go test -tags 'e2e perf' -run TestPerf -count=1 -timeout 10m -v ./test/e2e/...

# The README's TUI demo, demo/pfwd.gif. Needs vhs and Docker; demo/setup.sh
# starts the demo host first. CI's demo workflow also takes the web UI
# screenshots.
demo:
	demo/setup.sh
	vhs demo/pfwd.tape

# Every platform into dist/. Signing is skipped: cosign keyless only works in release.yml.
snapshot:
	goreleaser check
	goreleaser release --snapshot --clean --skip=sign

# Full release build for the tag on HEAD, without publishing. Run it after
# tagging locally and before pushing the tag.
release-dry-run:
	goreleaser check
	goreleaser release --clean --skip=publish,announce,sign,sbom
