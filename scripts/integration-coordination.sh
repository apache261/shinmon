#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${DEPLOY_ENV_FILE:-"$repository_root/deploy-dev.env"}
admin_url=${SHINMON_ADMIN_URL:-http://127.0.0.1:4041}
listener_url=${SHINMON_LISTENER_URL:-http://127.0.0.1:4100/}

set -a
. "$environment_file"
set +a
authorization="Authorization: Bearer $GATEWAY_ADMIN_BEARER_TOKEN"
actor="X-Admin-Actor: coordination-integration"
api_get() { curl -fsS -H "$authorization" -H "$actor" "$admin_url$1"; }
api_post() { curl -fsS -X POST -H "$authorization" -H "$actor" -H 'Content-Type: application/json' --data "$2" "$admin_url$1"; }
api_patch() { curl -fsS -X PATCH -H "$authorization" -H "$actor" -H 'Content-Type: application/json' --data "$2" "$admin_url$1"; }

listener=$(api_get /admin/v1/listeners | jq -c '.[] | select(.listenPort == 4100)')
listener_id=$(printf '%s' "$listener" | jq -r .listenerId)
row_version=$(printf '%s' "$listener" | jq -r .rowVersion)
api_patch "/admin/v1/listeners/$listener_id/policies" "{\"rateLimitPerSecond\":2,\"rateLimitBurst\":0,\"quotaRequestsPerMinute\":100,\"circuitFailureThreshold\":2,\"circuitOpenMs\":2000,\"expectedVersion\":$row_version}" >/dev/null

consumer_name="coordination-integration-$(date +%s)"
consumer=$(api_post /admin/v1/consumers "{\"environment\":\"development\",\"name\":\"$consumer_name\",\"displayName\":\"Coordination Integration Client\",\"permissions\":[\"smoke-a:v1:invoke\"]}")
consumer_id=$(printf '%s' "$consumer" | jq -r .id)
issued=$(api_post "/admin/v1/consumers/$consumer_id/keys" '{"name":"Coordination ephemeral key","permissions":["smoke-a:v1:invoke"]}')
key_id=$(printf '%s' "$issued" | jq -r .id)
api_key=$(printf '%s' "$issued" | jq -r .key)

activate_configuration() {
  candidate=$(api_post /admin/v1/configurations '{}')
  configuration_id=$(printf '%s' "$candidate" | jq -r .configurationVersion)
  api_post "/admin/v1/configurations/$configuration_id/validate" '{}' >/dev/null
  api_post "/admin/v1/configurations/$configuration_id/activate" '{}' >/dev/null
}
activate_configuration
sleep 3

statuses=""
request_number=0
while [ "$request_number" -lt 6 ]; do
  statuses="$statuses $(curl -sS -o /dev/null -w '%{http_code}' -H "X-API-Key: $api_key" "$listener_url")"
  request_number=$((request_number + 1))
done
case "$statuses" in *429*) ;; *) echo "distributed rate limit did not reject traffic: $statuses" >&2; exit 1;; esac
sleep 2

api_post "/admin/v1/keys/$key_id/revoke" '{}' >/dev/null
started=$(date +%s%3N)
status=200
while [ "$status" != 401 ]; do
  status=$(curl -sS -o /dev/null -w '%{http_code}' -H "X-API-Key: $api_key" "$listener_url")
  elapsed=$(( $(date +%s%3N) - started ))
  [ "$elapsed" -lt 1500 ] || { echo "revocation notification exceeded 1500ms" >&2; exit 1; }
  [ "$status" = 401 ] || sleep 0.05
done

replacement=$(api_post "/admin/v1/consumers/$consumer_id/keys" '{"name":"Coordination outage key","permissions":["smoke-a:v1:invoke"]}')
replacement_key=$(printf '%s' "$replacement" | jq -r .key)
activate_configuration
sleep 3

restore_redis() { docker compose -f "$repository_root/gateway-docker-compose.yml" --env-file "$environment_file" start redis >/dev/null 2>&1 || true; }
trap restore_redis EXIT INT TERM
docker compose -f "$repository_root/gateway-docker-compose.yml" --env-file "$environment_file" stop redis >/dev/null
sleep 1
test "$(curl -sS -o /dev/null -w '%{http_code}' -H "X-API-Key: $replacement_key" "$listener_url")" = 503
activate_configuration
sleep 3
loaded=$(api_get /admin/v1/gateway-instances | jq --argjson version "$configuration_id" '[.[] | select(.loadedConfigurationVersion == $version)] | length')
test "$loaded" -ge 1
restore_redis
trap - EXIT INT TERM
sleep 3
test "$(curl -sS -o /dev/null -w '%{http_code}' -H "X-API-Key: $replacement_key" "$listener_url")" = 200

listener=$(api_get /admin/v1/listeners | jq -c '.[] | select(.listenPort == 4100)')
row_version=$(printf '%s' "$listener" | jq -r .rowVersion)
api_patch "/admin/v1/listeners/$listener_id/policies" "{\"rateLimitPerSecond\":0,\"rateLimitBurst\":0,\"quotaRequestsPerMinute\":0,\"circuitFailureThreshold\":5,\"circuitOpenMs\":30000,\"expectedVersion\":$row_version}" >/dev/null
activate_configuration
sleep 3

printf 'coordination integration passed: shared-rate-limit=429 revocation_ms=%s redis-outage=fail-closed polling=fallback\n' "$elapsed"
