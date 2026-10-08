# Testes HTTP com Hurl

A suíte em `tests/http` exercita a aplicação em execução e suas dependências reais. Ela não substitui os testes Go: os arquivos Hurl verificam o contrato HTTP ponta a ponta, enquanto os testes Go cobrem regras de domínio, persistência, concorrência e cenários de falha com controle mais fino.

## Pré-requisitos

1. Inicie a stack completa:

   ```bash
   docker compose up --build
   ```

2. Instale o [Hurl](https://hurl.dev/docs/installation.html).
3. Crie o arquivo local de variáveis:

   ```bash
   cp tests/http/local.env.example tests/http/local.env
   ```

`tests/http/local.env` é ignorado pelo Git. O arquivo de exemplo contém apenas credenciais locais de desenvolvimento e nenhum token persistido. Cada cenário solicita seus próprios tokens por `client_credentials` ao Keycloak.

## Execução

Execute toda a suíte com:

```bash
make test-http
```

Por padrão, o target usa `hurl`, `tests/http/local.env` e todos os arquivos `.hurl`. É possível sobrescrever o executável ou o arquivo de variáveis:

```bash
make test-http HURL=/caminho/para/hurl HTTP_TEST_VARIABLES=/caminho/para/local.env
```

Os arquivos são independentes e podem ser executados em paralelo pelo modo `--test` do Hurl.

## Cenários

- `00_health.hurl`: liveness, readiness e métricas.
- `01_auth.hurl`: tokens reais dos três clientes e rejeição de credenciais ausentes ou inválidas.
- `02_wallet.hurl`: criação, consulta, conflito de unicidade e autorização interna.
- `03_wager_flow.hurl`: fluxo BET, WIN e LOSS com conferência do saldo.
- `04_idempotency.hurl`: replay exato e conflitos de chave ou transação externa.
- `05_authorization.hurl`: isolamento entre providers e ausência de efeitos não autorizados.
- `06_rejections.hurl`: saldo insuficiente e replay com o saldo originalmente observado.
- `07_reversals.hurl`: REFUND, ROLLBACK, reversão duplicada e referência pendente.
- `08_reconciliation.hurl`: ledger paginado, reconciliação e imutabilidade do saldo.

Cada arquivo cria sua própria Wallet e captura dinamicamente IDs e tokens. Nenhum cenário depende do estado produzido por outro arquivo.
