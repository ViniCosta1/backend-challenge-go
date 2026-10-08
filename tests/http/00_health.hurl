# Public process and dependency probes.
GET {{base_url}}/health/live
HTTP 200
[Asserts]
jsonpath "$.status" == "alive"
header "Content-Type" contains "application/json"

GET {{base_url}}/health/ready
HTTP 200
[Asserts]
jsonpath "$.status" == "ready"
jsonpath "$.checks.postgres" == "up"
jsonpath "$.checks.sqs" == "up"

GET {{base_url}}/metrics
HTTP 200
[Asserts]
header "Content-Type" contains "text/plain"
body contains "wager_http_requests_total"
body contains "wager_outbox_oldest_pending_age_seconds"
