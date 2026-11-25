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

.PHONY: operator-broker-dev
operator-broker-dev: ## Create the local Studio operator's RabbitMQ and Redis logins (stratum_operator_dev)
	@printf '%s' stratum_operator_dev | \
		RABBITMQCTL='$(COMPOSE) -f $(COMPOSE_FILE) exec -T rabbitmq rabbitmqctl' \
		REDIS_CLI='$(COMPOSE) -f $(COMPOSE_FILE) exec -T redis redis-cli' \
		sh deploy/broker-init/operator.sh add stratum_operator_dev

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

## --- Backups ---

BACKUP_DIR ?= backups

.PHONY: db-backup
db-backup: ## Dump the local database into BACKUP_DIR (default backups/)
	@umask 077; \
	mkdir -p $(BACKUP_DIR); \
	file="$(BACKUP_DIR)/stratum-$$(date -u +%Y%m%dT%H%M%SZ).dump"; \
	if $(COMPOSE) -f $(COMPOSE_FILE) exec -T postgres pg_dump --format=custom --no-owner -U stratum stratum > "$$file"; then \
		echo "wrote $$file"; \
	else \
		rm -f "$$file"; \
		echo "backup failed" >&2; \
		exit 1; \
	fi

.PHONY: db-restore-verify
db-restore-verify: ## Prove a dump restores: make db-restore-verify FILE=backups/stratum-....dump
	@if [ -z "$(FILE)" ]; then echo "usage: make db-restore-verify FILE=<dump>"; exit 1; fi
	@sh deploy/backup/restore-verify.sh "$(FILE)"

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
test: ## Run unit and handler tests; the Postgres URLs .env exports are blanked, so no test reaches a database
	@$(foreach v,$(TEST_DB_URLS),$(v)=) gotestsum --hide-summary=skipped ./... -race -count=1

# Integration tests run against their own database, so a running cmd/worker never
# relays the outbox rows they write, and their relay runs never claim the dev data's
# due rows. Every Postgres URL comes from .env with its database swapped for TEST_DB,
# which expects the .env form .../stratum?... . test-db refuses a URL it cannot swap,
# because a test pool or the migrate run on it would still reach the dev database.
# TEST_DB is created in the compose postgres service, the server .env points at by
# default. The roles are cluster-wide and the migrations grant them, so a migrated
# TEST_DB needs nothing else.
TEST_DB ?= stratum_test
TEST_DB_URLS := POSTGRES_URL POSTGRES_APP_URL POSTGRES_WORKER_URL POSTGRES_WEBHOOK_URL POSTGRES_UNSCOPED_URL POSTGRES_OPERATOR_URL
to_test_db = $(subst /stratum?,/$(TEST_DB)?,$(1))
check_test_db = $(if $(findstring /$(TEST_DB)?,$(call to_test_db,$($(1)))),,$(error $(1) must name its database as /stratum?... so the tests can be moved to $(TEST_DB)))
TEST_ENV = TEST_DATABASE_URL='$(call to_test_db,$(POSTGRES_TEST_URL))' \
	$(foreach v,$(TEST_DB_URLS),$(v)='$(call to_test_db,$($(v)))') \
	TEST_REDIS_URL='$(CACHE_URL)'
# The bare export at the top would hand these DSN-bearing strings to every recipe.
unexport TEST_ENV TEST_DB_URLS to_test_db check_test_db

.PHONY: test-db
test-db: ## Create the integration-test database (TEST_DB, default stratum_test) if missing and migrate it up
	$(if $(filter stratum,$(TEST_DB)),$(error TEST_DB must not be stratum, the dev database))
	$(call check_test_db,POSTGRES_TEST_URL)
	$(foreach v,$(TEST_DB_URLS),$(if $($(v)),$(call check_test_db,$(v))))
	@$(COMPOSE) -f $(COMPOSE_FILE) exec -T postgres psql -U stratum -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$(TEST_DB)'" | grep -qx 1 || \
		$(COMPOSE) -f $(COMPOSE_FILE) exec -T postgres createdb -U stratum -O stratum $(TEST_DB)
	@migrate -path $(MIGRATIONS_PATH) -database "$(call to_test_db,$(POSTGRES_URL))" up

.PHONY: test-integration
test-integration: test-db ## Run all tests including integration (requires infra) against TEST_DB
	@$(TEST_ENV) gotestsum -- ./... -race -count=1

.PHONY: test-cover
test-cover: test-db ## Run all tests (incl. integration) with coverage and print the total %
	@$(TEST_ENV) gotestsum -- ./... -race -count=1 -coverprofile=coverage.out
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
pre-push: verify-modules vet fmt lint vuln test-db ## Mirror the GitHub CI api+web+studio jobs locally
	@$(TEST_ENV) go test ./... -race -count=1 -timeout 120s -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out
	cd ui/app && npm ci && npm run typecheck && npm run lint && npx prettier --write "**/*.{ts,tsx}" && npm run build
	cd ui/studio && go vet ./... && gofmt -w . && go build ./...
	cd ui/studio/frontend && npm ci && npm run lint && npx prettier --write "**/*.{ts,tsx}" && npm run build

## --- Help ---

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
