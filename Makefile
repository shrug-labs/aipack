# aipack CLI

TASK := go run ./tools/task

.PHONY: build install fmt fmt-check lint release-tag-check test dist clean help

build: ## Build for current platform into dist/
	$(TASK) build

install: build ## Build and install to ~/.local/bin
	$(TASK) install

fmt: ## Format Go source
	go fmt ./...

fmt-check: ## Fail if Go source is not formatted
	$(TASK) fmt-check

lint: ## Run static analysis (go vet + staticcheck + go fix)
	$(TASK) lint

release-tag-check: ## Validate TAG against VERSION (supports prereleases)
	$(TASK) release-tag-check

test: ## Run Go tests
	$(TASK) test

dist: ## Cross-compile for all platforms
	$(TASK) dist

clean: ## Remove build artifacts
	$(TASK) clean

help: ## Show available targets
	$(TASK) help

.DEFAULT_GOAL := help
