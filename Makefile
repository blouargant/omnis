# Agent Toolkit Makefile

MODULE      := github.com/blouargant/agent-toolkit
BIN_DIR     := bin
DIST_DIR    := dist
EXAMPLES_DIR := examples
ROOT_BIN    := agent-toolkit

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

.PHONY: build
build: build-root $(addprefix build-example-,$(CMDS)) ## Build the root binary and all examples for the host platform

.PHONY: build-root
build-root: ## Build the root agent-toolkit binary
	@mkdir -p $(BIN_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BIN_DIR)/$(ROOT_BIN) .

.PHONY: build-example-%
build-example-%: ## Build a single example (e.g. make build-example-s01_loop)
	@mkdir -p $(BIN_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BIN_DIR)/$* ./$(EXAMPLES_DIR)/$*

.PHONY: release
release: clean ## Build cross-platform release archives in dist/
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		stage="$(DIST_DIR)/agent-toolkit_$(VERSION)_$${os}_$${arch}"; \
		mkdir -p $$stage; \
		echo ">> building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build $(BUILD_FLAGS) -o $$stage/$(ROOT_BIN)$${ext} . || exit 1; \
		for cmd in $(CMDS); do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
				$(GO) build $(BUILD_FLAGS) -o $$stage/$${cmd}$${ext} ./$(EXAMPLES_DIR)/$$cmd || exit 1; \
		done; \
		cp README.md LICENSE $$stage/ 2>/dev/null || true; \
		if [ "$$os" = "windows" ]; then \
			(cd $(DIST_DIR) && zip -qr $$(basename $$stage).zip $$(basename $$stage)); \
		else \
			tar -czf $$stage.tar.gz -C $(DIST_DIR) $$(basename $$stage); \
		fi; \
		rm -rf $$stage; \
	done
	@echo ">> release artifacts:"; ls -1 $(DIST_DIR)

.PHONY: checksums
checksums: ## Generate SHA256 checksums for release artifacts
	@cd $(DIST_DIR) && shasum -a 256 *.tar.gz *.zip 2>/dev/null > SHA256SUMS && cat SHA256SUMS

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(DIST_DIR)

.PHONY: version
version: ## Print version info
	@echo "version=$(VERSION) commit=$(COMMIT) date=$(DATE)"
