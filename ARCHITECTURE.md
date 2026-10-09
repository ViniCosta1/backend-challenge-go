# Decisões de Arquitetura

## Limites entre pacotes

- `internal/domain`: regras financeiras, entidades, Value Objects e eventos.
- `internal/application`: casos de uso, contratos de repositório e transações.
- `internal/infrastructure`: adapters PostgreSQL, OIDC, SQS e workers.
- `internal/transport`: parsing HTTP, autorização e responses.
- `internal/bootstrap`: composição e lifecycle com Uber Fx.

O domínio não depende de infraestrutura ou transporte. Repositórios não abrem
nem confirmam transações.

## Money, Wallet e Ledger

`Money` armazena unidades mínimas em `int64` e carrega a moeda. Valores externos
devem ser strings decimais sem sinal com exatamente duas casas. Parsing e
aritmética rejeitam formato inválido, moedas incompatíveis e overflow. O fluxo
financeiro atual aceita somente BRL.

Wallet protege o saldo não negativo. A versão começa em 1 e só incrementa
quando o saldo muda. Criação e reidratação validam Money, versão e timestamps
UTC ordenados.

Entradas do Ledger validam `balanceAfter = balanceBefore ± money`. O PostgreSQL
garante uma entrada por Wallet/transação e rejeita UPDATE ou DELETE por meio de
trigger append-only.

## Transações

`TransactionManager.WithinTransaction` abre uma única `pgx.Tx` e fornece um
conjunto de repositórios vinculado a ela. Wallet, WagerTransaction, Ledger,
Inbox e Outbox compartilham o mesmo commit quando aplicável. A application não
recebe `pgx.Tx` diretamente.

Uma Wallet com saldo inicial positivo persiste Wallet, OPENING, Ledger e dois
eventos Outbox atomicamente. Saldo inicial zero persiste somente a Wallet.

## Concorrência

O processamento financeiro bloqueia a Wallet com `SELECT ... FOR UPDATE` dentro
da transação SQL. O PostgreSQL coordena processos independentes e serializa
somente operações da mesma Wallet. Não existe lock financeiro global ou em
memória.

Essa escolha prioriza previsibilidade e integridade, com o custo de espera em
Wallets muito concorridas.

## Idempotência financeira

O PostgreSQL protege de forma única:

- `(provider_id, idempotency_key)`;
- `(provider_id, external_transaction_id)`.

A reserva usa `INSERT ... ON CONFLICT DO NOTHING`, seguida de consulta e
classificação do registro persistido. Um hash SHA-256 inclui os campos de
negócio tipados e exclui a chave de idempotência e metadados de transporte.
Entradas equivalentes por HTTP e SQS produzem o mesmo hash financeiro.

Um replay compatível retorna status, failureCode e saldo original sem repetir
Wallet, Ledger ou Outbox. Payload divergente ou external ID reutilizado gera
conflito.

## Regras e estados de Wager

- BET: débito positivo; saldo insuficiente resulta em `REJECTED`.
- WIN: crédito positivo.
- LOSS: valor zero, sem alteração de saldo/versão e sem Ledger.
- REFUND: crédito integral de uma BET processada compatível.
- ROLLBACK: movimento oposto de BET, WIN ou REFUND processado.
- OPENING: crédito interno criado para saldo inicial positivo.

`PENDING` é a reserva inicial, `PENDING_REFERENCE` representa espera durável e
`PROCESSED`, `REJECTED` e `FAILED` são terminais. Falhas de negócio são
`REJECTED`; falhas transitórias de infraestrutura causam rollback e retry.

`FAILED` representa uma falha permanente de infraestrutura, mas nenhum adapter
atual consegue classificar com segurança uma falha pós-reserva como permanente.
A transição permanece suportada e testada no domínio sem criar uma segunda
transação insegura apenas para produzir esse estado.

## Referências e reversões

Operações referenciadas devem concordar em provider, player, Wallet, moeda e
round. REFUND e ROLLBACK também exigem o valor integral referenciado. O banco
permite apenas uma reversão financeira processada por referência.

Uma WIN pode referenciar opcionalmente uma BET processada da mesma rodada. WIN
não é reversão: seu valor é independente e ela não consome a unicidade de
reversões.

Referências ausentes ou não terminais geram `PENDING_REFERENCE` sem alterar a
Wallet. O estado de retry fica no PostgreSQL e é reclamado com
`FOR UPDATE SKIP LOCKED`. Após cinco tentativas com backoff exponencial, a
operação termina com `REFERENCE_NOT_FOUND`. Incompatibilidades permanentes são
rejeitadas imediatamente.

Failure codes principais:

- `REFERENCE_MISMATCH`;
- `REFERENCE_INCOMPATIBLE`;
- `REFERENCE_ALREADY_REVERSED`;
- `REFERENCE_NOT_FOUND`;
- `ROLLBACK_INSUFFICIENT_BALANCE`.

## Inbox e entrada SQS

A identidade da Inbox é `(consumer_name, message_id)`, acompanhada do hash do
body recebido. Reserva da Inbox, processamento financeiro, Outbox e conclusão
da Inbox usam a mesma transação. DeleteMessage só ocorre após o commit.

O consumer assume entrega at-least-once. Uma reentrega após commit é resolvida
pela Inbox concluída; o mesmo messageId com conteúdo diferente é inválido. O
visibility timeout e o redrive do broker fornecem retry limitado e DLQ.

A fila de entrada é uma fronteira confiável. `providerId` é afirmado por um
produtor interno autorizado; OIDC se aplica ao HTTP. Credenciais MiniStack são
apenas valores dummy. IAM e Queue Policies de produção estão fora deste
repositório. A deduplicação FIFO não é garantia financeira.

## Outbox transacional

Estado financeiro e snapshots imutáveis dos eventos são confirmados juntos. Um
publisher separado reclama eventos vencidos usando `FOR UPDATE SKIP LOCKED`,
mantém o row lock durante SendMessage e depois registra sucesso ou
attempts/backoff.

Manter a transação aberta durante o envio simplifica ownership, mas ocupa uma
conexão durante a chamada externa. Lotes pequenos e timeouts limitam esse custo.

Se o processo cair após publicar e antes de marcar a linha, o mesmo evento será
republicado com o mesmo eventId. A garantia é at-least-once; consumidores devem
deduplicar pelo eventId.

## Autenticação e autorização

O Keycloak usa `client_credentials`. A API valida issuer, assinatura RS256,
expiração e audience por OIDC discovery/JWKS. O claim assinado `azp` define o
provider autenticado.

Providers acessam apenas suas próprias transações. Wallet, Ledger e
reconciliação exigem role interna. Consultas entre providers usam SQL escopado
e retornam 404 sem expor a existência do dado.

## Fx e shutdown

Uber Fx compõe configuração, pool pgx, OIDC, casos de uso, HTTP, SQS, workers,
logging e métricas. Os módulos Fx não contêm regras de negócio.

O startup valida configuração e PostgreSQL antes de aceitar requests. No
shutdown, HTTP e novos polls são interrompidos, trabalhos em andamento são
drenados ou cancelados, as goroutines terminam e somente então o PostgreSQL é
fechado. Operações SQL e externas usam contextos canceláveis e limitados.

## Observabilidade e health

Logs JSON incluem IDs de correlação, mensagem, evento, transação, Wallet e
provider quando disponíveis, sem tokens, secrets ou payload financeiro
completo.

Métricas Prometheus cobrem HTTP, resultados de wager, replay/conflitos, retries,
candidatos a DLQ, idade/publicação da Outbox, latência e divergência de
reconciliação. Labels não utilizam IDs de negócio.

Liveness verifica apenas o processo. Readiness verifica PostgreSQL e todas as
filas SQS obrigatórias. O Keycloak é validado no startup e na autenticação, não
em cada chamada de readiness.

## Migrations e testes

Migrations UP/DOWN versionadas definem o schema. A migration 000002 recusa um
downgrade que descartaria snapshots de rejeições. Testes PostgreSQL reais
cobrem UP/DOWN/UP, constraints e imutabilidade do Ledger.

As integrações usam PostgreSQL, Keycloak e MiniStack reais. Elas cobrem
duplicatas concorrentes, duas BET 80 sobre saldo 100, Wallets distintas, três
processos OS, redelivery da Inbox, publishers concorrentes, recovery após
publish-before-mark, referências pendentes, restart e operações cruzando HTTP
e SQS.

## Trade-offs conhecidos

- Operações da mesma Wallet aguardam o row lock.
- Entrega entre PostgreSQL e SQS é at-least-once, não exactly-once.
- Keycloak e MiniStack locais não representam hardening de produção.
- O Ledger é auditável single-entry, não contabilidade double-entry.
- Tracing distribuído não foi implementado.
