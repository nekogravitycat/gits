# gits — build, lint and test entry points.
#
# The pre-commit hook (.githooks/pre-commit) reads GOLANGCI_LINT_VERSION from this file, so the
# pin lives here and nowhere else.

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOLANGCI_LINT_VERSION ?= v2.12.2

BIN_DIR := bin
BIN := $(BIN_DIR)/gits
PKG := ./...

# `gits --version` reports this. Off a tag it's exactly the tag (v0.1.0); between tags it's
# `git describe` (v0.1.0-3-gabcdef), so a bug report from a dev build still names a commit.
# goreleaser stamps the same var via `.Tag` at release time — see .goreleaser.yaml.
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS := -X github.com/nekogravitycat/gits/internal/cli.version=$(VERSION)

.PHONY: all
all: lint test build

.PHONY: build
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/gits

.PHONY: install
install:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/gits

# Unit tests only: no test here spawns a real git binary, so this stays fast enough to sit in the
# pre-commit gate. Real-git coverage lives behind the `integration` tag (see test-integration).
.PHONY: test
test:
	$(GO) test $(PKG)

.PHONY: test-integration
test-integration:
	$(GO) test -tags integration $(PKG)

.PHONY: test-all
test-all: test test-integration

.PHONY: cover
cover:
	$(GO) test -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt:
	gofmt -w $$(git ls-files '*.go')

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix:
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-tools
lint-tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Point git at the tracked hooks directory. Re-run after cloning on a new machine.
.PHONY: hooks
hooks:
	git config core.hooksPath .githooks

.PHONY: tidy
tidy:
	$(GO) mod tidy

# Tags and pushes a release. The pushed tag is the whole trigger: .github/workflows/release.yml
# runs goreleaser on it, which builds, checksums and publishes the binaries — nothing here talks
# to GitHub except the tag push itself. `go install .../gits@latest` then resolves to it, because
# that is how the Go module proxy picks `latest` once semver tags exist (no repo-side config for
# that half — it is just how modules work).
#
# Usage: make release VERSION=v0.1.0
.PHONY: release
release:
	@[ "$(origin VERSION)" = "command line" ] || \
		(echo "usage: make release VERSION=vX.Y.Z" && exit 2)
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || \
		(echo "VERSION must look like vX.Y.Z, got $(VERSION)" && exit 2)
	@git diff --quiet && git diff --cached --quiet || \
		(echo "release: working tree is not clean" && exit 1)
	@[ "$$(git rev-parse --abbrev-ref HEAD)" = "main" ] || \
		(echo "release: not on main" && exit 1)
	@git fetch origin --quiet
	@[ "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" ] || \
		(echo "release: local main is not in sync with origin/main" && exit 1)
	@git rev-parse "$(VERSION)" >/dev/null 2>&1 && \
		(echo "release: tag $(VERSION) already exists" && exit 1) || true
	$(MAKE) lint test
	git tag -a "$(VERSION)" -m "$(VERSION)"
	git push origin "$(VERSION)"
	@echo "pushed $(VERSION) -- release workflow will publish binaries to GitHub Releases"

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) coverage.out
