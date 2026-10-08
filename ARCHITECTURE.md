# Decisões de arquitetura

## Limites entre pacotes

`internal/domain` contém Value Objects, entidades, invariantes e modelos de
eventos. Ele não importa HTTP, PostgreSQL/pgx, SQS, Keycloak ou Fx.
`internal/application` coordena casos de uso e declara os contratos que
consome. `internal/infrastructure` implementa PostgreSQL, OIDC e SQS;
`internal/transport` adapta HTTP. Essa direção mantém regras financeiras
testáveis sem transformar entidades em modelos de persistência.

## Money, Wallet e Ledger

`Money` usa `int64` em unidades mínimas e carrega a moeda. A representação
externa aceita somente decimal sem sinal, com exatamente duas casas; não há
float no fluxo monetário. O parser valida formato e overflow antes da
conversão. Aritmética rejeita moedas incompatíveis e overflow/underflow. O
fluxo atual aceita BRL e os mesmos limites são reforçados pelo PostgreSQL.

Wallet encapsula saldo e versão: saldo nunca é negativo, versão começa em 1 e
só cresce quando Debit/Credit efetivamente muda o saldo. Criação e reidratação
validam Money, versão e timestamps UTC ordenados. Saldo inicial positivo gera
OPENING; zero não fabrica movimentação.

O Ledger é a trilha auditável imutável. Cada entrada valida `after = before ±
money`, moeda e movimento positivo. A unicidade `(wallet_id, transaction_id)`
evita movimento duplicado e um trigger PostgreSQL rejeita UPDATE e DELETE. A
defesa é testada contra um PostgreSQL real, incluindo a preservação da linha
após ambas as tentativas.

## Controle de concorrência da carteira

Problema
--------

Duas instâncias podem modificar a mesma carteira simultaneamente.

Alternativas consideradas
--------------------------

- lock pessimista
- optimistic locking
- update condicional

Decisão
-------

Lock pessimista por carteira com `SELECT ... FOR UPDATE`. O lock é adquirido
dentro da mesma transação SQL que atualiza Wallet, WagerTransaction, Ledger e
Outbox, e é liberado no commit/rollback.

Motivo
------

O PostgreSQL coordena processos e instâncias sem memória compartilhada. O lock
por linha serializa somente operações da mesma Wallet e impede lost updates,
enquanto carteiras distintas continuam avançando em paralelo.

Trade-offs
----------

Operações concorrentes na mesma Wallet aguardam o lock. Em troca da
previsibilidade, as transações precisam permanecer curtas para reduzir espera e
contenção; não há mutex nem lock global na aplicação.

## Persistência e transações

O pacote `domain` preserva as regras de negócio e não conhece PostgreSQL ou
`pgx`. Os contratos de repositório pertencem a `application`, enquanto suas
implementações SQL explícitas ficam em `infrastructure/postgres`.

Uma operação que precisa alterar mais de um modelo usa
`TransactionManager.WithinTransaction`. O manager abre uma única `pgx.Tx` e
entrega ao callback um conjunto de repositórios vinculados a essa mesma
transação. Os repositórios apenas executam comandos: eles não iniciam,
confirmam nem revertem transações.

A Inbox deduplica a entrega de uma mensagem pelo par `consumerName` e
`messageId`; ela não substitui a idempotência financeira, que é consultada em
`WagerTransaction` por provedor e chave/identificador externo. A Outbox guarda
snapshots duráveis dos eventos na mesma transação dos dados que os originam,
deixando a publicação pós-commit para um publisher separado.

### Abertura de carteira

`CreateWallet` é coordenado pela camada `application`. Uma abertura com saldo
positivo persiste Wallet, `OPENING`, Ledger e os dois eventos na Outbox de forma
atômica; com saldo zero, persiste somente a Wallet. A garantia definitiva de
uma carteira por jogador e moeda é a constraint PostgreSQL
`uq_wallets_player_currency`.

### Processamento financeiro e idempotência

`ProcessWagerTransaction`, na camada `application`, coordena BET, WIN, LOSS,
REFUND e ROLLBACK.
A idempotência financeira é persistida em `wager_transactions` pelos pares
`(provider_id, idempotency_key)` e
`(provider_id, external_transaction_id)`, combinados com `payload_hash`. A
reserva usa `INSERT ... ON CONFLICT DO NOTHING`; assim, as constraints do
PostgreSQL arbitram corridas entre instâncias sem deixar a transação atual em
estado abortado. Um replay compatível lê o `result_balance` original e não
repete Wallet, Ledger ou Outbox. Isso inclui rejeições definitivas: todas as
novas rejeições que observam uma Wallet persistem seu saldo e failureCode.
`HasResultBalance` distingue resultados financeiros de `PENDING_REFERENCE`,
cujo replay devolve identidade e estado persistidos, sem inventar um saldo.

O hash é SHA-256 hexadecimal sobre JSON produzido por uma struct tipada, com
ordem fixa dos campos: `providerId`, `externalTransactionId`, `playerId`,
`walletId`, `roundId`, `gameId`, `kind`, `money` e
`referenceExternalTransactionId`. Strings são preservadas exatamente, uma
referência ausente vira string vazia e `Money` usa seu JSON canônico com valor
decimal de duas casas e moeda. `idempotencyKey` e metadados de transporte não
participam do hash, garantindo a mesma representação para entradas HTTP e SQS.

### Estados e classificação de falhas

`PENDING` é a reserva inicial, `PENDING_REFERENCE` é espera durável,
`PROCESSED` e `REJECTED` são resultados terminais financeiros/de negócio, e
`FAILED` representa uma falha permanente de infraestrutura já associada a uma
operação. Erros transitórios de PostgreSQL, SQS ou cancelamento revertem a
transação e deixam o broker/chamador tentar novamente; não são convertidos em
rejeição. Validações determinísticas, saldo insuficiente e referências
incompatíveis são `REJECTED` com failureCode estável.

O domínio protege e testa a transição para `FAILED`, mas nenhum adapter atual
classifica uma falha de infraestrutura pós-reserva como seguramente permanente.
Persistir FAILED exigiria saber que o erro não é transitório e fazê-lo sem uma
segunda transação que viole a atomicidade. Por isso o runtime atual não força
um caminho artificial para esse estado.

### WIN com referência opcional

WIN sem referência preserva o fluxo original. Quando informa
`referenceExternalTransactionId`, a aplicação resolve no mesmo mecanismo
usado pelas referências duráveis. A referência precisa ser uma BET PROCESSED
do mesmo provider, player, Wallet, currency e round. O valor da WIN é
independente do valor apostado: WIN não é reversão e não participa do índice
único de reversões, portanto não impede REFUND/ROLLBACK posterior da BET.

Referência ausente ou ainda não terminal leva a `PENDING_REFERENCE` e é
retomada pelo worker persistente. BET terminal rejeitada/failed, tipo incorreto
ou dados divergentes terminam a WIN como `REJECTED` usando
`REFERENCE_INCOMPATIBLE` ou `REFERENCE_MISMATCH`. Replay continua usando o
resultado original persistido.

### Reversões financeiras

REFUND credita integralmente uma BET `PROCESSED`. ROLLBACK desfaz uma BET por
crédito, ou uma WIN/REFUND por débito integral. A referência é localizada por
`(provider_id, reference_external_transaction_id)` e precisa concordar em
provedor, jogador, Wallet, moeda, rodada e valor. Uma referência terminal sem
sucesso ou de tipo incompatível causa rejeição definitiva; divergências de
dados também são definitivas. Todas as moedas continuam limitadas a BRL pelo
Money e pelo banco; valores de outra moeda são recusados antes de persistir
uma operação. Uma identidade externa que existe somente em outro provedor
não identifica uma referência deste provedor e segue a política de ausência.

O índice `ux_wager_transactions_processed_reversal_reference` permite somente
uma reversão `PROCESSED` por referência, somando REFUND e ROLLBACK. Depois de
REFUND de uma BET, outro REFUND ou ROLLBACK da mesma BET é rejeitado. É possível
fazer um ROLLBACK do próprio REFUND: ele referencia o crédito do REFUND e gera
um débito. Isso não libera a BET original para uma nova devolução.

A Wallet permanece bloqueada com `SELECT ... FOR UPDATE` enquanto a aplicação
valida a referência e persiste o movimento, status, Ledger e Outbox. A mudança
do status da reversão para `PROCESSED` é protegida por savepoint: uma violação
do índice único é convertida em `REFERENCE_ALREADY_REVERSED`, com rejeição e
evento no mesmo commit, sem persistir saldo nem ledger da tentativa duplicada.
Os repositórios não abrem nem confirmam transações; o savepoint contém somente
a atualização SQL do status. A FK de WagerTransaction para Wallet é verificada
no commit (DEFERRABLE INITIALLY DEFERRED), evitando upgrade concorrente de
locks KEY SHARE adquiridos no INSERT para o lock financeiro FOR UPDATE.

### Referências pendentes

Referências ausentes ou ainda não terminais compatíveis geram
`PENDING_REFERENCE` e um único `WagerTransactionPendingReference` na Outbox.
Não há movimentação, incremento de versão ou Ledger. O PostgreSQL guarda
`reference_attempts` e `reference_next_attempt_at`; a migration 000003 exige
um horário agendado enquanto o registro estiver pendente e nenhum horário
agendado depois da conclusão. Inconsistências de dados/tipo já identificáveis
são rejeitadas imediatamente, mesmo quando a referência ainda não é terminal.

`RetryPendingReferences.Execute(ctx, now, limit)` faz uma passagem limitada:
busca itens vencidos (`reference_next_attempt_at <= now`), reclama um por
transação com `FOR UPDATE SKIP LOCKED` e reutiliza o processamento financeiro
com o lock da Wallet. Instâncias concorrentes não reclamam a mesma pendência.
Uma instância nova retoma exclusivamente o estado do banco; o agendamento
periódico das chamadas pertence ao runtime, sem scheduler em memória neste
serviço. Cada tentativa, próximo horário, resultado e evento são confirmados
atomicamente; erros de persistência revertem também o contador da tentativa.

São cinco retentativas, com esperas de 1, 2, 4, 8 e 16 segundos. A criação da
pendência começa com contador zero e agenda a primeira tentativa para +1s.
Cada retentativa incrementa o contador; se a referência continuar ausente ou
não terminal na quinta, a operação é rejeitada com `REFERENCE_NOT_FOUND` e
saldo observado persistido. Se a referência já estiver utilizável na quinta,
o processamento ainda pode concluir. O evento de pendência não é repetido a
cada retry; conclusão ou rejeição produz o evento terminal correspondente.

Failure codes estáveis:

- `INSUFFICIENT_BALANCE`: BET sem saldo.
- `ROLLBACK_INSUFFICIENT_BALANCE`: ROLLBACK que precisa debitar sem saldo.
- `REFERENCE_MISMATCH`: dados/valor da referência ou jogador da Wallet divergem.
- `REFERENCE_INCOMPATIBLE`: referência terminal sem sucesso ou tipo não reversível.
- `REFERENCE_ALREADY_REVERSED`: o índice único impede uma segunda reversão.
- `REFERENCE_NOT_FOUND`: referência ausente/não utilizável após cinco tentativas.

### Evolução do resultado persistido

A migration 000002 amplia a constraint para permitir `result_balance` em
`REJECTED`; a migration 000001 permanece intacta. Rejeições antigas sem
snapshot não são preenchidas com o saldo atual, pois isso fabricaria um
resultado histórico. Seu replay retorna `ErrWagerReplayResultUnavailable`.
O DOWN de 000002 restaura a constraint anterior e falha se já existirem
snapshots de rejeição, preservando dados auditáveis em vez de apagá-los.

### HTTP e autenticação OIDC

`internal/transport/httpapi` adapta requests/DTOs aos casos de uso existentes;
não executa SQL nem duplica regras financeiras. Usa `net/http`, JSON estrito,
limite de body de 1 MiB e valores monetários em strings decimais com duas casas.
Os parsers do domínio continuam responsáveis pela conversão monetária.
`cmd/api` executa esse adapter pela composição Fx descrita adiante.

O Keycloak local importa automaticamente
`deploy/keycloak/wager-challenge-realm.json` via `start-dev --import-realm`.
O realm dedicado `wager-challenge` oferece `client_credentials` para
`provider-a`, `provider-b` (role `provider`) e `internal-service` (role
`internal`). Secrets de desenvolvimento estão em `.env.example`; o arquivo de
importação usa placeholders das variáveis de ambiente. São exclusivamente
credenciais locais, não configuração de produção. A importação de startup
preserva um realm já existente: mudar secrets no ambiente não o sobrescreve.

`internal/infrastructure/oidc` usa `github.com/coreos/go-oidc/v3` para discovery
e validação de issuer exato, assinatura RS256 via JWKS, expiração e audience
`wager-api`, sem desabilitar verificações. O client de discovery/JWKS possui
timeout e a biblioteca suporta cache/renovação de chaves. Também exigimos
`typ=Bearer` e `azp` não vazio. Não emitimos tokens nem armazenamos senhas de
usuários da aplicação. HTTP sem TLS e o bootstrap `admin/admin` são somente
para desenvolvimento local; produção exige HTTPS e secrets próprios.

O middleware extrai a identidade de `azp` e as roles de `realm_access.roles`.
Somente a role interna permite operar/ler Wallet, ledger e reconciliação;
somente a role provider permite processar/ler wagers. Identidades com ambas
as roles são recusadas, evitando permissões ambíguas. Health é público.
O provider usado no caso de uso vem do token assinado, nunca do request:
um `providerId` opcional no body divergente recebe 403. Confiar no body
permitiria que um provider assumisse a identidade de outro.

Leitura de transação por ID inclui `provider_id` na própria query SQL;
por identidade externa, usa sempre o provider autenticado. Provider no path
divergente, transação de outro provider e transação inexistente recebem o
mesmo 404, sem revelar existência ou dados de outro provedor.

Endpoints implementados:

- `POST /wallets`: 201; duplicidade jogador/moeda: 409.
- `GET /wallets/{walletId}`, `GET /wallets/{walletId}/ledger` e
  `POST /wallets/{walletId}/reconciliation`: 200, internos.
- `POST /wagering/transactions`: 200 PROCESSED, 202 PENDING_REFERENCE,
  422 REJECTED; exige um `Idempotency-Key` não vazio, sem geração/substituição.
- `GET /wagering/transactions/{transactionId}` e
  `GET /providers/{providerId}/wagering/transactions/{externalTransactionId}`:
  200, escopo do provider.
- `GET /health/live` e `GET /health/ready`: públicos.

Entrada inválida recebe 400, Content-Type inválido 415, conflito de chave ou
identidade externa 409, credencial ausente/inválida/expirada 401, falta de
permissão 403, recurso inexistente 404 e indisponibilidade transitória do
PostgreSQL 503. Falhas inesperadas recebem 500 sem detalhes internos.
Erros usam `{"error":{"code":"...","message":"..."}}`; rejeições financeiras
usam o resultado persistido com `status`, `failureCode` e saldo observado.
Replays preservam o status HTTP da operação e `idempotentReplay=true`, sem
reconstruir respostas a partir do saldo atual. Pendências não inventam saldo.

### Paginação, reconciliação e health

Ledger usa keyset pagination crescente por `(created_at, id)`, aproveitando
o índice existente. O cursor opaco base64url contém ambos os valores e a
Wallet, impedindo reaproveitá-lo em outra carteira. Default 50, máximo 100;
uma linha adicional determina `nextCursor`. O UUID desempata timestamps
iguais. Não usamos offset; páginas sucessivas não constituem um snapshot
global congelado de todas as movimentações futuras.

`ReadWallets.Reconcile` recebe uma única query agregada PostgreSQL que observa
Wallet e soma de CREDIT menos DEBIT no mesmo snapshot MVCC, incluindo OPENING.
A soma usa NUMERIC exato no SQL, convertido explicitamente para int64/Money,
sem ponto flutuante. Retorna saldo armazenado, calculado, diferença
`stored - calculated`, consistência e quantidade de entradas. Divergências
retornam normalmente e geram log JSON estruturado; nunca corrigem saldo.
Cada divergência também incrementa uma métrica sem identificar a Wallet.

Liveness verifica somente o processo. `application.Readiness` recebe checks
nomeados independentes do transporte. `cmd/api` registra `pool.Ping` real do
PostgreSQL e acesso real às três filas SQS (resolução de URL quando necessária,
GetQueueAttributes, ARN e FIFO). Readiness devolve 200 somente se ambos passam;
caso contrário, 503, com `scope=configured_dependencies` e checks `postgres`
e `sqs`. Uma fila ausente também torna SQS indisponível. Liveness não depende
dessas consultas.

### Execução e testes locais da API

Com `.env` configurado a partir de `.env.example`, subir dependências com
`docker compose up -d` (ou `sudo docker compose up -d` quando necessário),
aplicar migrations com `make migrate-up` e executar a API:

```sh
set -a
source .env
set +a
go run ./cmd/api
```

Obter access token real (trocar client/secret para a identidade desejada):

```sh
curl --fail -X POST \
  "$OIDC_ISSUER_URL/protocol/openid-connect/token" \
  -d grant_type=client_credentials \
  -d client_id=provider-a \
  --data-urlencode "client_secret=$PROVIDER_A_CLIENT_SECRET"
```

`POSTGRES_TEST_DATABASE_URL` deve apontar para um banco de testes dedicado
com migrations aplicadas. `KEYCLOAK_TEST_ISSUER_URL` deve apontar para o realm
provisionado. Com ambas exportadas, `go test ./...` e `go test -race ./...`
executam também os testes HTTP com PostgreSQL e Keycloak reais; sem elas,
esses testes são explicitamente skipped, não substituídos por JWTs fake.
Fixtures HTTP isolam tabelas em schemas temporários e removem só seus schemas.
O realm local inclui os clientes de teste `auth-expiry-test` (token 2 segundos),
`auth-no-role-test` e `auth-wrong-audience-test`, com secrets locais
`<client-id>-local-secret`. Eles não devem ser provisionados em produção.
Os testes cobrem autorização, expiração/assinatura/audience inválidas,
isolamento de providers e ausência de efeitos financeiros não autorizados,
contratos HTTP, replay original inclusive rejeições, cursores com timestamps
iguais, reconciliação e indisponibilidade real do pool PostgreSQL.

### Filas e adapter SQS

`deploy/ministack/ready.d/01-create-queues.sh`, montado pelo Compose em
`/docker-entrypoint-initaws.d`, provisiona automaticamente e de forma
idempotente `wager-transactions.fifo`, `wager-transactions-dlq.fifo` e
`wager-events.fifo`. Usa o AWS CLI incluído no MiniStack, depois de o gateway
estar pronto. Para LocalStack, o mesmo script pode ser montado em
`/etc/localstack/init/ready.d`.
[Referência do hook MiniStack](https://ministack.org/blog/aws-cli-init-scripts).
O bootstrap preserva mensagens de filas existentes; não faz purge nem seeds.

`internal/infrastructure/sqs` usa AWS SDK for Go v2 com `BaseEndpoint` local,
region, credenciais dummy e nomes/URLs em `.env.example`. URLs explícitas
sobrescrevem resolução por nome. O endpoint padrão local é localhost:4566;
`SQS_ENDPOINT_URL` explicitamente vazio usa endpoints AWS e a cadeia normal
de credenciais quando nenhuma credencial estática é fornecida. Não há SDK
AWS ou configuração de filas na application/domain.

Produtores usam `MessageGroupId=walletId` e
`MessageDeduplicationId=messageId` do envelope de entrada. Isso permite
Wallets diferentes avançarem sem um grupo global. Na saída, o adapter envia
o snapshot persistido com `MessageGroupId=aggregateId` e
`MessageDeduplicationId=eventId`. Não há garantia de ordenação global entre
aggregates; a política de escolha/locking do publisher permite paralelismo.
A deduplicação temporária FIFO é uma otimização, nunca a garantia financeira:
correção depende das constraints PostgreSQL, Inbox e idempotência de wagers.

O envelope tipado `WagerTransactionRequested` exige campos de identidade,
timestamp RFC3339 e Money decimal string, sem floats. Dados financeiros são
convertidos pelos parsers existentes e enviados ao mesmo core usado por HTTP.

#### Trust boundary da entrada SQS

A fila de entrada é uma fronteira de broker confiável. O produtor é tratado
como serviço interno autorizado e `data.providerId` é a identidade de negócio
afirmada por ele; OIDC autentica os endpoints HTTP, não cada mensagem. No
MiniStack, access key/secret são valores dummy exigidos pela compatibilidade da
API AWS. IAM, Queue Policy e STS de produção ficam deliberadamente fora do
escopo desta solução e precisam ser definidos pelo ambiente de deployment.
Isso não torna FIFO uma garantia financeira: Inbox, idempotência persistente,
constraints e lock PostgreSQL continuam obrigatórios mesmo com produtor
confiável.

### Inbox transacional e retry de entrada

`ProcessWagerMessage` abre uma única transação, reserva a Inbox com
`INSERT ... ON CONFLICT (consumer_name, message_id) DO NOTHING`, lê a linha
com `FOR UPDATE`, verifica o hash e chama o financeiro usando os mesmos
`TransactionRepositories`. A PK arbitra criação concorrente; o lock também
serializa recuperação de registros existentes ainda não concluídos.
Wallet, WagerTransaction, Ledger, Outbox e conclusão da Inbox compartilham
esse commit. A refatoração de `ProcessWagerTransaction` separou preparação e
execução com repositories; HTTP mantém seu `Execute` transacional. Não há
nested transactions nem uma segunda implementação de regras financeiras.

O consumerName padrão da composição é `wager-transaction-consumer`, estável
entre instâncias/restarts. Inbox usa `consumerName + messageId` do envelope,
nunca o MessageId do broker nem idempotencyKey. Seu hash é SHA-256 hexadecimal
dos bytes UTF-8 exatos do body, incluindo metadados e chave: redelivery deve
preservar o envelope serializado, inclusive whitespace/ordem das chaves.
O hash financeiro permanece o JSON canônico tipado já documentado, excluindo
chave e transporte. Inbox completed com mesmo hash é ack seguro sem executar
o financeiro; mesmo messageId com hash diferente é permanente e vai à DLQ.

PROCESSED, REJECTED, PENDING_REFERENCE e replay financeiro completam a Inbox
e permitem DeleteMessage somente depois de `Execute` confirmar o commit.
Pendências continuam pelo worker PostgreSQL existente. Crash depois do commit
e antes do delete é recuperado por redelivery/Inbox, sem novos efeitos.
Falha antes do commit reverte inclusive a Inbox e não apaga a mensagem.

Visibility é 30s; o processamento tem timeout de 20s e delete/change visibility
de até 5s. O consumer faz long polling de 20s, uma mensagem por passagem;
múltiplas instâncias fornecem paralelismo. Falhas usam ChangeMessageVisibility
com backoff de 1, 2, 4, 8, 16... segundos limitado a 30s. `maxReceiveCount=5`
na política redrive envia falhas persistentes, inclusive envelopes inválidos
e conflitos permanentes, à DLQ. Não fazemos retries financeiros agressivos
em memória nem remoção antecipada de mensagens inválidas. Retenção: entrada
e saída quatro dias; DLQ quatorze dias. SDK faz uma tentativa por operação;
o retry durável pertence ao broker/Outbox.

### Publicação da Outbox e lifecycle

`PublishOutbox.Execute(ctx, cutoff, limit)` seleciona eventos devidos, um por
transação, com `published_at IS NULL`, `next_attempt_at <= cutoff` e
`FOR UPDATE SKIP LOCKED`. O limite é 1–100; o polling de `Publisher` usa um
evento por passagem para shutdown previsível. Publishers concorrentes usam
conexões/transações independentes e não assumem uma mesma linha bloqueada.

O row lock é mantido durante SendMessage (timeout 5s), simplificando ownership:
crash encerra a transação e libera o lock automaticamente, sem leases em
memória/colunas extras. Trade-off: conexão e transação ficam abertas durante
a chamada externa. Sucesso marca publishedAt; falha mantém pendente e
persiste attempts/nextAttemptAt. Attempts conta todas as tentativas de envio,
inclusive sucesso. Backoff começa em 1s, dobra e limita em 5min, sem apagar
eventos após falhas. Payload/envelope/eventId não são alterados por retries.

Se o envio ocorrer e o processo morrer antes do mark/commit, o banco continua
pendente e outro publisher envia o mesmo snapshot/eventId. O SQS pode suprimir
essa duplicata na janela FIFO, mas consumidores de saída ainda devem deduplicar
por eventId: a garantia é at-least-once, não exactly-once entre SQL e SQS.

Consumer e Publisher expõem `Start(ctx)`/`Stop(ctx)`. Stop cancela novos polls
e permite trabalho atual concluir dentro do deadline; quando ele esgota,
cancela o contexto de trabalho sem ack de mensagem não commitada e aguarda a
goroutine realmente sair antes de retornar. Publisher e pending-reference
worker usam a mesma barreira de término. Assim o hook seguinte pode fechar o
pool sem uma goroutine ainda capaz de utilizá-lo. Operações externas/SQL são
context-aware e têm timeouts próprios, evitando espera infinita após o deadline
de drain. Uma entrega não apagada volta a ficar disponível após sua visibility.
O mutex local controla somente lifecycle, nunca correção financeira/ownership
distribuído. A composição Fx inicia consumer, publisher e worker de referências.

### Testes reais de messaging

`go test ./...` pode pular integrações quando variáveis externas não existem,
mantendo o ciclo unitário rápido. A verificação obrigatória usa
`make test-integration`: ela exige `POSTGRES_TEST_DATABASE_URL`,
`KEYCLOAK_TEST_ISSUER_URL` e `SQS_TEST_ENDPOINT_URL` (com fallbacks explícitos
do `.env`), faz preflight e falha se qualquer suíte real for pulada por ausência
de infraestrutura. Fixtures criam/removem schemas e filas temporárias próprias,
sem substituir PostgreSQL, Keycloak ou MiniStack por mocks.

Os testes reais comprovam UP/DOWN/UP em schema limpo, proteção append-only,
redelivery pós-commit, hash conflitante, rollback
na conclusão da Inbox após escritas financeiras, PROCESSED/REJECTED/LOSS e
PENDING_REFERENCE, recuperação da referência, DLQ após cinco recebimentos,
três processos consumidores independentes, dois publishers com locks
simultâneos em linhas distintas, publish-before-mark, backoff persistente e
reconstrução de componentes. HTTP→SQS e SQS→HTTP compartilham um único efeito
financeiro usando Keycloak real. Readiness falha com fila ausente e Stop
é testado tanto por drain quanto por deadline/rollback. Alguns testes forçam
group/dedup IDs distintos para demonstrar independência da deduplicação FIFO.

### Composição executável com Uber Fx

`cmd/api` contém somente `bootstrap.New().Run()`. O bootstrap é dividido em
módulos Fx de configuração, PostgreSQL, autenticação, application, HTTP,
messaging e observabilidade. Construtores são registrados com `fx.Provide`;
o único `fx.Invoke` registra o comportamento do runtime. O container não é
exposto como service locator e nenhum módulo contém regra financeira.

A configuração é lida e validada uma vez antes dos hooks de startup. Ela
centraliza endereço/timeouts HTTP, conexão/limite do pool, issuer/audience,
endpoint/credenciais/filas SQS, polling/batches dos workers, nível de log e
deadlines de startup/shutdown. Campos obrigatórios ausentes, durations
inválidas, credenciais AWS incompletas ou timeout de processamento incompatível
com visibility interrompem a construção com erro claro. Secrets continuam
somente no ambiente; `.env.example` possui valores locais não sensíveis.

O pool pgx registra seu hook primeiro e valida PostgreSQL no OnStart. Um hook
de runtime cria um contexto de longa duração e inicia pending-reference worker,
Outbox Publisher, SQS Consumer e, por último, o listener HTTP. Se algum Start
falha, os componentes já iniciados são encerrados. Fx executa hooks OnStop na
ordem inversa: o runtime para primeiro novas entradas HTTP, depois novos
receives SQS, polling de Outbox e ciclos de referência, drenando trabalho em
andamento até `SHUTDOWN_TIMEOUT`; só depois o hook do PostgreSQL fecha o pool.
O contexto de OnStart nunca é reutilizado como contexto de vida dos workers.

O listener é aberto sincronamente no OnStart para falhas de bind aparecerem
no startup, mas `Serve` roda em goroutine. Erro inesperado solicita shutdown
do Fx. OnStop usa `http.Server.Shutdown`, para de aceitar requests e espera as
em andamento. Consumer, Publisher e worker mantêm lifecycle idempotente e não
deixam ack/publicação financeira fora de seus protocolos duráveis.

### Observabilidade mínima

Logs usam `log/slog` com handler JSON, inclusive eventos internos do Fx.
Requests recebem `X-Correlation-ID`: um UUID canônico fornecido é preservado;
ausente, múltiplo ou inválido é substituído por UUID novo. Logs de request
incluem, quando conhecidos, correlationId, transactionId, walletId e providerId;
SQS inclui messageId. Tokens, credentials, client secrets e payload financeiro
completo não são registrados.

`GET /metrics` é público e usa um registry Prometheus próprio por processo.
São expostos resultados de wager por status/transport, replay idempotente,
latência financeira e HTTP, retries por componente, candidatos observados ao
redrive/DLQ, conflitos classificáveis, divergências de reconciliação, resultados
de consumo/publicação e idade do evento Outbox pendente mais antigo. Labels são
conjuntos fechados; IDs de Wallet/transação/mensagem/provider nunca são labels.
Como a DLQ é movida pelo broker, a aplicação mede a mensagem vista no último
receive configurado, e não afirma ter executado o redrive.

Readiness continua consultando PostgreSQL e as três filas SQS reais; liveness
mede somente o processo. Keycloak é validado por discovery no startup e JWKS
na autenticação, mas não é consultado em todo `/health/ready`, evitando tornar
readiness dependente de uma chamada remota por probe.

### Imagem e execução via Compose

O Dockerfile usa build multi-stage com a mesma versão Go do `go.mod`, binário
CGO-disabled e runtime Alpine não-root com certificados CA. O Compose adiciona
um serviço versionado `migrate` antes da aplicação; Keycloak realm e filas SQS
continuam provisionados automaticamente pelos hooks existentes. No ambiente
Linux local, o serviço app usa host networking porque o issuer público do
Keycloak é `localhost:8081` e a validação OIDC exige correspondência exata.
Nenhum secret real é incluído.

Os testes de bootstrap sobem a composição Fx real em schema e filas temporárias,
validam live/ready/metrics, token Keycloak, Wallet e wager HTTP, wager SQS,
Inbox, Outbox publicada e fechamento de HTTP/consumer/publisher/worker antes do
pool. O teste distribuído executa o próprio binário de teste três vezes como
processos OS distintos; cada filho constrói a aplicação Fx completa, listener,
pool e workers próprios sobre PostgreSQL/Keycloak/SQS comuns. Duas BETs de 80
enviadas concorrentemente a instâncias diferentes sobre saldo 100 resultam em
um PROCESSED, um REJECTED, saldo 20 e um único ledger DEBIT, consultado pela
terceira. A coordenação permanece no `SELECT ... FOR UPDATE` PostgreSQL, sem
memória compartilhada.

## Migrations e compatibilidade

Migrations SQL possuem pares UP/DOWN e são a fonte do schema. Um teste real
aplica todo o UP, executa o DOWN completo em schema limpo e reaplica o UP,
verificando tabelas, trigger e constraints. O DOWN da 000002 é propositalmente
conservador: se já houver snapshots de saldo em rejeições, a constraint antiga
é incompatível e o downgrade falha em vez de destruir histórico auditável.

## Limitações e trade-offs assumidos

- A consistência é forte por Wallet; operações concorrentes da mesma carteira
  esperam pelo row lock.
- A entrega SQS e publicação de eventos são at-least-once. Consumidores de
  integração precisam deduplicar pelo eventId estável.
- O publisher mantém uma transação aberta durante SendMessage para ownership
  simples; lotes unitários e timeout limitam o custo.
- Keycloak e MiniStack do Compose são ambientes de desenvolvimento. TLS,
  secrets, IAM e Queue Policies pertencem ao deployment de produção.
- Ledger é single-entry auditável do saldo, não contabilidade double-entry.
- Não há tracing distribuído; logs, métricas e IDs de correlação cobrem o
  escopo obrigatório atual.
