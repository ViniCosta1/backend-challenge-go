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

# The captured Wallet UUID is a stable unique external ID/key for this file.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-flow",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Captures]
bet_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.status" == "PROCESSED"
jsonpath "$.balance.amount" == "75.00"
jsonpath "$.balance.currency" == "BRL"
jsonpath "$.idempotentReplay" == false

POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{bet_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{bet_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-flow",
  "gameId": "game-hurl",
  "kind": "WIN",
  "money": {"amount": "10.00", "currency": "BRL"}
}
HTTP 200
[Captures]
win_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.status" == "PROCESSED"
jsonpath "$.balance.amount" == "85.00"
jsonpath "$.idempotentReplay" == false

POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{win_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{win_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-flow",
  "gameId": "game-hurl",
  "kind": "LOSS",
  "money": {"amount": "0.00", "currency": "BRL"}
}
HTTP 200
[Asserts]
jsonpath "$.status" == "PROCESSED"
jsonpath "$.balance.amount" == "85.00"
jsonpath "$.idempotentReplay" == false

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "85.00"
jsonpath "$.balance.currency" == "BRL"
jsonpath "$.version" == 3
