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

if [ "$profile" = production ] && [ "$action" = up ]; then
  ensure_environment_file "$dashboard_environment" "$dashboard_environment_example"
  haproxy_config=$(sed -n 's/^HAPROXY_CONFIG_PATH=//p' "$deploy_environment" | tail -n 1)
  haproxy_config=${haproxy_config:-./deploy/haproxy/haproxy.https.production.cfg}
  case $haproxy_config in /*) ;; *) haproxy_config="$repository_root/${haproxy_config#./}" ;; esac
  if [ ! -r "$haproxy_config" ] || ! grep -Eq '^[[:space:]]*bind :4300-4399 ssl ' "$haproxy_config"; then
    echo "Production HAProxy configuration must terminate TLS on ports 4300-4399." >&2
    exit 1
  fi
  if ! grep -Eq '^GATEWAY_ADMIN_TLS_CERT_FILE=/.+' "$deploy_environment" || \
     ! grep -Eq '^GATEWAY_ADMIN_TLS_KEY_FILE=/.+' "$deploy_environment" || \
     ! grep -Eq '^GATEWAY_ADMIN_HEALTH_SCHEME=https$' "$deploy_environment"; then
    echo "Production management TLS settings are missing from $deploy_environment." >&2
    exit 1
  fi
  if ! grep -Eq '^DASHBOARD_LISTEN=.*ssl' "$dashboard_environment" || \
     ! grep -Eq '^DASHBOARD_API_TARGET=https://' "$dashboard_environment" || \
     ! grep -Eq '^DASHBOARD_PROXY_TLS_CONFIG=.*proxy_ssl_verify on;' "$dashboard_environment"; then
    echo "Production dashboard and management proxy must use verified HTTPS." >&2
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
    "$dashboard_tls_dir/dashboard.crt" \
    "$dashboard_tls_dir/dashboard.key" \
    "$dashboard_tls_dir/internal-ca.pem"
  do
    if [ ! -r "$required_file" ]; then
      echo "Production TLS file is missing or unreadable: $required_file" >&2
      exit 1
    fi
  done
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
    if [ "$profile" = production ]; then
      echo "TLS production profile is running; organizational production approval remains separate."
      echo "Dashboard: https://127.0.0.1:4042"
      echo "Admin API: https://127.0.0.1:4041"
      echo "Default gateway listener pool: https://127.0.0.1:$default_port_pool"
    else
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
