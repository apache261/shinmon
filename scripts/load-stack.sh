#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${DEPLOY_ENV_FILE:-"$repository_root/deploy-dev.env"}
admin_url=${SHINMON_ADMIN_URL:-http://127.0.0.1:4041}

set -a
. "$environment_file"
set +a

authorization="Authorization: Bearer $GATEWAY_ADMIN_BEARER_TOKEN"
actor="X-Admin-Actor: stack-load"
api_get() { curl -fsS -H "$authorization" -H "$actor" "$admin_url$1"; }
api_post() { curl -fsS -X POST -H "$authorization" -H "$actor" -H 'Content-Type: application/json' --data "$2" "$admin_url$1"; }

allocated_ports=$(api_get /admin/v1/listeners | jq -r '.[].listenPort')
port=4102
while [ "$port" -le 4199 ]; do
  if ! printf '%s\n' "$allocated_ports" | grep -qx "$port"; then
    api_post /admin/v1/listeners/allocate-port "{\"environment\":\"development\",\"service\":\"smoke-a\",\"serviceVersion\":\"v1\",\"preferredPort\":$port,\"requiredPermission\":\"smoke-a:v1:invoke\",\"allowedMethods\":[\"GET\"]}" >/dev/null
  fi
  port=$((port + 1))
done

consumer_name="stack-load-$(date +%s)"
consumer_id=$(api_post /admin/v1/consumers "{\"environment\":\"development\",\"name\":\"$consumer_name\",\"displayName\":\"Stack Load Client\",\"permissions\":[\"smoke-a:v1:invoke\",\"smoke-b:v1:invoke\"]}" | jq -r .id)
api_key=$(api_post "/admin/v1/consumers/$consumer_id/keys" '{"name":"Stack ephemeral load key","permissions":["smoke-a:v1:invoke","smoke-b:v1:invoke"]}' | jq -r .key)
configuration_id=$(api_post /admin/v1/configurations '{}' | jq -r .configurationVersion)
api_post "/admin/v1/configurations/$configuration_id/validate" '{}' >/dev/null
api_post "/admin/v1/configurations/$configuration_id/activate" '{}' >/dev/null
sleep 3

SHINMON_LOAD_API_KEY="$api_key" "$repository_root/bin/shinmon-loadtest" \
  -target-template 'http://127.0.0.1:%d/' \
  -baseline-url http://127.0.0.1:4400/ \
  -port-min 4100 -port-max 4199 -requests 3000 -concurrency 30
