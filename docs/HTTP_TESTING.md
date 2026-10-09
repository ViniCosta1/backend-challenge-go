# Testes HTTP com Hurl

A suíte em `tests/http` exercita a API em execução, autenticação real via
Keycloak e comportamento persistido no PostgreSQL. Ela complementa os testes
Go, sem substituir os testes de domínio, concorrência e recovery.

## Execução

Inicie a stack e prepare o arquivo local de variáveis:

```bash
docker compose up --build
cp tests/http/local.env.example tests/http/local.env
make test-http
```

`tests/http/local.env` é ignorado pelo Git. Cada cenário solicita seus próprios
tokens ao Keycloak; tokens não são persistidos no repositório.

Para usar outro executável ou arquivo de variáveis:

```bash
make test-http HURL=/caminho/para/hurl HTTP_TEST_VARIABLES=/caminho/para/local.env
```

## Cobertura

- `00_health.hurl`: liveness, readiness e métricas.
- `01_auth.hurl`: service accounts reais e credenciais inválidas.
- `02_wallet.hurl`: criação, consulta, unicidade e autorização interna.
- `03_wager_flow.hurl`: BET, WIN e LOSS.
- `04_idempotency.hurl`: replay e conflitos de identidade.
- `05_authorization.hurl`: isolamento de providers e ausência de efeitos indevidos.
- `06_rejections.hurl`: saldo insuficiente e replay exato da rejeição.
- `07_reversals.hurl`: REFUND, ROLLBACK, reversão duplicada e referência pendente.
- `08_reconciliation.hurl`: paginação do Ledger e reconciliação.

Cada arquivo cria seus próprios dados e tokens, sem depender da ordem de
execução dos demais.
