# Backend de Processamento de Apostas

Serviço distribuído para carteiras e processamento de apostas, construído com
Go, PostgreSQL, Keycloak, filas compatíveis com AWS SQS e Uber Fx.

O projeto prioriza integridade financeira:

- dinheiro representado com inteiros, sem ponto flutuante;
- lock PostgreSQL por Wallet;
- idempotência financeira persistente;
- Ledger imutável;
- Inbox e Outbox transacionais;
- processamento e recuperação em ambiente at-least-once.

- [Desafio original](./CHALLENGE.md)
- [Decisões de arquitetura](./ARCHITECTURE.md)

## Arquitetura

```text
HTTP + Keycloak         Entrada SQS
       \                   /
          Casos de uso
                |
             Domínio
                |
           PostgreSQL
 Wallet / Wager / Ledger / Inbox / Outbox
                |
     Workers de Outbox e referências -> Eventos SQS
```

O domínio é independente de HTTP, PostgreSQL, SQS, Keycloak e Fx. A camada de
application define casos de uso, contratos de repositório e transações; a
infraestrutura implementa os adapters.

## Pré-requisitos

- Docker Engine e Docker Compose v2
- Go 1.26.8 para desenvolvimento no host
- [Hurl](https://hurl.dev/) para os testes de contrato HTTP
- `migrate` v4 apenas para migrations executadas manualmente

## Executando o serviço

```bash
cp .env.example .env
cp tests/http/local.env.example tests/http/local.env
docker compose up --build
```

O Compose inicia PostgreSQL, Keycloak, MiniStack, migrations e a API. O realm
do Keycloak e as filas SQS são provisionados automaticamente.

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
curl --fail http://localhost:8080/metrics
```

| Serviço | Endereço |
|---|---|
| API | `http://localhost:8080` |
| PostgreSQL | `localhost:5432` |
| Keycloak | `http://localhost:8081` |
| MiniStack | `http://localhost:4566` |

As configurações disponíveis estão em [.env.example](./.env.example). Os
valores e secrets desse arquivo são exclusivos para desenvolvimento local.

## Autenticação e autorização

O Keycloak importa o realm `wager-challenge`, com audience `wager-api`, e cria
os seguintes clients com service account:

| Client | Permissão | Secret local |
|---|---|---|
| `provider-a` | Operações próprias de apostas | `provider-a-local-secret` |
| `provider-b` | Operações próprias de apostas | `provider-b-local-secret` |
| `internal-service` | Wallet, Ledger e reconciliação | `internal-service-local-secret` |

Obtendo um token com `client_credentials`:

```bash
curl --fail -X POST \
  http://localhost:8081/realms/wager-challenge/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=internal-service \
  -d client_secret=internal-service-local-secret
```

A identidade do provider é derivada do token assinado, nunca do payload HTTP.
Consultas entre providers diferentes retornam `404` sem expor dados.

## API HTTP

| Método | Caminho | Acesso |
|---|---|---|
| POST | `/wallets` | Interno |
| GET | `/wallets/{walletId}` | Interno |
| GET | `/wallets/{walletId}/ledger` | Interno |
| POST | `/wallets/{walletId}/reconciliation` | Interno |
| POST | `/wagering/transactions` | Provider |
| GET | `/wagering/transactions/{transactionId}` | Provider proprietário |
| GET | `/providers/{providerId}/wagering/transactions/{externalTransactionId}` | Provider proprietário |
| GET | `/health/live` | Público |
| GET | `/health/ready` | Público |
| GET | `/metrics` | Público |

Criando uma Wallet:

```bash
curl -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $INTERNAL_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "playerId": "11111111-1111-4111-8111-111111111111",
    "initialBalance": {"amount": "100.00", "currency": "BRL"}
  }'
```

Processando uma BET:

```bash
curl -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" \
  -H 'Idempotency-Key: bet-1' \
  -H 'Content-Type: application/json' \
  -d '{
    "externalTransactionId": "bet-1",
    "playerId": "11111111-1111-4111-8111-111111111111",
    "walletId": "WALLET_UUID",
    "roundId": "round-1",
    "gameId": "game-1",
    "kind": "BET",
    "money": {"amount": "25.00", "currency": "BRL"}
  }'
```

Repetir a mesma operação retorna o resultado original com
`idempotentReplay: true`. REFUND, ROLLBACK e WIN com referência usam o campo
`referenceExternalTransactionId`.

Money é sempre uma string com exatamente duas casas decimais e, no fluxo
atual, utiliza BRL. Valores em ponto flutuante não são aceitos.

Principais status HTTP:

- `200`: operação processada, replay ou consulta;
- `201`: Wallet criada;
- `202`: `PENDING_REFERENCE` persistido;
- `400`: entrada inválida;
- `401` / `403`: falha de autenticação ou autorização;
- `404`: recurso ausente ou ocultado entre providers;
- `409`: conflito de unicidade ou idempotência;
- `422`: rejeição definitiva de negócio;
- `503`: dependência indisponível.

## SQS

O Compose provisiona automaticamente:

- `wager-transactions.fifo`
- `wager-transactions-dlq.fifo`
- `wager-events.fifo`

A fila de entrada usa visibility timeout de 30 segundos, long polling de 20
segundos e redrive após cinco recebimentos. Produtores devem usar `walletId`
como `MessageGroupId` e `messageId` como `MessageDeduplicationId`.

O produtor SQS é considerado um serviço interno confiável. IAM e Queue Policies
de produção pertencem ao ambiente de deployment. A correção financeira não
depende da deduplicação FIFO: constraints, Inbox, idempotência e locks do
PostgreSQL continuam sendo as garantias definitivas.

## Migrations

O Compose aplica as migrations UP automaticamente. O Makefile é apenas um
atalho; para execução manual, escolha uma das opções abaixo.

Com Make:

```bash
make migrate-up
make migrate-version
make migrate-down
```

Sem Make, carregue `DATABASE_URL` e execute o `migrate` diretamente:

```bash
set -a
source .env
set +a

migrate -path internal/database/migrations -database "$DATABASE_URL" up
migrate -path internal/database/migrations -database "$DATABASE_URL" version
migrate -path internal/database/migrations -database "$DATABASE_URL" down 1
```

Para reverter mais versões, troque `1` pela quantidade desejada ou use
`MIGRATION_STEPS` com o Makefile. A migration 000002 recusa intencionalmente um
downgrade que descartaria saldos históricos de operações rejeitadas.

## Testes

```bash
make test              # testes Go padrão
make test-race         # race detector
make vet               # análise estática
make test-http         # Hurl contra a API em execução
make test-integration  # PostgreSQL, Keycloak e MiniStack reais
make verify            # verificação completa
```

Os mesmos comandos sem Make:

```bash
# Testes, race detector e análise estática
go test ./...
go test -race ./...
go vet ./...

# Contratos HTTP; requer a stack em execução e tests/http/local.env
hurl --test --variables-file tests/http/local.env tests/http/*.hurl

# Integração real
set -a
source .env
set +a
export POSTGRES_TEST_DATABASE_URL="${POSTGRES_TEST_DATABASE_URL:-$DATABASE_URL}"
export KEYCLOAK_TEST_ISSUER_URL="${KEYCLOAK_TEST_ISSUER_URL:-${OIDC_ISSUER_URL:-http://localhost:8081/realms/wager-challenge}}"
export SQS_TEST_ENDPOINT_URL="${SQS_TEST_ENDPOINT_URL:-${SQS_ENDPOINT_URL:-http://localhost:4566}}"
bash scripts/test-integration.sh
```

Para reproduzir `make verify` sem Make, execute os comandos anteriores em
sequência: testes, race, vet, integração e Hurl.

`make test-integration` verifica as dependências e falha caso uma suíte real
seja ignorada por falta de infraestrutura. Ele cobre migrations, imutabilidade
do Ledger, concorrência, recovery, mensageria e três processos OS independentes.

Consulte [HTTP_TESTING.md](./docs/HTTP_TESTING.md) para executar o Hurl.

## Garantias e limitações

- Wallet, wager, Ledger, Inbox e Outbox são atômicos quando aplicável.
- Operações da mesma Wallet são serializadas com `SELECT ... FOR UPDATE`.
- Replays financeiros retornam o resultado originalmente persistido.
- SQS e eventos de integração são at-least-once, não exactly-once.
- O eventId permanece estável durante retries e republicações.
- Referências pendentes e retries sobrevivem a reinicializações.
- Keycloak e MiniStack locais não representam hardening de produção.
- O Ledger é um histórico auditável single-entry, não contabilidade double-entry.

As justificativas e trade-offs estão em [ARCHITECTURE.md](./ARCHITECTURE.md).
