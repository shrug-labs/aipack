# aipack CLI

VERSION := $(shell cat VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DISTRIBUTION ?= github
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X github.com/shrug-labs/aipack/internal/update.distribution=$(DISTRIBUTION)
BINARY  := aipack
DIST    := dist

TAGS ?=
ifneq ($(TAGS),)
  GO_TAGS := -tags $(TAGS)
endif

.PHONY: build install fmt fmt-check lint release-tag-check test validate dist clean help

build: ## Build for current platform into dist/
	@mkdir -p $(DIST)
	go build $(GO_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/aipack

install: build ## Build and install to ~/.local/bin
	@mkdir -p $(HOME)/.local/bin
	cp $(DIST)/$(BINARY) $(HOME)/.local/bin/$(BINARY)
	@if [ "$$(uname)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then \
		codesign -s - -f $(HOME)/.local/bin/$(BINARY) 2>/dev/null; \
	fi
	@printf "Installed: %s (%s)\n" "$(HOME)/.local/bin/$(BINARY)" "$(VERSION)"

fmt: ## Format Go source
	go fmt ./...

fmt-check: ## Fail if Go source is not formatted
	@test -z "$$(gofmt -l . | grep -v '^dist/\|^vendor/' )" || { gofmt -l . | grep -v '^dist/\|^vendor/'; echo "Go files need formatting. Run: make fmt"; exit 1; }

lint: ## Run static analysis (go vet + staticcheck + go fix)
	go vet $(GO_TAGS) ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck $(GO_TAGS) ./...; fi
	go fix ./...

release-tag-check: ## Validate TAG against VERSION (supports prereleases)
	@test -n "$(TAG)" || { echo "usage: make release-tag-check TAG=vX.Y.Z[-suffix]"; exit 1; }
	@version="$$(cat VERSION)"; \
	case "$(TAG)" in \
		"v$$version"|"v$$version-"*) ;; \
		*) echo "release tag $(TAG) does not match VERSION $$version"; exit 1 ;; \
	esac

test: ## Run Go tests
	go test $(GO_TAGS) ./...

validate: ## Validate pack content (PACK_ROOT required)
	@test -n "$(PACK_ROOT)" || { echo "usage: make validate PACK_ROOT=/path/to/pack"; exit 1; }
	go run $(GO_TAGS) ./cmd/aipack validate "$(PACK_ROOT)"
	@echo "validate passed"

dist: ## Cross-compile for all platforms
	@mkdir -p $(DIST)
	@for platform in darwin/arm64 darwin/amd64 linux/amd64 windows/amd64 windows/arm64; do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		ext=""; if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
		outname=$(BINARY)-$${goos}-$${goarch}$${ext}; \
		GOOS=$$goos GOARCH=$$goarch \
		go build $(GO_TAGS) -ldflags "$(LDFLAGS)" \
			-o $(DIST)/$$outname ./cmd/aipack || exit 1; \
		case "$$goos" in darwin) \
			if [ "$$(uname)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then \
				codesign -s - -f $(DIST)/$$outname 2>/dev/null; \
			fi ;; esac; \
		echo "  $(DIST)/$$outname"; \
	done

clean: ## Remove build artifacts
	rm -rf $(DIST)

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
