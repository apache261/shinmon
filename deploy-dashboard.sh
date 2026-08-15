#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
requested_profile=${1:-}
profile=
action=

case $requested_profile in
  dev)
    [ "$#" -eq 2 ] || { echo "Usage: $0 dev config|pull|up|down" >&2; exit 2; }
    profile=dev
    action=$2
    ;;
  prod)
    [ "$#" -eq 3 ] || { echo "Usage: $0 prod http|https config|pull|up|down" >&2; exit 2; }
    profile=prod-$2
    action=$3
    ;;
  prod-http|prod-https)
    [ "$#" -eq 2 ] || { echo "Usage: $0 $requested_profile config|pull|up|down" >&2; exit 2; }
    profile=$requested_profile
    action=$2
    ;;
  *)
    echo "Usage:" >&2
    echo "  $0 dev config|pull|up|down" >&2
    echo "  $0 prod http|https config|pull|up|down" >&2
    echo "  $0 prod-http|prod-https config|pull|up|down" >&2
    exit 2
    ;;
esac

case $action in config|pull|up|down) ;; *) echo "Unsupported dashboard action: $action" >&2; exit 2;; esac

case $profile in
  dev)
    default_environment=$repository_root/dashboard.env
    environment_example=$repository_root/dashboard.env.example
    scheme=http
    label=development
    ;;
  prod-http)
    default_environment=$repository_root/dashboard-prod-http.env
    environment_example=$repository_root/dashboard-prod-http.env.example
    scheme=http
    label=production-listener-http
    ;;
  prod-https)
    default_environment=$repository_root/dashboard-prod.env
    environment_example=$repository_root/dashboard-prod.env.example
    scheme=https
    label=production-listener-https
    ;;
  *)
    echo "Production dashboard transport must be http or https." >&2
    exit 2
    ;;
esac

dashboard_environment=${DASHBOARD_ENV_FILE:-$default_environment}
base_compose=$repository_root/dashboard-docker-compose.yml
network_override=$repository_root/deploy/dashboard/docker-compose.stack.yml

if [ ! -f "$dashboard_environment" ]; then
  (umask 077 && cp "$environment_example" "$dashboard_environment")
  echo "Created $(basename "$dashboard_environment") from $(basename "$environment_example")."
fi

compose() {
  docker compose \
    -f "$base_compose" \
    -f "$network_override" \
    --env-file "$dashboard_environment" \
    "$@"
}

environment_value() {
  key=$1
  sed -n "s/^${key}=//p" "$dashboard_environment" | tail -n 1
}

validate_https_files() {
  tls_dir=$(environment_value DASHBOARD_TLS_DIR)
  tls_dir=${tls_dir:-./deploy/tls}
  case $tls_dir in /*) ;; *) tls_dir=$repository_root/${tls_dir#./};; esac

  for required_file in dashboard.crt dashboard.key internal-ca.pem; do
    if [ ! -r "$tls_dir/$required_file" ]; then
      echo "HTTPS dashboard file is missing or unreadable: $tls_dir/$required_file" >&2
      exit 1
    fi
  done

  if ! openssl x509 -in "$tls_dir/dashboard.crt" -noout -checkend 86400 >/dev/null 2>&1 || \
     ! openssl pkey -in "$tls_dir/dashboard.key" -noout >/dev/null 2>&1; then
    echo "HTTPS dashboard certificate validation failed." >&2
    exit 1
  fi

  certificate_key=$(openssl x509 -in "$tls_dir/dashboard.crt" -pubkey -noout | openssl sha256)
  private_key=$(openssl pkey -in "$tls_dir/dashboard.key" -pubout 2>/dev/null | openssl sha256)
  if [ "$certificate_key" != "$private_key" ]; then
    echo "HTTPS dashboard private key does not match dashboard.crt." >&2
    exit 1
  fi
}

case $action in
  config)
    compose config --quiet
    echo "Shinmon dashboard $label configuration is valid."
    ;;
  pull)
    compose config --quiet
    compose pull dashboard
    ;;
  up)
    if [ "$scheme" = https ]; then
      validate_https_files
    fi
    control_network=$(environment_value DASHBOARD_CONTROL_NETWORK)
    control_network=${control_network:-shinmon-stack_control}
    if ! docker network inspect "$control_network" >/dev/null 2>&1; then
      echo "Gateway control network '$control_network' is unavailable. Start the gateway stack first." >&2
      exit 1
    fi
    compose config --quiet
    compose up -d --force-recreate --wait
    echo "Shinmon dashboard $label profile is ready at $scheme://127.0.0.1:$(environment_value DASHBOARD_PORT)."
    if [ "$scheme" = http ]; then
      echo "HTTP is restricted to isolated development and testing."
    fi
    ;;
  down)
    compose down
    echo "Shinmon dashboard $label profile is stopped."
    ;;
esac
