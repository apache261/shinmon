#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
inventory=${1:-}
common_environment=${2:-}
output=${3:-}
if [ -z "$inventory" ] || [ -z "$common_environment" ] || [ -z "$output" ]; then
  echo "Usage: $0 INVENTORY COMMON_ENV OUTPUT_DIRECTORY" >&2
  exit 2
fi
if [ ! -r "$common_environment" ]; then
  echo "Common environment file is not readable: $common_environment" >&2
  exit 1
fi
inventory=$(CDPATH= cd -- "$(dirname -- "$inventory")" && pwd)/$(basename -- "$inventory")
common_environment=$(CDPATH= cd -- "$(dirname -- "$common_environment")" && pwd)/$(basename -- "$common_environment")
if [ -e "$output" ] && [ "$(find "$output" -mindepth 1 -maxdepth 1 2>/dev/null | head -n 1)" ]; then
  echo "Output directory must not exist or must be empty: $output" >&2
  exit 1
fi

"$repository_root/scripts/validate-multihost.sh" "$inventory"
# shellcheck disable=SC1090
. "$inventory"

SHINMON_TRANSPORT_MODE=${SHINMON_TRANSPORT_MODE:-http}
SHINMON_IMAGE_TAG=${SHINMON_IMAGE_TAG:-development}
SHINMON_LISTENER_PORT_MIN=${SHINMON_LISTENER_PORT_MIN:-4100}
SHINMON_LISTENER_PORT_MAX=${SHINMON_LISTENER_PORT_MAX:-4199}
GATEWAY_DISCOVERY_MODE=${GATEWAY_DISCOVERY_MODE:-dns}
GATEWAY_DISCOVERY_NAME=${GATEWAY_DISCOVERY_NAME:-gateway.service.internal}
GATEWAY_DISCOVERY_MAX_REPLICAS=${GATEWAY_DISCOVERY_MAX_REPLICAS:-8}
MANAGEMENT_DISCOVERY_NAME=${MANAGEMENT_DISCOVERY_NAME:-gateway-admin.service.internal}

for required in GATEWAY_ENVIRONMENT GATEWAY_METRICS_BEARER_TOKEN GATEWAY_API_KEY_PEPPER GATEWAY_ADMIN_BEARER_TOKEN GATEWAY_DATABASE_URL GATEWAY_REDIS_URL GATEWAY_UPSTREAM_ALLOWED_CIDRS; do
  if ! grep -q "^$required=..*" "$common_environment"; then
    echo "$required is required in the common environment file." >&2
    exit 1
  fi
done

umask 077
mkdir -p "$output"

resolve_ipv4() {
  getent ahostsv4 "$1" | awk 'NR==1 { print $1; exit }'
}

edge_cidrs=
old_ifs=$IFS
IFS=,
set -- $EDGE_HOSTS
IFS=$old_ifs
for entry do
  address=${entry#*@}
  address=$(resolve_ipv4 "$address")
  if [ -z "$edge_cidrs" ]; then edge_cidrs=$address/32; else edge_cidrs=$edge_cidrs,$address/32; fi
done

write_common_role_environment() {
  role=$1
  target=$2
  case $role in
    gateway)
      keys='GATEWAY_ENVIRONMENT GATEWAY_LOG_LEVEL GATEWAY_SHUTDOWN_TIMEOUT GATEWAY_METRICS_BEARER_TOKEN GATEWAY_API_KEY_PEPPER GATEWAY_DATABASE_URL GATEWAY_DATABASE_MIN_CONNECTIONS GATEWAY_DATABASE_MAX_CONNECTIONS GATEWAY_DATABASE_CONNECT_TIMEOUT GATEWAY_REDIS_URL GATEWAY_UPSTREAM_ALLOWED_CIDRS GATEWAY_CONFIG_POLL_INTERVAL GATEWAY_CONFIG_POLL_JITTER GATEWAY_UPSTREAM_HEALTH_INTERVAL GATEWAY_UPSTREAM_HEALTH_TIMEOUT GATEWAY_UPSTREAM_TLS_CA_FILE'
      ;;
    management)
      keys='GATEWAY_ENVIRONMENT GATEWAY_LOG_LEVEL GATEWAY_SHUTDOWN_TIMEOUT GATEWAY_METRICS_BEARER_TOKEN GATEWAY_API_KEY_PEPPER GATEWAY_ADMIN_BEARER_TOKEN GATEWAY_CONFIGURATION_APPROVALS_REQUIRED GATEWAY_DATABASE_URL GATEWAY_DATABASE_MIN_CONNECTIONS GATEWAY_DATABASE_MAX_CONNECTIONS GATEWAY_DATABASE_CONNECT_TIMEOUT GATEWAY_REDIS_URL GATEWAY_UPSTREAM_ALLOWED_CIDRS'
      ;;
    *) keys='' ;;
  esac
  for key in $keys; do
    grep "^$key=" "$common_environment" | tail -n 1 >> "$target" || true
  done
}

render_haproxy() {
  target=$1
  if [ "$SHINMON_TRANSPORT_MODE" = https ]; then
    bind_options=' ssl crt /etc/shinmon/tls/edge.pem ssl-min-ver TLSv1.2'
    forwarded_proto='    http-request set-header X-Forwarded-Proto https'
    backend_tls=" ssl verify required ca-file /etc/shinmon/tls/internal-ca.pem sni str(${GATEWAY_TLS_SERVER_NAME:-$GATEWAY_DISCOVERY_NAME}) verifyhost ${GATEWAY_TLS_SERVER_NAME:-$GATEWAY_DISCOVERY_NAME}"
  else
    bind_options=
    forwarded_proto=
    backend_tls=
  fi
  {
    echo 'global'
    echo '    log stdout format raw local0 info'
    echo '    maxconn 4096'
    echo
    echo 'defaults'
    echo '    log global'
    echo '    mode http'
    echo '    option httplog'
    echo '    option dontlognull'
    echo '    timeout connect 3s'
    echo '    timeout client 30s'
    echo '    timeout server 30s'
    echo '    timeout http-request 10s'
    if [ "$GATEWAY_DISCOVERY_MODE" = dns ]; then
      echo
      echo 'resolvers internal'
      echo "    nameserver dns ${GATEWAY_DISCOVERY_RESOLVER}:53"
      echo '    resolve_retries 3'
      echo '    timeout resolve 1s'
      echo '    timeout retry 1s'
      echo '    hold valid 10s'
    fi
    echo
    echo 'frontend shinmon_listener_pool'
    echo "    bind :${SHINMON_LISTENER_PORT_MIN}-${SHINMON_LISTENER_PORT_MAX}${bind_options}"
    echo '    http-request del-header X-Gateway-Listener-Port'
    echo '    http-request set-header X-Gateway-Listener-Port %[dst_port]'
    if [ -n "$forwarded_proto" ]; then echo "$forwarded_proto"; fi
    echo '    default_backend shinmon_gateways'
    echo
    echo 'backend shinmon_gateways'
    echo '    balance roundrobin'
    echo '    option httpchk'
    echo '    http-check send meth GET uri /health/ready hdr Host gateway-health'
    echo '    http-check expect status 200'
    echo '    default-server check inter 2s fastinter 500ms downinter 500ms fall 2 rise 2'
    if [ "$GATEWAY_DISCOVERY_MODE" = dns ]; then
      echo "    server-template gateway 1-${GATEWAY_DISCOVERY_MAX_REPLICAS} ${GATEWAY_DISCOVERY_NAME}:4040 resolvers internal init-addr libc,none${backend_tls}"
    else
      old_ifs=$IFS
      IFS=,
      set -- $GATEWAY_HOSTS
      IFS=$old_ifs
      for entry do
        name=${entry%@*}
        address=$(resolve_ipv4 "${entry#*@}")
        echo "    server $name $address:4040${backend_tls}"
      done
    fi
  } > "$target"
}

render_node() {
  role=$1
  entry=$2
  name=${entry%@*}
  address=$(resolve_ipv4 "${entry#*@}")
  node_dir=$output/$name
  mkdir -p "$node_dir/tls"
  cp "$repository_root/deploy/multihost/docker-compose.yml" "$node_dir/docker-compose.yml"
  sed "s/@ROLE@/$role/g" "$repository_root/deploy/multihost/shinmon.service.template" > "$node_dir/shinmon.service"
  environment=$node_dir/node.env
  : > "$environment"
  write_common_role_environment "$role" "$environment"
  {
    echo "SHINMON_NODE_NAME=$name"
    echo "SHINMON_NODE_ROLE=$role"
    echo "SHINMON_IMAGE_TAG=$SHINMON_IMAGE_TAG"
    echo "SHINMON_REGISTRY=${SHINMON_REGISTRY:-shinmon}"
    echo "SHINMON_TRANSPORT_MODE=$SHINMON_TRANSPORT_MODE"
    echo "SHINMON_SECRET_GID=$(id -g)"
  } >> "$environment"
  case $role in
    gateway)
      {
        echo 'GATEWAY_HTTP_ADDR=:4040'
        echo "GATEWAY_INSTANCE_ID=$name"
        echo "GATEWAY_ADVERTISE_ADDR=$address:4040"
        echo "GATEWAY_TRUSTED_PROXY_CIDRS=$edge_cidrs"
      } >> "$environment"
      if [ "$SHINMON_TRANSPORT_MODE" = https ]; then
        {
          echo 'GATEWAY_TLS_CERT_FILE=/etc/shinmon/tls/internal.crt'
          echo 'GATEWAY_TLS_KEY_FILE=/etc/shinmon/tls/internal.key'
        } >> "$environment"
      fi
      ;;
    management)
      echo 'GATEWAY_ADMIN_HTTP_ADDR=:4041' >> "$environment"
      if [ "$SHINMON_TRANSPORT_MODE" = https ]; then
        {
          echo 'GATEWAY_TLS_CERT_FILE=/etc/shinmon/tls/internal.crt'
          echo 'GATEWAY_TLS_KEY_FILE=/etc/shinmon/tls/internal.key'
        } >> "$environment"
      fi
      ;;
    dashboard)
      if [ "$SHINMON_TRANSPORT_MODE" = https ]; then
        {
          echo 'DASHBOARD_LISTEN=4042 ssl'
          echo 'DASHBOARD_TLS_CONFIG=ssl_certificate /etc/shinmon/tls/dashboard.crt; ssl_certificate_key /etc/shinmon/tls/dashboard.key; ssl_protocols TLSv1.2 TLSv1.3;'
          echo 'DASHBOARD_HEALTH_SCHEME=https'
          echo "DASHBOARD_API_TARGET=https://${MANAGEMENT_DISCOVERY_NAME}:4041"
          echo "DASHBOARD_PROXY_TLS_CONFIG=proxy_ssl_verify on; proxy_ssl_trusted_certificate /etc/shinmon/tls/internal-ca.pem; proxy_ssl_server_name on; proxy_ssl_name ${MANAGEMENT_DISCOVERY_NAME};"
        } >> "$environment"
      else
        {
          echo 'DASHBOARD_LISTEN=4042'
          echo 'DASHBOARD_TLS_CONFIG='
          echo 'DASHBOARD_HEALTH_SCHEME=http'
          echo "DASHBOARD_API_TARGET=http://${MANAGEMENT_DISCOVERY_NAME}:4041"
          echo 'DASHBOARD_PROXY_TLS_CONFIG='
        } >> "$environment"
      fi
      ;;
    edge) render_haproxy "$node_dir/haproxy.cfg" ;;
  esac
  if [ -n "${SHINMON_TLS_SOURCE_DIR:-}" ]; then
    case $role in
      edge)
        cp "$SHINMON_TLS_SOURCE_DIR/edge.pem" "$SHINMON_TLS_SOURCE_DIR/internal-ca.pem" "$node_dir/tls/"
        ;;
      gateway|management)
        cp "$SHINMON_TLS_SOURCE_DIR/internal.crt" "$SHINMON_TLS_SOURCE_DIR/internal.key" "$node_dir/tls/"
        if [ -r "$SHINMON_TLS_SOURCE_DIR/upstream-ca.pem" ] && [ "$role" = gateway ]; then
          cp "$SHINMON_TLS_SOURCE_DIR/upstream-ca.pem" "$node_dir/tls/"
        fi
        ;;
      dashboard)
        cp "$SHINMON_TLS_SOURCE_DIR/dashboard.crt" "$SHINMON_TLS_SOURCE_DIR/dashboard.key" "$SHINMON_TLS_SOURCE_DIR/internal-ca.pem" "$node_dir/tls/"
        ;;
    esac
  fi
  chmod 750 "$node_dir" "$node_dir/tls"
  chmod 640 "$node_dir/docker-compose.yml" "$node_dir/shinmon.service"
  if [ -f "$node_dir/haproxy.cfg" ]; then chmod 640 "$node_dir/haproxy.cfg"; fi
  find "$node_dir/tls" -type f -exec chmod 640 {} \;
}

for role_and_hosts in "edge:$EDGE_HOSTS" "gateway:$GATEWAY_HOSTS" "management:$MANAGEMENT_HOSTS" "dashboard:$DASHBOARD_HOSTS"; do
  role=${role_and_hosts%%:*}
  hosts=${role_and_hosts#*:}
  old_ifs=$IFS
  IFS=,
  set -- $hosts
  IFS=$old_ifs
  for entry do render_node "$role" "$entry"; done
done

cat > "$output/README.txt" <<EOF
Copy each node directory to /opt/shinmon/releases/<release> on its named host.
Point /opt/shinmon/current at that release, install shinmon.service in systemd,
then run: systemctl daemon-reload && systemctl enable --now shinmon

Roll gateways one at a time. Roll the passive edge, move the VIP/L4 traffic,
then roll the other edge. Keep the previous release directory for rollback.
EOF

echo "Rendered multi-host bundles in $output. Secrets are owner/group restricted for the container runtime."
