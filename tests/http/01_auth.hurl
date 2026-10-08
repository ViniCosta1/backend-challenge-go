# Real client_credentials grants for every local service identity.
POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{internal_client_id}}
client_secret: {{internal_client_secret}}
HTTP 200
[Captures]
internal_token: jsonpath "$.access_token"
[Asserts]
jsonpath "$.access_token" isString
jsonpath "$.token_type" == "Bearer"

POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{provider_a_client_id}}
client_secret: {{provider_a_client_secret}}
HTTP 200
[Captures]
provider_a_token: jsonpath "$.access_token"
[Asserts]
jsonpath "$.access_token" isString
jsonpath "$.token_type" == "Bearer"

POST {{keycloak_url}}/realms/{{realm}}/protocol/openid-connect/token
[Form]
grant_type: client_credentials
client_id: {{provider_b_client_id}}
client_secret: {{provider_b_client_secret}}
HTTP 200
[Captures]
provider_b_token: jsonpath "$.access_token"
[Asserts]
jsonpath "$.access_token" isString
jsonpath "$.token_type" == "Bearer"

# Authentication is evaluated before the resource lookup.
GET {{base_url}}/wallets/{{newUuid}}
HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"
header "WWW-Authenticate" contains "Bearer"

GET {{base_url}}/wallets/{{newUuid}}
Authorization: Bearer definitely-not-a-jwt
HTTP 401
[Asserts]
jsonpath "$.error.code" == "unauthorized"
