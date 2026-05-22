# Yoke Makefile

MODULE      := github.com/blouargant/yoke
BIN_DIR     := bin
DIST_DIR    := dist
EXAMPLES_DIR := examples
ROOT_BIN    := yoke
SERVER_BIN  := yoke-server

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS     := -s -w \
               -X main.version=$(VERSION) \
               -X main.commit=$(COMMIT) \
               -X main.date=$(DATE)

GO          ?= go
GOFLAGS     ?=
BUILD_FLAGS := -trimpath -ldflags '$(LDFLAGS)'

# Cross-compile target platforms (override with `make release PLATFORMS="linux/amd64"`).
PLATFORMS   ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

# All example packages (examples/<name>).
CMDS        := $(notdir $(wildcard $(EXAMPLES_DIR)/*))

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: fmt
fmt: ## Format sources
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: unit-tests
unit-tests: test ## Run unit tests

.PHONY: env-tests
env-tests: ## Source .env and run LLM tests
	@set -a; . ./.env; set +a; $(GO) test ./core/llm

# Default A2A endpoint used by a2a-smoke. Override per-invocation, e.g.:
#   make a2a-smoke A2A_URL=http://localhost:9091/ A2A_TOKEN=secret
A2A_URL ?= http://127.0.0.1:8091/

.PHONY: a2a-smoke
a2a-smoke: ## Validate A2A protocol + client wiring against a live endpoint (A2A_URL=, A2A_TOKEN=)
	@echo "== 1/2 protocol smoke (curl tasks/send) =="
	@A2A_URL='$(A2A_URL)' A2A_TOKEN='$(A2A_TOKEN)' scripts/a2a_smoke.sh
	@echo "== 2/2 client live test (a2a.SendTask) =="
	@YOKE_A2A_TEST_URL='$(A2A_URL)' YOKE_A2A_TEST_TOKEN='$(A2A_TOKEN)' \
		$(GO) test -tags integration -count=1 -v -run TestLive ./internal/a2a

.PHONY: all
all: build ## Alias for `build` (root binary + server)

.PHONY: build
build: build-root build-server ## Build the production binaries (root yoke + HTTP server) for the host platform

.PHONY: examples
examples: $(addprefix build-example-,$(CMDS)) ## Build all examples (opt-in; not part of the default build target)

.PHONY: build-root
build-root: ## Build the root yoke binary
	@mkdir -p $(BIN_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BIN_DIR)/$(ROOT_BIN) .

.PHONY: build-server
build-server: ## Build the HTTP API server (bin/yoke-server)
	@mkdir -p $(BIN_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BIN_DIR)/$(SERVER_BIN) ./server

.PHONY: run-server
run-server: ## Run the HTTP API server (requires YOKE_SERVER_TOKEN)
	$(GO) run ./server

.PHONY: build-example-%
build-example-%: ## Build a single example (e.g. make build-example-s01_loop)
	@mkdir -p $(BIN_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BIN_DIR)/$* ./$(EXAMPLES_DIR)/$*

.PHONY: release
	release: clean ## Build cross-platform release binaries of yoke in dist/ (raw binaries — no packaging)
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="$(DIST_DIR)/$(ROOT_BIN)_$(VERSION)_$${os}_$${arch}$${ext}"; \
		echo ">> building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build $(BUILD_FLAGS) -o $$out . || exit 1; \
	done
	@echo ">> release artifacts:"; ls -1 $(DIST_DIR)

GORELEASER ?= goreleaser

.PHONY: package
package: clean ## Build .deb + .rpm + .zip (Windows) + tar.gz archives via goreleaser into dist/
	cp -f config/*.json packaging/etc-yoke/
	cp -a config/filters packaging/etc-yoke/ 2>/dev/null || true
	@command -v $(GORELEASER) >/dev/null 2>&1 || { \
		echo "goreleaser not found. Install: https://goreleaser.com/install/"; \
		echo "  (e.g. go install github.com/goreleaser/goreleaser/v2@latest)"; \
		exit 1; \
	}
	$(GORELEASER) release --snapshot --clean --skip=publish
	@echo ">> package artifacts:"; ls -1 $(DIST_DIR) | grep -E '\.(deb|rpm|zip|tar\.gz|txt)$$' || true

.PHONY: package-check
package-check: ## Validate .goreleaser.yaml without building anything
	@command -v $(GORELEASER) >/dev/null 2>&1 || { \
		echo "goreleaser not found. Install: https://goreleaser.com/install/"; \
		exit 1; \
	}
	$(GORELEASER) check

.PHONY: checksums
checksums: ## Generate SHA256 checksums for release artifacts
	@cd $(DIST_DIR) && shasum -a 256 *.tar.gz *.zip 2>/dev/null > SHA256SUMS && cat SHA256SUMS

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(DIST_DIR)

.PHONY: version
version: ## Print version info
	@echo "version=$(VERSION) commit=$(COMMIT) date=$(DATE)"
