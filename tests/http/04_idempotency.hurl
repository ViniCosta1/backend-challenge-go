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

# Original BET.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-idempotency",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Captures]
original_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.balance.amount" == "75.00"
jsonpath "$.idempotentReplay" == false

# Exact replay: no second debit.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-idempotency",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Asserts]
jsonpath "$.transactionId" == "{{original_transaction_id}}"
jsonpath "$.balance.amount" == "75.00"
jsonpath "$.idempotentReplay" == true

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "75.00"

# A later movement changes current balance to 85.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{original_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{original_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-idempotency",
  "gameId": "game-hurl",
  "kind": "WIN",
  "money": {"amount": "10.00", "currency": "BRL"}
}
HTTP 200
[Captures]
win_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.balance.amount" == "85.00"

# Replay still returns the originally observed 75, not current 85.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-idempotency",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "75.00"
jsonpath "$.idempotentReplay" == true

# Same key, different financial payload.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{player_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-idempotency",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "26.00", "currency": "BRL"}
}
HTTP 409
[Asserts]
jsonpath "$.error.code" == "idempotency_conflict"

# Same external transaction ID, another key.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{player_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-idempotency",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 409
[Asserts]
jsonpath "$.error.code" == "external_transaction_conflict"

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "85.00"
