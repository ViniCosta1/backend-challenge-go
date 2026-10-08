POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{internal_client_id}}
client_secret: {{internal_client_secret}}
HTTP 200
[Captures]
internal_token: jsonpath "$.access_token"

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
[Asserts]
jsonpath "$.id" isUuid
jsonpath "$.playerId" isUuid
jsonpath "$.balance.amount" == "100.00"
jsonpath "$.balance.currency" == "BRL"
jsonpath "$.version" == 1
header "Location" == "/wallets/{{wallet_id}}"

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{internal_token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{wallet_id}}"
jsonpath "$.playerId" == "{{player_id}}"
jsonpath "$.balance.amount" == "100.00"
jsonpath "$.balance.currency" == "BRL"
jsonpath "$.version" == 1

# PostgreSQL uniqueness is surfaced as HTTP 409.
POST {{base_url}}/wallets
Authorization: Bearer {{internal_token}}
Content-Type: application/json
{
  "playerId": "{{player_id}}",
  "initialBalance": {"amount": "100.00", "currency": "BRL"}
}
HTTP 409
[Asserts]
jsonpath "$.error.code" == "wallet_already_exists"

POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{provider_a_client_id}}
client_secret: {{provider_a_client_secret}}
HTTP 200
[Captures]
provider_a_token: jsonpath "$.access_token"

GET {{base_url}}/wallets/{{wallet_id}}
Authorization: Bearer {{provider_a_token}}
HTTP 403
[Asserts]
jsonpath "$.error.code" == "forbidden"
