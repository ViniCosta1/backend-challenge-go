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
provider_a_token: jsonpath "$.access_token"

POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{provider_b_client_id}}
client_secret: {{provider_b_client_secret}}
HTTP 200
[Captures]
provider_b_token: jsonpath "$.access_token"

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
Authorization: Bearer {{provider_a_token}}
Idempotency-Key: {{wallet_id}}
Content-Type: application/json
{
  "externalTransactionId": "{{wallet_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-authz",
  "gameId": "game-hurl",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
HTTP 200
[Captures]
transaction_id: jsonpath "$.transactionId"
[Asserts]
jsonpath "$.balance.amount" == "75.00"

GET {{base_url}}/wagering/transactions/{{transaction_id}}
Authorization: Bearer {{provider_a_token}}
HTTP 200
[Asserts]
jsonpath "$.providerId" == "{{provider_a_client_id}}"
jsonpath "$.status" == "PROCESSED"

# Provider B cannot observe Provider A by internal ID.
GET {{base_url}}/wagering/transactions/{{transaction_id}}
Authorization: Bearer {{provider_b_token}}
HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"

# Provider B also cannot use Provider A's provider-scoped path.
GET {{base_url}}/providers/{{provider_a_client_id}}/wagering/transactions/{{wallet_id}}
Authorization: Bearer {{provider_b_token}}
HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"

# Authenticated identity wins over a forged providerId body field.
POST {{base_url}}/wagering/transactions
Authorization: Bearer {{provider_a_token}}
Idempotency-Key: {{player_id}}
Content-Type: application/json
{
  "providerId": "{{provider_b_client_id}}",
  "externalTransactionId": "{{transaction_id}}",
  "playerId": "{{player_id}}",
  "walletId": "{{wallet_id}}",
  "roundId": "round-hurl-authz",
  "gameId": "game-hurl",
  "kind": "WIN",
  "money": {"amount": "50.00", "currency": "BRL"}
}
HTTP 403
[Asserts]
jsonpath "$.error.code" == "forbidden"

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{provider_a_token}}
HTTP 403
[Asserts]
jsonpath "$.error.code" == "forbidden"

GET {{base_url}}/wagering/transactions/{{transaction_id}}
HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"

# Unauthorized attempts created no financial effect.
GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.balance.amount" == "75.00"
jsonpath "$.version" == 2

GET {{base_url}}/wagering/transactions/{{transaction_id}}
Authorization: Bearer {{provider_a_token}}
HTTP 200
[Asserts]
jsonpath "$.status" == "PROCESSED"
jsonpath "$.resultBalance.amount" == "75.00"
