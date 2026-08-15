#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
profile=${1:-}
shift || true
action=${1:-}
shift || true
launcher=${SHINMON_DEPLOY_COMMAND:-"$0"}

case $profile in
  development)
    default_environment="$repository_root/deploy-dev.env"
    environment_example="$repository_root/deploy-dev.env.example"
    profile_label=development
    default_port_pool=4100-4199
    default_dashboard_environment="$repository_root/dashboard.env"
    dashboard_environment_example="$repository_root/dashboard.env.example"
    ;;
  production)
    default_environment="$repository_root/deploy-prod.env"
    environment_example="$repository_root/deploy-prod.env.example"
    profile_label=production-profile
    default_port_pool=4300-4399
    default_dashboard_environment="$repository_root/dashboard-prod.env"
    dashboard_environment_example="$repository_root/dashboard-prod.env.example"
    ;;
  *)
    echo "Unsupported internal deployment profile." >&2
    exit 2
    ;;
esac

usage() {
  if [ "$profile" = development ]; then
    echo "Usage: $launcher build|up|down [-v]" >&2
    echo "  down -v  Stop development and remove its PostgreSQL and Redis volumes." >&2
  else
    echo "Usage: $launcher build|up|down" >&2
  fi
}

case $action in
  build|up|down) ;;
  *)
    usage
    exit 2
    ;;
esac

remove_volumes=false
if [ "$action" = down ] && [ "$#" -eq 1 ] && [ "$1" = -v ]; then
  if [ "$profile" != development ]; then
    echo "Volume deletion is available only through deploy-dev.sh." >&2
    usage
    exit 2
  fi
  remove_volumes=true
elif [ "$#" -ne 0 ]; then
  usage
  exit 2
fi

deploy_environment=${DEPLOY_ENV_FILE:-"$default_environment"}
dashboard_environment=${DASHBOARD_ENV_FILE:-"$default_dashboard_environment"}
gateway_compose="$repository_root/gateway-docker-compose.yml"
dashboard_compose="$repository_root/dashboard-docker-compose.yml"
dashboard_stack_override="$repository_root/deploy/dashboard/docker-compose.stack.yml"
temporary_docker_config=

cleanup() {
  if [ -n "$temporary_docker_config" ] && [ -d "$temporary_docker_config" ]; then
    rm -rf -- "$temporary_docker_config"
  fi
}

# Shinmon currently builds from public base images. Use an isolated Docker
# config unless authenticated registry configuration is explicitly supplied.
docker_config=${SHINMON_DOCKER_CONFIG:-}
if [ -z "$docker_config" ]; then
  temporary_docker_config=$(mktemp -d "${TMPDIR:-/tmp}/shinmon-docker-config.XXXXXX")
  docker_config=$temporary_docker_config
  trap cleanup EXIT HUP INT TERM
fi

ensure_environment_file() {
  target=$1
  example=$2

  if [ ! -f "$target" ]; then
    (umask 077 && cp "$example" "$target")
    if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_UID:-}" ] && [ -n "${SUDO_GID:-}" ]; then
      chown "$SUDO_UID:$SUDO_GID" "$target"
    fi
    echo "Created $target from $(basename "$example")."
    echo "Review every credential and connection setting before shared use."
  fi
}

compose_gateway() {
  DOCKER_CONFIG=$docker_config docker compose \
    -f "$gateway_compose" \
    --env-file "$deploy_environment" \
    "$@"
}

compose_dashboard() {
  DOCKER_CONFIG=$docker_config docker compose \
    -f "$dashboard_compose" \
    -f "$dashboard_stack_override" \
    --env-file "$dashboard_environment" \
    "$@"
}

ensure_environment_file "$deploy_environment" "$environment_example"

transport_mode=$(sed -n 's/^SHINMON_TRANSPORT_MODE=//p' "$deploy_environment" | tail -n 1)
transport_mode=${transport_mode:-http}
case $transport_mode in
  http|https) ;;
  *)
    echo "SHINMON_TRANSPORT_MODE must be http or https." >&2
    exit 1
    ;;
esac
if [ "$profile" = production ] && [ "$transport_mode" = http ]; then
  dashboard_environment_example="$repository_root/dashboard-prod-http.env.example"
fi

if [ "$transport_mode" = https ]; then
  if [ "$profile" = production ]; then
    selected_haproxy_config=./deploy/haproxy/haproxy.https.production.cfg
  else
    selected_haproxy_config=./deploy/haproxy/haproxy.https.cfg
  fi
  export HAPROXY_CONFIG_PATH=${HAPROXY_CONFIG_PATH:-$selected_haproxy_config}
  export GATEWAY_DATA_TLS_CERT_FILE=/etc/shinmon/tls/internal.crt
  export GATEWAY_DATA_TLS_KEY_FILE=/etc/shinmon/tls/internal.key
  export GATEWAY_ADMIN_TLS_CERT_FILE=/etc/shinmon/tls/internal.crt
  export GATEWAY_ADMIN_TLS_KEY_FILE=/etc/shinmon/tls/internal.key
  export GATEWAY_ADMIN_HEALTH_SCHEME=https
  export DASHBOARD_LISTEN='4042 ssl'
  export DASHBOARD_TLS_CONFIG='ssl_certificate /etc/shinmon/tls/dashboard.crt; ssl_certificate_key /etc/shinmon/tls/dashboard.key; ssl_protocols TLSv1.2 TLSv1.3;'
  export DASHBOARD_HEALTH_SCHEME=https
  export DASHBOARD_STACK_API_TARGET=https://gateway-admin:4041
  export DASHBOARD_PROXY_TLS_CONFIG='proxy_ssl_verify on; proxy_ssl_trusted_certificate /etc/shinmon/tls/internal-ca.pem; proxy_ssl_server_name on; proxy_ssl_name gateway-admin;'
else
  if [ "$profile" = production ]; then
    selected_haproxy_config=./deploy/haproxy/haproxy.production.cfg
  else
    selected_haproxy_config=./deploy/haproxy/haproxy.cfg
  fi
  export HAPROXY_CONFIG_PATH=${HAPROXY_CONFIG_PATH:-$selected_haproxy_config}
  export GATEWAY_DATA_TLS_CERT_FILE=
  export GATEWAY_DATA_TLS_KEY_FILE=
  export GATEWAY_ADMIN_TLS_CERT_FILE=
  export GATEWAY_ADMIN_TLS_KEY_FILE=
  export GATEWAY_ADMIN_HEALTH_SCHEME=http
  export DASHBOARD_LISTEN=4042
  export DASHBOARD_TLS_CONFIG=
  export DASHBOARD_HEALTH_SCHEME=http
  export DASHBOARD_STACK_API_TARGET=http://gateway-admin:4041
  export DASHBOARD_PROXY_TLS_CONFIG=
fi

if [ "$transport_mode" = https ] && [ "$action" = up ]; then
  ensure_environment_file "$dashboard_environment" "$dashboard_environment_example"
  haproxy_config=$HAPROXY_CONFIG_PATH
  case $haproxy_config in /*) ;; *) haproxy_config="$repository_root/${haproxy_config#./}" ;; esac
  if [ ! -r "$haproxy_config" ] || ! grep -Eq '^[[:space:]]*bind :[0-9]+-[0-9]+ ssl ' "$haproxy_config"; then
    echo "HTTPS mode requires an HAProxy configuration that terminates TLS." >&2
    exit 1
  fi
  gateway_tls_dir=$(sed -n 's/^GATEWAY_TLS_DIR=//p' "$deploy_environment" | tail -n 1)
  dashboard_tls_dir=$(sed -n 's/^DASHBOARD_TLS_DIR=//p' "$dashboard_environment" 2>/dev/null | tail -n 1 || true)
  gateway_tls_dir=${gateway_tls_dir:-./deploy/tls}
  dashboard_tls_dir=${dashboard_tls_dir:-./deploy/tls}
  case $gateway_tls_dir in /*) ;; *) gateway_tls_dir="$repository_root/${gateway_tls_dir#./}" ;; esac
  case $dashboard_tls_dir in /*) ;; *) dashboard_tls_dir="$repository_root/${dashboard_tls_dir#./}" ;; esac
  for required_file in \
    "$gateway_tls_dir/edge.pem" \
    "$gateway_tls_dir/internal.crt" \
    "$gateway_tls_dir/internal.key" \
    "$gateway_tls_dir/internal-ca.pem" \
    "$dashboard_tls_dir/dashboard.crt" \
    "$dashboard_tls_dir/dashboard.key" \
    "$dashboard_tls_dir/internal-ca.pem"
  do
    if [ ! -r "$required_file" ]; then
      echo "HTTPS TLS file is missing or unreadable: $required_file" >&2
      exit 1
    fi
  done
  if ! openssl x509 -in "$gateway_tls_dir/edge.pem" -noout -checkend 86400 >/dev/null 2>&1 || \
     ! openssl pkey -in "$gateway_tls_dir/edge.pem" -noout >/dev/null 2>&1 || \
     ! openssl x509 -in "$gateway_tls_dir/internal.crt" -noout -checkend 86400 >/dev/null 2>&1 || \
     ! openssl x509 -in "$gateway_tls_dir/internal.crt" -noout -checkhost gateway >/dev/null 2>&1 || \
     ! openssl x509 -in "$gateway_tls_dir/internal.crt" -noout -checkhost gateway-admin >/dev/null 2>&1 || \
     ! openssl verify -CAfile "$gateway_tls_dir/internal-ca.pem" "$gateway_tls_dir/internal.crt" >/dev/null 2>&1 || \
     ! openssl x509 -in "$dashboard_tls_dir/dashboard.crt" -noout -checkend 86400 >/dev/null 2>&1; then
    echo "HTTPS certificate validation failed; transport will not fall back to HTTP." >&2
    exit 1
  fi
  edge_cert_key=$(openssl x509 -in "$gateway_tls_dir/edge.pem" -pubkey -noout | openssl sha256)
  edge_private_key=$(openssl pkey -in "$gateway_tls_dir/edge.pem" -pubout 2>/dev/null | openssl sha256)
  internal_cert_key=$(openssl x509 -in "$gateway_tls_dir/internal.crt" -pubkey -noout | openssl sha256)
  internal_private_key=$(openssl pkey -in "$gateway_tls_dir/internal.key" -pubout 2>/dev/null | openssl sha256)
  dashboard_cert_key=$(openssl x509 -in "$dashboard_tls_dir/dashboard.crt" -pubkey -noout | openssl sha256)
  dashboard_private_key=$(openssl pkey -in "$dashboard_tls_dir/dashboard.key" -pubout 2>/dev/null | openssl sha256)
  if [ "$edge_cert_key" != "$edge_private_key" ] || [ "$internal_cert_key" != "$internal_private_key" ] || [ "$dashboard_cert_key" != "$dashboard_private_key" ]; then
    echo "HTTPS certificate validation failed; a private key does not match its certificate." >&2
    exit 1
  fi
fi

case $action in
  build)
    compose_gateway config --quiet
    compose_gateway build gateway-admin gateway-1
    echo "Shinmon $profile_label images are built."
    ;;
  up)
    ensure_environment_file "$dashboard_environment" "$dashboard_environment_example"
    compose_gateway config --quiet
    compose_dashboard config --quiet
    compose_gateway up -d --build --wait
    # Recreate Nginx after gateway-admin is healthy so its upstream resolves.
    compose_dashboard up -d --force-recreate --wait

    echo "Shinmon $profile_label deployment is ready."
    if [ "$transport_mode" = https ]; then
      echo "HTTPS transport is enabled; organizational production approval remains separate."
      echo "Dashboard: https://127.0.0.1:4042"
      echo "Admin API: https://127.0.0.1:4041"
      echo "Default gateway listener pool: https://127.0.0.1:$default_port_pool"
    else
      echo "HTTP transport is enabled for isolated development or trusted networks only."
      echo "Dashboard: http://127.0.0.1:4042"
      echo "Admin API: http://127.0.0.1:4041"
      echo "Default gateway listener pool: http://127.0.0.1:$default_port_pool"
    fi
    ;;
  down)
    ensure_environment_file "$dashboard_environment" "$dashboard_environment_example"
    if [ "$remove_volumes" = true ]; then
      echo "Removing the development deployment and all PostgreSQL and Redis volumes."
      compose_dashboard down -v
      compose_gateway down -v
      echo "Shinmon development deployment stopped; development database and Redis data were deleted."
    else
      compose_dashboard down
      compose_gateway down
      echo "Shinmon $profile_label deployment stopped; database and Redis volumes were preserved."
    fi
    ;;
esac
