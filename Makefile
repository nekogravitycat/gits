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

.PHONY: all
all: lint test build

.PHONY: build
build:
	$(GO) build -o $(BIN) ./cmd/gits

.PHONY: install
install:
	$(GO) install ./cmd/gits

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

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) coverage.out
