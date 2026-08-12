#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${DEPLOY_ENV_FILE:-"$repository_root/deploy-dev.env"}
admin_url=${SHINMON_ADMIN_URL:-http://127.0.0.1:4041}
listener_url=${SHINMON_LISTENER_URL:-http://127.0.0.1}

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

set -a
. "$environment_file"
set +a

authorization="Authorization: Bearer $GATEWAY_ADMIN_BEARER_TOKEN"
actor="X-Admin-Actor: stack-smoke"

api_post() {
  path=$1
  payload=$2
  curl -fsS -X POST -H "$authorization" -H "$actor" -H 'Content-Type: application/json' --data "$payload" "$admin_url$path"
}

api_post /admin/v1/permissions '{"name":"smoke-a:v1:invoke","description":"Stack smoke A"}' >/dev/null
api_post /admin/v1/permissions '{"name":"smoke-b:v1:invoke","description":"Stack smoke B"}' >/dev/null

api_post /admin/v1/services '{"environment":"development","name":"smoke-a","displayName":"Stack Smoke A","owner":"deployment-smoke"}' >/dev/null
version_a=$(api_post /admin/v1/services/smoke-a/versions '{"environment":"development","version":"v1","healthCheckPath":"/","requestTimeoutMs":5000,"maxRequestBytes":1048576}' | jq -r .id)
smoke_upstream_a=${SMOKE_UPSTREAM_A_CONTROL_IP:-10.254.241.50}
smoke_upstream_b=${SMOKE_UPSTREAM_B_CONTROL_IP:-10.254.241.51}

api_post "/admin/v1/service-versions/$version_a/upstreams" "{\"address\":\"$smoke_upstream_a\",\"port\":80,\"weight\":100,\"healthCheckPath\":\"/\"}" >/dev/null

api_post /admin/v1/services '{"environment":"development","name":"smoke-b","displayName":"Stack Smoke B","owner":"deployment-smoke"}' >/dev/null
version_b=$(api_post /admin/v1/services/smoke-b/versions '{"environment":"development","version":"v1","healthCheckPath":"/","requestTimeoutMs":5000,"maxRequestBytes":1048576}' | jq -r .id)
api_post "/admin/v1/service-versions/$version_b/upstreams" "{\"address\":\"$smoke_upstream_b\",\"port\":80,\"weight\":100,\"healthCheckPath\":\"/\"}" >/dev/null

api_post /admin/v1/listeners/allocate-port '{"environment":"development","service":"smoke-a","serviceVersion":"v1","preferredPort":4100,"requiredPermission":"smoke-a:v1:invoke","allowedMethods":["GET"]}' >/dev/null
api_post /admin/v1/listeners/allocate-port '{"environment":"development","service":"smoke-b","serviceVersion":"v1","preferredPort":4101,"requiredPermission":"smoke-b:v1:invoke","allowedMethods":["GET"]}' >/dev/null

consumer_id=$(api_post /admin/v1/consumers '{"environment":"development","name":"smoke-client","displayName":"Stack Smoke Client","permissions":["smoke-a:v1:invoke","smoke-b:v1:invoke"]}' | jq -r .id)
api_key=$(api_post "/admin/v1/consumers/$consumer_id/keys" '{"name":"Stack smoke key","permissions":["smoke-a:v1:invoke","smoke-b:v1:invoke"]}' | jq -r .key)

configuration_id=$(api_post /admin/v1/configurations '{}' | jq -r .configurationVersion)
api_post "/admin/v1/configurations/$configuration_id/validate" '{}' >/dev/null
api_post "/admin/v1/configurations/$configuration_id/activate" '{}' >/dev/null

sleep 3

response_a=$(curl -fsS -H "X-API-Key: $api_key" -H 'X-Gateway-Listener-Port: 4101' "$listener_url:4100/")
response_b=$(curl -fsS -H "X-API-Key: $api_key" -H 'X-Gateway-Listener-Port: 4100' "$listener_url:4101/")
test "$response_a" = "shinmon-smoke-upstream-a"
test "$response_b" = "shinmon-smoke-upstream-b"

edge_admin_status=$(curl -sS -o /dev/null -w '%{http_code}' -H "$authorization" "$listener_url:4100/admin/v1/services")
test "$edge_admin_status" = "401"

docker compose -f "$repository_root/gateway-docker-compose.yml" --env-file "$environment_file" stop gateway-1 >/dev/null
sleep 3
failover_response=$(curl -fsS -H "X-API-Key: $api_key" "$listener_url:4100/")
test "$failover_response" = "shinmon-smoke-upstream-a"
docker compose -f "$repository_root/gateway-docker-compose.yml" --env-file "$environment_file" start gateway-1 >/dev/null

printf 'stack smoke passed: ports=4100,4101 failover=gateway-2\n'
