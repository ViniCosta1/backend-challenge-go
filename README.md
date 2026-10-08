# Wager Processing Backend

## Overview

Financial wallet and wager-processing service written in Go. It accepts the
same business operations over authenticated HTTP and an at-least-once SQS
input, serializes changes per Wallet in PostgreSQL, and records immutable
Ledger, Inbox and Outbox state in the same transaction.

The executable is composed with Uber Fx and runs PostgreSQL persistence,
Keycloak/OIDC authentication, the HTTP API, SQS consumer, Outbox publisher and
pending-reference worker. Local infrastructure is provisioned by Docker
Compose using Keycloak and MiniStack.

- [Original challenge](./CHALLENGE.md)
- [Architecture decisions](./ARCHITECTURE.md)

## Architecture at a glance

```text
 Keycloak                         MiniStack / SQS
    |                       input FIFO       events FIFO
    v                           |                ^
 HTTP API ---------------------+                |
    |                                            | Outbox publisher
    v                                            |
 Application use cases / transaction boundary --+
    |
    v
 Domain: Money, Wallet, WagerTransaction, Ledger
    |
    v
 PostgreSQL: Wallet / Wager / Ledger / Inbox / Outbox
                         ^
                         | pending-reference worker
```

The domain has no dependency on HTTP, SQS, Fx or PostgreSQL. Application
declares repository contracts and transaction boundaries; infrastructure
implements them with pgx, OIDC and AWS SDK adapters.

## Prerequisites

- Docker Engine with Docker Compose v2
- Go 1.26.8 for host-side builds and tests
- [Hurl](https://hurl.dev/docs/installation.html) for HTTP contract tests
- `migrate` v4 when running migrations manually; Compose runs them without a
  host installation
- `curl` for integration preflight checks and `jq` for the shell examples

## Quick start

The Compose defaults are sufficient for local development. Copying the example
is recommended when commands will also run on the host:

```bash
cp .env.example .env
cp tests/http/local.env.example tests/http/local.env
docker compose up --build
```

On hosts where Docker access requires elevation, use `sudo docker compose`.
Compose waits for PostgreSQL, applies all migrations, imports the Keycloak realm,
provisions the SQS queues and starts the application.

Verify the running service:

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
curl --fail http://localhost:8080/metrics
```

## Services and ports

| Service | Local endpoint | Purpose |
|---|---|---|
| Application | `http://localhost:8080` | HTTP API, health and metrics |
| PostgreSQL 17 | `localhost:5432` | Application and Keycloak databases |
| Keycloak | `http://localhost:8081` | OAuth2/OIDC identity provider |
| MiniStack | `http://localhost:4566` | AWS-compatible SQS endpoint |

The app container uses host networking on Linux so Keycloak's public issuer
remains exactly `http://localhost:8081/realms/wager-challenge`.

## Environment variables

`.env.example` is the authoritative local template. Important groups are:

| Group | Variables |
|---|---|
| Application | `HTTP_ADDR`, HTTP timeouts, `LOG_LEVEL`, startup/shutdown timeouts |
| PostgreSQL | `DATABASE_URL`, `DATABASE_MAX_CONNS` |
| OIDC | `OIDC_ISSUER_URL`, `OIDC_AUDIENCE` |
| Local clients | `PROVIDER_A_CLIENT_SECRET`, `PROVIDER_B_CLIENT_SECRET`, `INTERNAL_SERVICE_CLIENT_SECRET` |
| SQS | endpoint, region, dummy credentials, input/DLQ/events queue names and operational timeouts |
| Workers | Outbox and pending-reference polling/operation settings |
| Integration | `POSTGRES_TEST_DATABASE_URL`, `KEYCLOAK_TEST_ISSUER_URL`, `SQS_TEST_ENDPOINT_URL` |

Configuration is validated before lifecycle components start. Queue URLs may
be provided explicitly; otherwise they are resolved by name.

## Authentication

Keycloak imports realm `wager-challenge` automatically. Its API audience is
`wager-api` and it provides service-account clients:

| Client | Role | Local development secret |
|---|---|---|
| `provider-a` | `provider` | `provider-a-local-secret` |
| `provider-b` | `provider` | `provider-b-local-secret` |
| `internal-service` | `internal` | `internal-service-local-secret` |

These values are intentionally public local-development credentials, not real
secrets and not suitable for production.

Obtain a client-credentials token:

```bash
TOKEN=$(curl --fail --silent \
  -X POST http://localhost:8081/realms/wager-challenge/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=internal-service \
  -d client_secret=internal-service-local-secret | jq -r .access_token)
```

Tokens are validated for issuer, RS256 signature/JWKS, expiry and audience.
Provider identity comes from signed `azp`, never from a request body.

## Authorization

- `internal-service` may create/read Wallets, list Ledger and reconcile.
- `provider-a` and `provider-b` may process and read only their own wagers.
- Cross-provider reads return the same 404 as an unknown transaction.
- A provider cannot override its identity through `providerId` in JSON.
- Health and metrics endpoints are public.

SQS has a separate trust boundary: its producer is considered an authorized
internal service and asserts the business `providerId` in the envelope. OIDC
applies to HTTP. Production IAM and Queue Policies belong to deployment and are
outside this repository; MiniStack credentials are compatibility-only dummy
values. Financial correctness still relies on Inbox, persistent idempotency,
constraints and PostgreSQL locking rather than FIFO security/deduplication.

## HTTP examples

Create a Wallet with an internal token:

```bash
curl --fail -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"playerId":"11111111-1111-4111-8111-111111111111","initialBalance":{"amount":"100.00","currency":"BRL"}}'
```

Obtain a provider token, then submit a BET. Save the Wallet/player IDs returned
above in `WALLET_ID` and `PLAYER_ID`:

```bash
PROVIDER_TOKEN=$(curl --fail --silent \
  -X POST http://localhost:8081/realms/wager-challenge/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=provider-a \
  -d client_secret=provider-a-local-secret | jq -r .access_token)

curl --fail -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" \
  -H 'Idempotency-Key: bet-example-1' \
  -H 'Content-Type: application/json' \
  -d "{\"externalTransactionId\":\"bet-example-1\",\"playerId\":\"$PLAYER_ID\",\"walletId\":\"$WALLET_ID\",\"roundId\":\"round-1\",\"gameId\":\"game-1\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}"
```

Repeating exactly that request returns the original balance with
`idempotentReplay: true`. A WIN uses `kind: "WIN"` and a positive amount; it may
optionally reference a processed BET from the same round without requiring the
same amount. REFUND/ROLLBACK include:

```json
"referenceExternalTransactionId": "bet-example-1"
```

Useful reads:

```bash
curl -H "Authorization: Bearer $PROVIDER_TOKEN" \
  http://localhost:8080/providers/provider-a/wagering/transactions/bet-example-1
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/wallets/$WALLET_ID/ledger?limit=50"
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/wallets/$WALLET_ID/reconciliation
```

Ledger pagination uses an opaque keyset cursor; pass the returned `nextCursor`
without decoding or changing it.

## HTTP status codes

| Status | Meaning |
|---|---|
| 200 | Successful/replayed wager or successful read/reconciliation |
| 201 | Wallet created |
| 202 | Referenced operation persisted as `PENDING_REFERENCE` |
| 400 | Invalid JSON, Money, UUID or business input shape |
| 401 | Missing, invalid or expired token |
| 403 | Authenticated identity lacks permission or attempts provider spoofing |
| 404 | Resource absent or intentionally hidden across provider boundaries |
| 409 | Wallet uniqueness or persistent idempotency/external-ID conflict |
| 422 | Durable business rejection, with stable `failureCode` |
| 503 | Required infrastructure temporarily unavailable |

Unsupported request media type returns 415; unexpected internal errors return
500 without exposing implementation details.

## Money

JSON Money uses a decimal string with exactly two digits after the point, for
example `{"amount":"25.00","currency":"BRL"}`. The domain converts this to
`int64` minor units with overflow checks; it never uses floating point. The
current financial flow is BRL-only. See [ARCHITECTURE.md](./ARCHITECTURE.md).

## SQS / MiniStack

Compose provisions these FIFO queues automatically:

- `wager-transactions.fifo`
- `wager-transactions-dlq.fifo`
- `wager-events.fifo`

Input defaults are a 30-second visibility timeout, 20-second long polling and
redrive after five receives; the DLQ retains messages for 14 days. Producers
use `MessageGroupId=walletId` and `MessageDeduplicationId=messageId`. Integration
events use aggregate ID and stable event ID respectively. Neither FIFO ordering
nor its deduplication window replaces database-backed idempotency.

## Migrations

Compose runs UP automatically. With `DATABASE_URL` and the `migrate` binary on
the host:

```bash
make migrate-up
make migrate-version
make migrate-down                    # one migration by default
make migrate-down MIGRATION_STEPS=3  # full current schema on a clean database
```

Migration 000002 deliberately refuses DOWN when rejected transactions already
contain result-balance snapshots. Reverting them would discard exact replay
history; use a clean/compatible database rather than deleting audit data.

## Tests

### Unit and standard package tests

```bash
make test
make test-race
make vet
```

The standard Go command may skip tests that require external infrastructure.

### HTTP contract tests

```bash
cp tests/http/local.env.example tests/http/local.env
make test-http
```

Hurl obtains real client-credentials tokens and exercises the running stack.
Details: [docs/HTTP_TESTING.md](./docs/HTTP_TESTING.md).

### Real integration tests

```bash
make test-integration
```

This target requires reachable PostgreSQL, Keycloak and MiniStack. It performs
preflight checks, exports the integration variables, runs the real suites and
fails if an infrastructure-gated suite was skipped. Defaults come from `.env`;
override the three `*_TEST_*` variables for a dedicated environment.

### Full verification

```bash
make verify
```

This runs standard tests, race detector, vet, real integration and Hurl. Start
the complete Compose stack first.

## Required distributed scenarios

The real integration suite contains reproducible tests for:

- 50 concurrent duplicates producing one financial movement;
- two distinct BET 80 against Wallet 100 yielding one PROCESSED, one REJECTED,
  balance 20 and one debit;
- independent Wallets progressing without a global lock;
- three OS processes running full Fx applications against shared dependencies;
- commit-before-SQS-delete redelivery;
- two competing Outbox publishers and publish-before-mark recovery;
- pending reference before its original operation and restart recovery;
- equivalent operation crossing HTTP/SQS in both orders.

Run all of them with `make test-integration`; test names and exact assertions
live beside their adapters under `internal/**/**_integration_test.go`.

## Failure and recovery guarantees

- PostgreSQL uniqueness and `SELECT ... FOR UPDATE` provide cross-process
  financial idempotency and per-Wallet serialization.
- Inbox completion shares the financial transaction; SQS delete happens only
  after commit.
- Financial data and Outbox snapshots commit together; publication is
  at-least-once with a stable event ID.
- Unresolved references persist attempts and exponential backoff, survive
  restart and eventually resolve or become a terminal rejection.
- Broker redrive handles transient/permanent input failures after a finite
  number of receives. Exactly-once across PostgreSQL and SQS is not claimed.

## Troubleshooting

- If Docker commands return permission denied, use the host's configured Docker
  group or prefix Compose commands with `sudo`.
- Ports 5432, 4566, 8080 and 8081 must be free.
- Keycloak import does not overwrite an existing persisted realm. If local
  development secrets changed, recreate the local volumes intentionally.
- `make test-integration` fails early when any real dependency is unavailable;
  check `/health/ready`, container health and the three integration URLs.
- Manual migration DOWN 000002 can fail by design when it would discard
  rejected-result history.
- Host networking used by the app service is Linux-oriented. On another Docker
  platform, run `go run ./cmd/api` on the host or provide networking/issuer URLs
  that preserve the exact OIDC issuer.

## Documentation

- [CHALLENGE.md](./CHALLENGE.md) — immutable original statement
- [ARCHITECTURE.md](./ARCHITECTURE.md) — decisions, invariants and trade-offs
- [docs/HTTP_TESTING.md](./docs/HTTP_TESTING.md) — Hurl usage and scenarios
- [tests/http](./tests/http) — executable HTTP contracts
