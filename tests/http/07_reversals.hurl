POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{internal_client_id}}
client_secret: {{internal_client_secret}}
HTTP 200
[Captures]
internal_token: jsonpath "$.access_token"

POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{provider_a_client_id}}
client_secret: {{provider_a_client_secret}}
HTTP 200
[Captures]
provider_token: jsonpath "$.access_token"

POST {{base_url}}/wallets
Authorization: Bearer {{internal_token}}
Content-Type: application/json
{
  "playerId": "{{newUuid}}",
  "initialBalance": {"amount": "100.00", "currency": "BRL"}
}
HTTP 201
[Captures]
wallet_id: jsonpath "$.id"
player_id: jsonpath "$.playerId"

# BET 25: 100 -> 75.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Captures]
bet_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.balance.amount" == "75.00"

# Full REFUND of the processed BET: 75 -> 100.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{bet_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{bet_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "REFUND",
  "money": {"amount": "25.00", "currency": "BRL"},
  "referenceExternalTransactionId": "{{wallet_id}}"
}
HTTP 200
[Captures]
refund_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.status" == "PROCESSED"
jsonpath "$.balance.amount" == "100.00"
jsonpath "$.idempotentReplay" == false

# Same REFUND is a replay and cannot credit twice.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{bet_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{bet_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "REFUND",
  "money": {"amount": "25.00", "currency": "BRL"},
  "referenceExternalTransactionId": "{{wallet_id}}"
}
HTTP 200
[Asserts]
jsonpath "$.transactionId" == "{{refund_transaction_id}}"
jsonpath "$.balance.amount" == "100.00"
jsonpath "$.idempotentReplay" == true

# A distinct second reversal of the same BET is terminally rejected.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{refund_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{refund_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "ROLLBACK",
  "money": {"amount": "25.00", "currency": "BRL"},
  "referenceExternalTransactionId": "{{wallet_id}}"
}
HTTP 422
[Captures]
second_reversal_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.status" == "REJECTED"
jsonpath "$.failureCode" == "REFERENCE_ALREADY_REVERSED"
jsonpath "$.balance.amount" == "100.00"

# WIN 25 followed by its opposite ROLLBACK: 100 -> 125 -> 100.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{second_reversal_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{second_reversal_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "WIN",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Captures]
win_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.balance.amount" == "125.00"

POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{win_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{win_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "ROLLBACK",
  "money": {"amount": "25.00", "currency": "BRL"},
  "referenceExternalTransactionId": "{{second_reversal_id}}"
}
HTTP 200
[Captures]
win_rollback_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.status" == "PROCESSED"
jsonpath "$.balance.amount" == "100.00"

# Unknown reference is durable PENDING_REFERENCE; this suite does not wait
# for the worker's full exhaustion policy.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{win_rollback_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{win_rollback_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-reversal",
  "gameId": "game-hurl",
  "kind": "REFUND",
  "money": {"amount": "25.00", "currency": "BRL"},
  "referenceExternalTransactionId": "{{player_id}}"
}
HTTP 202
[Asserts]
jsonpath "$.status" == "PENDING_REFERENCE"
jsonpath "$.idempotentReplay" == false
jsonpath "$.balance" not exists

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "100.00"
