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

POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-ledger",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Captures]
bet_transaction_id: jsonpath "$.transactionId"

POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_token}}
Idempotency-Key: {{bet_transaction_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{bet_transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-ledger",
  "gameId": "game-hurl",
  "kind": "WIN",
  "money": {"amount": "10.00", "currency": "BRL"}
}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "85.00"

# OPENING + BET are returned on the first page; WIN remains on page two.
GET {{base_url}}/wallets/{{wallet_id}}/ledger?limit=2
Authorization: Bearer {{internal_token}}
HTTP 200
[Captures]
next_cursor: jsonpath "$.nextCursor"
[Asserts]
jsonpath "$.entries" count == 2
jsonpath "$.entries[0].direction" == "CREDIT"
jsonpath "$.entries[0].balanceBefore.amount" == "0.00"
jsonpath "$.entries[0].balanceAfter.amount" == "100.00"
jsonpath "$.entries[1].direction" == "DEBIT"
jsonpath "$.entries[1].money.amount" == "25.00"
jsonpath "$.nextCursor" isString

GET {{base_url}}/wallets/{{wallet_id}}/ledger?limit=2&cursor={{next_cursor}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.entries" count == 1
jsonpath "$.entries[0].direction" == "CREDIT"
jsonpath "$.entries[0].money.amount" == "10.00"
jsonpath "$.entries[0].balanceAfter.amount" == "85.00"
jsonpath "$.nextCursor" not exists

POST {{base_url}}/wallets/{{wallet_id}}/reconciliation
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.walletId" == "{{wallet_id}}"
jsonpath "$.storedBalance.amount" == "85.00"
jsonpath "$.calculatedBalance.amount" == "85.00"
jsonpath "$.difference.amount" == "0.00"
jsonpath "$.difference.currency" == "BRL"
jsonpath "$.consistent" == true
jsonpath "$.checkedEntries" == 3

# Reconciliation is read-only.
GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "85.00"
jsonpath "$.version" == 3
