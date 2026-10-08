# AGENTS.md

## Project

Backend Challenge — Distributed Wager Processing in Go.

`CHALLENGE.md` is the immutable copy of the original challenge statement.
`README.md` documents the implemented solution and may evolve with it. Never
edit `CHALLENGE.md` when updating implementation documentation.

This repository implements a financial wallet/wager service with:

- Go
- Uber Fx
- PostgreSQL
- AWS SQS (MiniStack/LocalStack locally)
- External OAuth2/OIDC IdP (Keycloak recommended)
- Docker Compose
- Versioned SQL migrations

Primary evaluation priorities:

1. Financial integrity
2. Concurrency correctness
3. Persistent idempotency
4. Messaging/recovery
5. Tests
6. Architecture/modeling
7. Observability
8. Documentation

Do not trade correctness in the first four areas for architectural elegance.

---

## General Working Rules

Before changing code:

1. Read the relevant existing files first.
2. Read `ARCHITECTURE.md`.
3. Read the current SQL migrations before writing SQL.
4. Preserve existing domain invariants, constructors, rehydration methods, tests, and naming.
5. Do not redesign working code unless the current task requires it.
6. Prefer the smallest correct change that advances the challenge.
7. Do not introduce abstractions "for future flexibility" without an immediate need.
8. Do not silently make an important architecture decision. If the task depends on an undecided policy, report it.

Always run after meaningful changes:

```bash
gofmt -w <changed go files>
go test ./...
go test -race ./...
go vet ./...
```

When persistence/migrations are involved, prefer a real PostgreSQL integration test over mocks.

---

## Architecture Boundaries

### `internal/domain`

Contains business rules, invariants, entities, value objects and domain/integration event models.

The domain MUST NOT depend on:

- PostgreSQL
- pgx
- HTTP
- SQS
- Uber Fx
- Keycloak
- infrastructure packages

Domain entities do not persist themselves.

Do not add `Save`, SQL, `pgx.Tx`, repository implementations, transport DTOs, or framework-specific concerns to domain types.

Fields should remain encapsulated. Add only minimal read-only getters when persistence/serialization genuinely requires them.

Never use reflection or `unsafe` to bypass encapsulation.

### `internal/application`

Contains use cases and contracts consumed by use cases.

It may depend on `internal/domain`.

It MUST NOT depend on:

- pgx
- PostgreSQL-specific types
- infrastructure packages
- HTTP/SQS adapters

Repository interfaces belong here because the application consumes them.

Business orchestration belongs here.

### `internal/infrastructure`

Contains technical implementations:

- PostgreSQL repositories
- transaction manager
- SQS adapters/workers
- Keycloak/OIDC integration
- infrastructure configuration

Infrastructure implements application contracts.

---

## Transaction Boundary

Financial operations may touch:

- Wallet
- WagerTransaction
- WalletLedgerEntry
- Inbox
- Outbox

When applicable, they must be persisted atomically.

Repositories MUST NOT open/commit/rollback their own SQL transactions.

The application expresses the transaction boundary through:

```go
TransactionManager.WithinTransaction(...)
```

The PostgreSQL implementation:

1. opens one `pgx.Tx`;
2. creates transaction-bound repositories using that exact tx;
3. executes the callback;
4. rolls back on callback error;
5. commits on success;
6. propagates commit/open errors.

All repositories supplied to the callback must use the same transaction.

The application must never receive `pgx.Tx`.

---

## Persistence

Use explicit SQL with `pgx/v5`.

Do not introduce an ORM.

Use existing migrations as the source of truth for:

- table names
- columns
- constraints
- indexes
- SQL types

Do not invent schema names in repository code.

Use domain rehydration methods when reading persisted entities.

Money is persisted in minor units (`BIGINT`) and must never use floating point.

Use versioned migrations for schema changes. Do not silently rewrite an already-applied migration to change behavior; create the next migration when appropriate.

---

## Money

Money is an immutable value object.

Rules:

- no floats;
- fixed two-decimal external representation;
- internal minor-unit arithmetic using integer values;
- currency must be carried by Money;
- current main flow may operate BRL-only;
- incompatible currencies must be rejected;
- arithmetic must handle integer overflow;
- negative values may exist only for internal calculations where valid;
- wallet balances must never become negative.

Do not bypass Money rules in repositories or handlers.

---

## Wallet

Wallet is the aggregate responsible for its balance invariants.

Important behavior:

- balance must never be negative;
- debit/credit rules belong to the domain;
- currency must match movements;
- version starts at `1`;
- version increments only when balance changes;
- `LOSS` must not increment version;
- persisted version uses `int64`.

Wallet does not know PostgreSQL.

---

## WagerTransaction

External kinds:

- `BET`
- `WIN`
- `LOSS`
- `REFUND`
- `ROLLBACK`

Internal kind:

- `OPENING`

Main statuses include:

- `PENDING`
- `PENDING_REFERENCE`
- `PROCESSED`
- `REJECTED`
- `FAILED`

Terminal state transitions must remain protected by the domain.

Important persisted identity fields include:

- providerId
- externalTransactionId
- idempotencyKey
- payloadHash

Persist the observed processing result balance when needed for exact replay, including definitive rejected operations where a wallet balance was observed.

---

## Ledger

`WalletLedgerEntry` is immutable audit history.

Each entry contains:

- walletId
- transactionId
- direction (`DEBIT` / `CREDIT`)
- money
- balanceBefore
- balanceAfter
- createdAt

Rules:

- validate `balanceAfter = balanceBefore ± money`;
- one movement per `(walletId, transactionId)`;
- no ledger for `LOSS`;
- no ledger for rejected operations;
- database must prevent update/delete of ledger entries.

Do not put wager-type business rules inside the ledger repository.

---

## Concurrency Decision

Chosen strategy:

**Pessimistic per-wallet locking using PostgreSQL `SELECT ... FOR UPDATE`.**

Reason:

- works across independent application instances;
- does not rely on process memory;
- serializes operations only for the same wallet;
- different wallets can continue in parallel;
- straightforward to reason about under the challenge deadline.

Rules:

- acquire the wallet lock inside the same SQL transaction used for financial persistence;
- keep transactions short;
- never use a global mutex or in-memory lock as the correctness mechanism;
- do not weaken this strategy without an explicit architecture decision.

Required scenario:

- initial balance: `100.00 BRL`
- two concurrent distinct BETs of `80.00`
- exactly one `PROCESSED`
- exactly one insufficient-funds `REJECTED`
- final balance `20.00`
- one debit ledger entry

---

## Financial Idempotency

Financial idempotency is persistent and belongs to wager processing, not Inbox.

Identity protections:

1. `(providerId, idempotencyKey)`
2. `(providerId, externalTransactionId)`

A deterministic `payloadHash` is stored to detect reuse of a key with different business content.

Expected behavior:

### Same key + same payload hash

Replay the persisted result:

- no second wallet effect;
- no second ledger movement;
- no duplicate financial events;
- `idempotentReplay = true`;
- return the original observed result balance, even if the wallet changed later.

### Same key + different payload hash

Conflict.

### Same provider + same externalTransactionId + different idempotency key

Conflict.

Concurrency safety must rely on PostgreSQL uniqueness/atomic insertion, not only `SELECT` followed by `INSERT`.

Current safe pattern uses `INSERT ... ON CONFLICT DO NOTHING` plus persisted lookup/classification.

Payload hashing:

- SHA-256;
- deterministic typed/canonical representation;
- include business fields;
- exclude idempotency key and transport metadata;
- HTTP and SQS must generate the same hash for equivalent operations.

---

## Inbox

Inbox is for **message deduplication**, not financial idempotency.

Identity:

```text
consumerName + messageId
```

Inbox answers:

> Has this broker message already been durably handled by this consumer?

It must not use `idempotencyKey` as its identity.

For SQS processing, Inbox and the corresponding domain/ledger/outbox changes must share the same SQL transaction.

A consumed SQS message must only be deleted/acknowledged after the durable transaction commits.

Do not delete Inbox history after processing; persist completion state.

---

## Outbox

Outbox is the durable record of events that must be published after the database commit.

Outbox prevents this failure:

```text
financial COMMIT succeeds
process crashes
event is never published
```

Financial state and outbox records are committed atomically.

A separate publisher later sends pending events.

Required integration events include:

- `WagerTransactionProcessed`
- `WagerTransactionRejected`
- `WalletBalanceChanged`
- `WagerTransactionPendingReference`

Stable `eventId` must be preserved across retries/republication.

Do not publish external events before the financial commit.

The Outbox publisher must eventually support:

- multiple publishers;
- record contention;
- retry/backoff;
- abandoned work recovery;
- crash after publish but before marking published.

Do not implement exactly-once assumptions across PostgreSQL and SQS.

---

## Implemented State

The repository currently has the following major pieces implemented and tested:

### Domain

- Money
- Wallet
- WagerTransaction
- WalletLedgerEntry
- Inbox
- Outbox
- Integration events

### Database / persistence

- versioned initial migration
- pgx/v5 explicit repositories
- application repository contracts
- `TransactionManager`
- transaction-bound repository set
- real PostgreSQL integration tests
- application-level `ErrNotFound`
- wallet uniqueness conflict mapping

### CreateWallet

Implemented.

Zero initial balance:

- Wallet only
- no OPENING
- no Ledger
- no financial Outbox events

Positive initial balance:

- Wallet
- internal `OPENING` transaction in `PROCESSED`
- CREDIT ledger from zero to initial balance
- `WagerTransactionProcessed`
- `WalletBalanceChanged`
- two Outbox records

Everything is committed in one SQL transaction.

### BET / WIN / LOSS

Implemented with real PostgreSQL tests.

#### BET

- positive amount;
- pessimistic wallet lock;
- debit on sufficient funds;
- insufficient funds -> REJECTED;
- no negative balance;
- ledger only on successful debit;
- processed/rejected event behavior;
- persisted idempotency/replay.

#### WIN

- positive amount;
- credit wallet;
- ledger CREDIT;
- processed + balance-changed events.

#### LOSS

- amount exactly zero;
- no balance change;
- no wallet version increment;
- no ledger;
- `WagerTransactionProcessed`;
- no `WalletBalanceChanged`.

### Concurrency/idempotency tests already proven

- two concurrent BET 80 on balance 100 -> one processed, one rejected, final 20, one ledger;
- 50 identical concurrent BETs -> one original effect, 49 replays;
- distinct wallets continue concurrently without global lock;
- `go test ./...` passes;
- `go test -race ./...` passes;
- `go vet ./...` passes.

---

## Finalized State

The rejected-result snapshot gap, REFUND, ROLLBACK, optional WIN reference,
durable `PENDING_REFERENCE`, HTTP/OIDC, SQS/Inbox, Outbox publisher, Fx
composition, lifecycle, health, metrics and final documentation are now
implemented. Preserve the tests and decisions in `ARCHITECTURE.md`; do not
reintroduce these former gaps as future work.

Authentication remains mandatory and eliminatory: the signed identity selects
the authorized HTTP provider, Wallet operations remain internal-only, and HTTP
and SQS reuse the same financial application core.

---

## SQS Requirements To Preserve

Input queue:

- `wager-transactions.fifo`

DLQ:

- `wager-transactions-dlq.fifo`

Assume at-least-once delivery.

Consumer requirements:

- use envelope `messageId` as durable message identity;
- verify payload hash on redelivery;
- Inbox gives additional message deduplication;
- remove message only after durable commit;
- business-terminal rejection may acknowledge/remove;
- transient errors retry with backoff;
- permanent/exhausted failures reach DLQ;
- graceful shutdown stops fetching and finishes/releases in-flight work safely.

Do not create a second independent business implementation for SQS.

---

## Outbox Publisher Requirements To Preserve

The publisher is separate from financial request processing.

It must continue to:

- find pending Outbox records;
- safely support multiple publisher instances;
- avoid concurrent ownership of the same record;
- publish to SQS;
- preserve stable `eventId`;
- update attempts/backoff;
- mark publication;
- recover records after process failure.

PostgreSQL coordination uses row-level locking with `FOR UPDATE SKIP LOCKED`;
preserve the documented ownership and recovery behavior.

---

## Authentication / Authorization Requirements

Use an external OAuth2/OIDC IdP.

Keycloak is the intended local choice.

Recommended service-to-service flow:

- `client_credentials`

Rules:

- authenticated provider identity determines authorized providerId;
- provider cannot access another provider's transactions;
- wallet/internal endpoints are restricted to internal service identity;
- missing/invalid/expired credentials must be rejected before financial side effects;
- never implement local password/token issuance.

---

## Uber Fx

Fx is only composition/lifecycle infrastructure.

Do not put business logic in Fx constructors/hooks.

Fx wires:

- configuration
- pgx pool
- repositories
- transaction manager
- application use cases
- HTTP server
- auth verifier
- SQS consumer
- outbox publisher
- pending-reference worker
- health/readiness

Lifecycle hooks must shut down workers/resources cleanly.

---

## Testing Priorities

Do not stop at mocks.

Required/important real scenarios include:

- same wager 50 times concurrently -> one financial effect;
- two distinct BET 80 on balance 100 -> one processed, one rejected;
- distinct wallets process in parallel;
- run relevant scenarios with at least three independent processes/instances;
- same logical operation crossing HTTP and SQS;
- consumer crash after DB commit but before SQS delete -> redelivery without duplicate effect;
- two Outbox publishers competing;
- Outbox crash after publish but before mark -> safe republication with same eventId;
- REFUND/ROLLBACK before reference;
- retry/reference recovery after restart;
- exact replay after wallet has changed;
- final stored balance reconciles with ledger;
- real Keycloak/PostgreSQL/SQS integration;
- Fx start/stop/resource release.

Always keep `go test -race` passing.

---

## Documentation Discipline

Update `ARCHITECTURE.md` when making a meaningful technical decision.

Use this structure where useful:

- Problem
- Alternatives considered
- Decision
- Reason
- Trade-offs

Important decisions that should be documented by the end:

- Money representation
- domain/application/infrastructure boundaries
- repository placement
- transaction boundary
- pessimistic wallet locking
- idempotency strategy
- payload hashing
- replay semantics
- reversal policy
- pending-reference retry policy
- Inbox
- Outbox
- auth/authorization
- Fx composition
- graceful shutdown

---

## Avoid

Do not:

- use floats for money;
- use in-memory idempotency;
- use local mutexes as distributed financial coordination;
- publish events before DB commit;
- open independent transactions inside each repository;
- make domain entities depend on pgx;
- make application interfaces expose pgx;
- conflate Inbox with financial idempotency;
- silently trust request `providerId`;
- create duplicate ledger movements;
- update/delete ledger history;
- add large frameworks/ORMs unnecessarily;
- spend time on optional tracing/load-testing before mandatory requirements;
- over-refactor stable code under deadline pressure.

---

## Decision Priority Under Time Pressure

When choices conflict, optimize in this order:

1. no incorrect financial result;
2. no duplicate movement;
3. no negative balance;
4. correct cross-instance concurrency;
5. persistent idempotency/replay;
6. no lost committed event;
7. recoverability after crashes/restarts;
8. required auth;
9. required tests;
10. reproducible run/documentation;
11. code elegance.

When uncertain, prefer the simplest implementation that can be proven correct with PostgreSQL-backed tests.
