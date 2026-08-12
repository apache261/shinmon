#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${1:-"$repository_root/deploy-dev.env.example"}

docker compose \
  -f "$repository_root/gateway-docker-compose.yml" \
  --env-file "$environment_file" \
  config --quiet

for configuration in \
  "$repository_root/deploy/haproxy/haproxy.cfg" \
  "$repository_root/deploy/haproxy/haproxy.staging.cfg" \
  "$repository_root/deploy/haproxy/haproxy.production.cfg"
do
  docker run --rm \
    -v "$configuration:/usr/local/etc/haproxy/haproxy.cfg:ro" \
    haproxy:3.2.21-alpine \
    haproxy -c -f /usr/local/etc/haproxy/haproxy.cfg
done

temporary_tls=$(mktemp -d "${TMPDIR:-/tmp}/shinmon-tls-validation.XXXXXX")
trap 'rm -rf -- "$temporary_tls"' EXIT HUP INT TERM
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj /CN=shinmon-validation \
  -keyout "$temporary_tls/edge.key" \
  -out "$temporary_tls/edge.crt" >/dev/null 2>&1
cp "$temporary_tls/edge.crt" "$temporary_tls/edge.pem"
sed -n '1,$p' "$temporary_tls/edge.key" >> "$temporary_tls/edge.pem"
chmod 755 "$temporary_tls"
chmod 644 "$temporary_tls/edge.pem"
docker run --rm \
  -v "$repository_root/deploy/haproxy/haproxy.https.production.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro" \
  -v "$temporary_tls:/etc/shinmon/tls:ro" \
  haproxy:3.2.21-alpine \
  haproxy -c -f /usr/local/etc/haproxy/haproxy.cfg
