# aipack CLI

VERSION := $(shell cat VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)
BINARY  := aipack
DIST    := dist

TAGS ?=
ifneq ($(TAGS),)
  GO_TAGS := -tags $(TAGS)
endif

.PHONY: build install fmt fmt-check release-tag-check test validate dist clean help

build: ## Build for current platform into dist/
	@mkdir -p $(DIST)
	go build $(GO_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/aipack

install: build ## Build and install to ~/.local/bin
	@mkdir -p $(HOME)/.local/bin
	cp $(DIST)/$(BINARY) $(HOME)/.local/bin/$(BINARY)
	@printf "Installed: %s (%s)\n" "$(HOME)/.local/bin/$(BINARY)" "$(VERSION)"

fmt: ## Format Go source
	go fmt ./...

fmt-check: ## Fail if Go source is not formatted
	@test -z "$$(gofmt -l . | grep -v '^dist/' )" || { gofmt -l . | grep -v '^dist/'; echo "Go files need formatting. Run: make fmt"; exit 1; }

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
	@for platform in darwin/arm64 darwin/amd64 linux/amd64; do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} \
		go build $(GO_TAGS) -ldflags "$(LDFLAGS)" \
			-o $(DIST)/$(BINARY)-$${platform%/*}-$${platform#*/} ./cmd/aipack || exit 1; \
		echo "  $(DIST)/$(BINARY)-$${platform%/*}-$${platform#*/}"; \
	done

clean: ## Remove build artifacts
	rm -rf $(DIST)

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
