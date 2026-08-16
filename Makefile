# Stratum — Makefile
#
# Run `make help` to list targets. Copy .env.example to .env first —
# `-include` below is silent if .env doesn't exist yet, but most targets
# (migrate-*, run-*) need POSTGRES_URL etc. to actually be set.
#
# Container engine: defaults to `podman compose`. If you're on Docker,
# override on the command line or export in your shell profile:
#   make infra-up COMPOSE=docker compose

-include .env
export

COMPOSE ?= podman compose
COMPOSE_FILE := deploy/docker-compose.local.yml
MIGRATIONS_PATH := db/migrations
DATABASE_URL ?= $(POSTGRES_URL)
CACHE_URL ?= $(REDIS_URL)

.DEFAULT_GOAL := help

PROFILE ?=

PROFILE_FLAG :=
ifneq ($(PROFILE),)
PROFILE_FLAG := --profile $(PROFILE)
endif

## --- Infra ---

.PHONY: infra-up
infra-up: ## Start Postgres, RabbitMQ, Redis (detached)
	$(COMPOSE) -f $(COMPOSE_FILE) $(PROFILE_FLAG) up -d

.PHONY: infra-down
infra-down: ## Stop and remove infra containers (keeps volumes)
	$(COMPOSE) -f $(COMPOSE_FILE) $(PROFILE_FLAG) down

.PHONY: infra-destroy
infra-destroy: ## Stop infra AND delete volumes (full reset — destroys local data)
	$(COMPOSE) -f $(COMPOSE_FILE) $(PROFILE_FLAG) down -v

.PHONY: infra-logs
infra-logs: ## Tail logs from all infra containers
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f

.PHONY: infra-observability-up
infra-observability-up:
	$(MAKE) infra-up PROFILE=observability

.PHONY: infra-observability-down
infra-observability-down:
	$(MAKE) infra-down PROFILE=observability

.PHONY: infra-observability-destroy
infra-observability-destroy:
	$(MAKE) infra-down PROFILE=observability

.PHONY: infra-observability-logs
infra-observability-logs:
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f \
		openobserve \
		otel-collector

## --- Migrations ---
## Usage: make migrate-create name=create_organizations_table

.PHONY: migrate-install
migrate-install: ## Install the golang-migrate CLI (postgres driver only)
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

.PHONY: migrate-create
migrate-create: ## Create a new up/down migration pair: make migrate-create name=xyz
	@if [ -z "$(name)" ]; then echo "usage: make migrate-create name=<migration_name>"; exit 1; fi
	migrate create -ext sql -dir $(MIGRATIONS_PATH) -seq $(name)

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations
	migrate -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: ## Revert the last migration
	migrate -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" down 1

.PHONY: migrate-down-all
migrate-down-all: ## Revert ALL migrations — destroys schema, use with care
	migrate -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" down -all

.PHONY: migrate-version
migrate-version: ## Print the current migration version
	migrate -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" version

.PHONY: migrate-force
migrate-force: ## Force the migration version after a failed/dirty migration: make migrate-force version=N
	@if [ -z "$(version)" ]; then echo "usage: make migrate-force version=<N>"; exit 1; fi
	migrate -path $(MIGRATIONS_PATH) -database "$(DATABASE_URL)" force $(version)

## --- Run ---

.PHONY: run-api
run-api: ## Run the HTTP API server
	go build -o bin/stratum-api ./cmd/api && ./bin/stratum-api

.PHONY: run-worker
run-worker: ## Run the background worker (RabbitMQ consumers)
	go build -o bin/stratum-worker ./cmd/worker && ./bin/stratum-worker

.PHONY: run-web
run-web: ## Run the web frontend dev server (ui/app)
	cd ./ui/app && npm run dev

.PHONY: run-studio
run-studio: ## Run Stratum Studio in dev mode (ui/studio)
	cd ./ui/studio && wails3 dev

## --- Build ---

.PHONY: build
build: ## Build both binaries into ./bin
	go build -o bin/stratum-api ./cmd/api
	go build -o bin/stratum-worker ./cmd/worker

## --- Docs ---

.PHONY: swagger-install
swagger-install: ## Install the swag CLI (OpenAPI doc generator)
	go install github.com/swaggo/swag/cmd/swag@latest

.PHONY: swagger
swagger: ## Regenerate Swagger/OpenAPI docs from handler annotations
	swag init -g internal/platform/httpserver/swagger.go -d . --exclude ./ui,./bin --parseInternal --parseDependency --useStructName --ot go -o internal/platform/httpserver/docs
	swag init -g internal/platform/httpserver/swagger.go -d . --exclude ./ui,./bin --parseInternal --parseDependency --useStructName --ot json,yaml -o docs/specs

## --- Quality ---

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: verify-modules
verify-modules: ## Fail if go mod tidy would change go.mod/go.sum
	go mod tidy
	git diff --exit-code go.mod go.sum

.PHONY: fmt
fmt: ## gofmt every file
	gofmt -w .

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: test
test: ## Run all tests
	gotestsum --hide-summary=skipped ./... -race -count=1

.PHONY: test-integration
test-integration: ## Run all tests including integration (requires infra): make test-integration
	TEST_DATABASE_URL=$(POSTGRES_TEST_URL) TEST_REDIS_URL=$(CACHE_URL) gotestsum -- ./... -race -count=1

.PHONY: test-cover
test-cover: ## Run all tests (incl. integration) with coverage and print the total %
	TEST_DATABASE_URL=$(POSTGRES_TEST_URL) TEST_REDIS_URL=$(CACHE_URL) gotestsum -- ./... -race -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

.PHONY: test-cover-html
test-cover-html: test-cover ## Same as test-cover, then open the HTML report in a browser
	go tool cover -html=coverage.out

.PHONY: lint
lint: ## Run golangci-lint (install separately: https://golangci-lint.run/welcome/install/)
	golangci-lint run ./...

.PHONY: vuln
vuln: ## Run Vulnerability scan
	govulncheck ./...

## --- Local dev loop ---

.PHONY: dev-reset
dev-reset: ## Full local reset: wipe infra (incl. observability), bring it back up fresh
	$(MAKE) infra-destroy
	$(MAKE) infra-up
	@echo "Waiting for Postgres to accept connections..."
	@sleep 5
	$(MAKE) migrate-up

.PHONY: expose-api
expose-api: ## Expose the local API via ngrok (reserved domain, personal account required)
	ngrok http --url=oversurely-unslow-tanna.ngrok-free.dev 8000

.PHONY: sync-deps
# Bumps to latest INCLUDING majors, unreviewed — run on a branch, not main.
# After running: make test / typecheck / build before trusting the result.
sync-deps: ## Upgrade Go (api/worker) and ui/app (web) deps to latest, then tidy/install
	go get -u ./... && go mod tidy
	cd ui/app && npx npm-check-updates -u && npm install

.PHONY: sync-studio-deps
# Bumps to latest INCLUDING majors, unreviewed — run on a branch, not main.
# After running: make test / typecheck / build before trusting the result.
sync-studio-deps: ## Upgrade studio deps to latest
	cd ui/studio && go get -u ./... && go mod tidy
	cd ui/studio/frontend && npx npm-check-updates -u && npm install

.PHONY: pre-push
pre-push: verify-modules vet fmt lint vuln ## Mirror the GitHub CI api+web+studio jobs locally
	TEST_DATABASE_URL=$(POSTGRES_TEST_URL) go test ./... -race -count=1 -timeout 120s -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out
	cd ui/app && npm ci && npm run typecheck && npm run lint && npx prettier --write "**/*.{ts,tsx}" && npm run build
	cd ui/studio && go vet ./... && gofmt -w . && go build ./...
	cd ui/studio/frontend && npm ci && npm run lint && npx prettier --write "**/*.{ts,tsx}" && npm run build

## --- Help ---

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'