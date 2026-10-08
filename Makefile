-include .env

MIGRATE ?= migrate
MIGRATIONS_PATH ?= internal/database/migrations
MIGRATION_STEPS ?= 1
HURL ?= hurl
HTTP_TEST_VARIABLES ?= tests/http/local.env
HTTP_TEST_FILES := $(sort $(wildcard tests/http/*.hurl))
GO ?= go

export DATABASE_URL

.PHONY: migrate-check migrate-up migrate-down migrate-version test test-race vet test-http test-integration verify

migrate-check:
	@command -v "$(MIGRATE)" >/dev/null 2>&1 || { \
		echo "error: migrate executable not found"; \
		exit 1; \
	}
	@test -n "$(DATABASE_URL)" || { \
		echo "error: DATABASE_URL is not set; configure it in .env or the environment"; \
		exit 1; \
	}

migrate-up: migrate-check
	@$(MIGRATE) -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" up

migrate-down: migrate-check
	@$(MIGRATE) -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" down "$(MIGRATION_STEPS)"

migrate-version: migrate-check
	@$(MIGRATE) -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" version

test:
	@$(GO) test ./...

test-race:
	@$(GO) test -race ./...

vet:
	@$(GO) vet ./...

test-http:
	@command -v "$(HURL)" >/dev/null 2>&1 || { \
		echo "error: hurl executable not found"; \
		exit 1; \
	}
	@test -f "$(HTTP_TEST_VARIABLES)" || { \
		echo "error: $(HTTP_TEST_VARIABLES) not found; copy tests/http/local.env.example first"; \
		exit 1; \
	}
	@$(HURL) --test --variables-file "$(HTTP_TEST_VARIABLES)" $(HTTP_TEST_FILES)

test-integration:
	@POSTGRES_TEST_DATABASE_URL="$${POSTGRES_TEST_DATABASE_URL:-$(DATABASE_URL)}" \
		KEYCLOAK_TEST_ISSUER_URL="$${KEYCLOAK_TEST_ISSUER_URL:-$${OIDC_ISSUER_URL:-http://localhost:8081/realms/wager-challenge}}" \
		SQS_TEST_ENDPOINT_URL="$${SQS_TEST_ENDPOINT_URL:-$${SQS_ENDPOINT_URL:-http://localhost:4566}}" \
		bash scripts/test-integration.sh

verify: test test-race vet test-integration test-http
