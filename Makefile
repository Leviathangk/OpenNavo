.DEFAULT_GOAL := help
.PHONY: help setup infra migrate seed seed-demo seed-e2e e2e-up e2e-down e2e-reset dev-api dev-worker dev-web dev-admin dev-desktop dev-desktop-web mock gen lint test e2e build gen-server lint-server test-server build-server perf-server gen-web lint-web test-web build-web

help: ## List unified commands
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "%-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: ## Check tools and install dependencies
	@node tooling/scripts/workspace.mjs setup
infra: ## Start isolated development infrastructure
	@node tooling/scripts/workspace.mjs infra
migrate: ## Run database migrations
	@node tooling/scripts/workspace.mjs migrate
seed: ## Load idempotent seed data
	@node tooling/scripts/workspace.mjs seed
seed-demo: ## Add demo features and collections to the local development database (requires a synced real catalog)
	@node tooling/scripts/workspace.mjs seed-demo
seed-e2e: ## Load fixed end-to-end fixtures (offline, backend-owned)
	@node tooling/scripts/workspace.mjs seed-e2e
e2e-up: ## Start the dedicated E2E backend (database, Redis 9/10, object bucket, API 18082)
	@node tooling/scripts/workspace.mjs e2e-up
e2e-down: ## Stop the dedicated E2E API (preserve data; idempotent)
	@node tooling/scripts/workspace.mjs e2e-down
e2e-reset: ## Recreate dedicated E2E data and restore the previous running state
	@node tooling/scripts/workspace.mjs e2e-reset
dev-api: ## Start public and admin APIs (8080)
	@node tooling/scripts/workspace.mjs dev-api
dev-worker: ## Start the worker
	@node tooling/scripts/workspace.mjs dev-worker
dev-web: ## Start the web app (3000)
	@node tooling/scripts/workspace.mjs dev-web
dev-admin: ## Start admin (9527)
	@node tooling/scripts/workspace.mjs dev-admin
dev-desktop: ## Start the Tauri desktop app
	@node tooling/scripts/workspace.mjs dev-desktop
dev-desktop-web: ## Start desktop browser mode (1420)
	@node tooling/scripts/workspace.mjs dev-desktop-web
mock: ## Start public (4010) and admin (4011) Prism mocks
	@node tooling/scripts/workspace.mjs mock
gen: ## Generate contract bindings and tokens
	@node tooling/scripts/workspace.mjs gen
lint: ## Check contracts, frontend, Go, and Rust
	@node tooling/scripts/workspace.mjs lint
test: ## Run frontend, Go, and Rust tests
	@node tooling/scripts/workspace.mjs test
e2e: ## Run browser end-to-end tests
	@node tooling/scripts/workspace.mjs e2e
build: ## Build all applications
	@node tooling/scripts/workspace.mjs build
gen-server: ## Generate Go contract code only
	@node tooling/scripts/workspace.mjs gen-server
lint-server: ## Check contracts and Go only
	@node tooling/scripts/workspace.mjs lint-server
test-server: ## Run Go tests only
	@node tooling/scripts/workspace.mjs test-server
build-server: ## Build Go only
	@node tooling/scripts/workspace.mjs build-server
perf-server: ## Measure public API latency (requires Docker; excluded from make test)
	@node tooling/scripts/workspace.mjs api-performance
gen-web: ## Generate frontend only (TS types, IPC bindings, tokens)
	@node tooling/scripts/workspace.mjs gen-web
lint-web: ## Check frontend only
	@node tooling/scripts/workspace.mjs lint-web
test-web: ## Run frontend tests only
	@node tooling/scripts/workspace.mjs test-web
build-web: ## Build frontend only
	@node tooling/scripts/workspace.mjs build-web

i18n-sync: ## Incrementally sync interface translations: four languages, eight for the desktop app (ARGS="--targets native,server --dry-run")
	@node tooling/scripts/workspace.mjs i18n-sync $(ARGS)

.PHONY: gen-mcp lint-mcp test-mcp build-mcp dev-mcp
gen-mcp: ## Generate MCP Python contracts and allowlist only
	@node tooling/scripts/workspace.mjs gen-mcp
lint-mcp: ## Check MCP lock file, ruff, pyright, and generation consistency
	@node tooling/scripts/workspace.mjs lint-mcp
test-mcp: ## Run MCP mock-backend and permission-drift tests
	@node tooling/scripts/workspace.mjs test-mcp
build-mcp: ## Build MCP Python distribution
	@node tooling/scripts/workspace.mjs build-mcp
dev-mcp: ## Start MCP (default 8790; credentials from environment)
	@node tooling/scripts/workspace.mjs dev-mcp
