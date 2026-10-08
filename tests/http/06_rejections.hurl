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
  "initialBalance": {"amount": "20.00", "currency": "BRL"}
}
HTTP 201
[Captures]
wallet_id: jsonpath "$.id"
player_id: jsonpath "$.playerId"

POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-rejected",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 422
[Captures]
rejected_transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.status" == "REJECTED"
jsonpath "$.failureCode" == "INSUFFICIENT_BALANCE"
jsonpath "$.balance.amount" == "20.00"
jsonpath "$.idempotentReplay" == false

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "20.00"
jsonpath "$.version" == 1

# Change the current Wallet balance after the definitive rejection.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{rejected_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{rejected_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-rejected",
  "gameId": "game-hurl",
  "kind": "WIN",
  "money": {"amount": "100.00", "currency": "BRL"}
}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "120.00"

# Exact replay must keep the original observed balance 20.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-rejected",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 422
[Asserts]
jsonpath "$.transactionId" == "{{rejected_transaction_id}}"
jsonpath "$.status" == "REJECTED"
jsonpath "$.failureCode" == "INSUFFICIENT_BALANCE"
jsonpath "$.balance.amount" == "20.00"
jsonpath "$.idempotentReplay" == true

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "120.00"
jsonpath "$.version" == 2
