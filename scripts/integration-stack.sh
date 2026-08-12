#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${DEPLOY_ENV_FILE:-"$repository_root/deploy-dev.env"}
admin_url=${SHINMON_ADMIN_URL:-http://127.0.0.1:4041}
listener_url=${SHINMON_LISTENER_URL:-http://127.0.0.1}

set -a
. "$environment_file"
set +a

authorization="Authorization: Bearer $GATEWAY_ADMIN_BEARER_TOKEN"
actor="X-Admin-Actor: stack-integration"
api_post() { curl -fsS -X POST -H "$authorization" -H "$actor" -H 'Content-Type: application/json' --data "$2" "$admin_url$1"; }

consumer_name="stack-integration-$(date +%s)"
consumer=$(api_post /admin/v1/consumers "{\"environment\":\"development\",\"name\":\"$consumer_name\",\"displayName\":\"Stack Integration Client\",\"permissions\":[\"smoke-a:v1:invoke\",\"smoke-b:v1:invoke\"]}")
consumer_id=$(printf '%s' "$consumer" | jq -r .id)
issued=$(api_post "/admin/v1/consumers/$consumer_id/keys" '{"name":"Stack ephemeral integration key","permissions":["smoke-a:v1:invoke","smoke-b:v1:invoke"]}')
api_key=$(printf '%s' "$issued" | jq -r .key)

candidate=$(api_post /admin/v1/configurations '{}')
configuration_id=$(printf '%s' "$candidate" | jq -r .configurationVersion)
api_post "/admin/v1/configurations/$configuration_id/validate" '{}' >/dev/null
api_post "/admin/v1/configurations/$configuration_id/activate" '{}' >/dev/null
sleep 3

test "$(curl -fsS -H "X-API-Key: $api_key" -H 'X-Gateway-Listener-Port: 4101' "$listener_url:4100/")" = shinmon-smoke-upstream-a
test "$(curl -fsS -H "X-API-Key: $api_key" -H 'X-Gateway-Listener-Port: 4100' "$listener_url:4101/")" = shinmon-smoke-upstream-b

restore_replica() {
  docker compose -f "$repository_root/gateway-docker-compose.yml" --env-file "$environment_file" start gateway-1 >/dev/null 2>&1 || true
}
trap restore_replica EXIT INT TERM
docker compose -f "$repository_root/gateway-docker-compose.yml" --env-file "$environment_file" stop gateway-1 >/dev/null
sleep 3
test "$(curl -fsS -H "X-API-Key: $api_key" "$listener_url:4100/")" = shinmon-smoke-upstream-a
restore_replica
trap - EXIT INT TERM

printf 'stack integration passed: forged-header=overwritten replica-failover=healthy\n'
