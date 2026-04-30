# Agent Toolkit Makefile

MODULE      := github.com/blouargant/agent-toolkit
BIN_DIR     := bin
DIST_DIR    := dist
CMD_DIR     := cmd

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

# All command packages (cmd/<name>).
CMDS        := $(notdir $(wildcard $(CMD_DIR)/*))

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
build: $(addprefix build-,$(CMDS)) ## Build all commands for the host platform

.PHONY: build-%
build-%: ## Build a single command (e.g. make build-full)
	@mkdir -p $(BIN_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BIN_DIR)/$* ./$(CMD_DIR)/$*

.PHONY: release
release: clean ## Build cross-platform release archives in dist/
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		stage="$(DIST_DIR)/agent-toolkit_$(VERSION)_$${os}_$${arch}"; \
		mkdir -p $$stage; \
		echo ">> building $$os/$$arch"; \
		for cmd in $(CMDS); do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
				$(GO) build $(BUILD_FLAGS) -o $$stage/$${cmd}$${ext} ./$(CMD_DIR)/$$cmd || exit 1; \
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
