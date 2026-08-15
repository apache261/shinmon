#!/bin/sh
set -eu

inventory=${1:-}
reachability=${2:-}
if [ -z "$inventory" ] || [ ! -r "$inventory" ]; then
  echo "Usage: $0 INVENTORY [--reachability]" >&2
  exit 2
fi
inventory=$(CDPATH= cd -- "$(dirname -- "$inventory")" && pwd)/$(basename -- "$inventory")
case $reachability in ""|--reachability) ;; *) echo "Unknown option: $reachability" >&2; exit 2;; esac

# The inventory is operator-controlled shell-compatible KEY=value data. Reject
# command syntax before loading it so values remain simple and auditable.
if grep -Ev '^[[:space:]]*(#.*|[A-Z][A-Z0-9_]*=[A-Za-z0-9_.,:/@+-]*|)$' "$inventory" | grep -q .; then
  echo "Inventory contains unsupported syntax or whitespace." >&2
  exit 1
fi
# shellcheck disable=SC1090
. "$inventory"

transport=${SHINMON_TRANSPORT_MODE:-http}
discovery=${GATEWAY_DISCOVERY_MODE:-dns}
maximum=${GATEWAY_DISCOVERY_MAX_REPLICAS:-8}
port_min=${SHINMON_LISTENER_PORT_MIN:-4100}
port_max=${SHINMON_LISTENER_PORT_MAX:-4199}
case $transport in http|https) ;; *) echo "SHINMON_TRANSPORT_MODE must be http or https." >&2; exit 1;; esac
case $discovery in dns|static) ;; *) echo "GATEWAY_DISCOVERY_MODE must be dns or static." >&2; exit 1;; esac
case $maximum in ''|*[!0-9]*) echo "GATEWAY_DISCOVERY_MAX_REPLICAS must be an integer from 2 to 8." >&2; exit 1;; esac
if [ "$maximum" -lt 2 ] || [ "$maximum" -gt 8 ]; then
  echo "GATEWAY_DISCOVERY_MAX_REPLICAS must be an integer from 2 to 8." >&2
  exit 1
fi
for port in "$port_min" "$port_max"; do
  case $port in ''|*[!0-9]*) echo "Listener ports must be integers." >&2; exit 1;; esac
done
if [ "$port_min" -lt 1 ] || [ "$port_max" -gt 65535 ] || [ "$port_min" -gt "$port_max" ]; then
  echo "Listener port range is invalid." >&2
  exit 1
fi

validate_hosts() {
  role=$1
  entries=$2
  minimum=$3
  maximum_count=$4
  old_ifs=$IFS
  IFS=,
  set -- $entries
  IFS=$old_ifs
  count=$#
  if [ "$count" -lt "$minimum" ] || [ "$count" -gt "$maximum_count" ]; then
    echo "$role requires $minimum-$maximum_count hosts; found $count." >&2
    exit 1
  fi
  for entry do
    name=${entry%@*}
    address=${entry#*@}
    if [ "$name" = "$entry" ] || [ -z "$name" ] || [ -z "$address" ]; then
      echo "Invalid $role host entry: $entry" >&2
      exit 1
    fi
    case $name in *[!a-zA-Z0-9-]*) echo "Invalid node name: $name" >&2; exit 1;; esac
    if ! getent ahosts "$address" >/dev/null 2>&1; then
      echo "Cannot resolve $role address: $address" >&2
      exit 1
    fi
    resolved=$(getent ahostsv4 "$address" | awk 'NR==1 { print $1; exit }')
    case ",$seen_names," in *",$name,"*) echo "Duplicate node name: $name" >&2; exit 1;; esac
    case ",$seen_addresses," in *",$resolved,"*) echo "Duplicate node address: $resolved" >&2; exit 1;; esac
    seen_names=${seen_names:+$seen_names,}$name
    seen_addresses=${seen_addresses:+$seen_addresses,}$resolved
    if [ "$reachability" = --reachability ]; then
      ssh -o BatchMode=yes -o ConnectTimeout=3 -p "${SHINMON_SSH_PORT:-22}" "$address" true
    fi
  done
}

seen_names=
seen_addresses=
validate_hosts edge "${EDGE_HOSTS:-}" 1 2
validate_hosts gateway "${GATEWAY_HOSTS:-}" 2 "$maximum"
validate_hosts management "${MANAGEMENT_HOSTS:-}" 1 2
validate_hosts dashboard "${DASHBOARD_HOSTS:-}" 1 2

if [ "$discovery" = dns ]; then
  : "${GATEWAY_DISCOVERY_NAME:?GATEWAY_DISCOVERY_NAME is required in DNS mode}"
  : "${GATEWAY_DISCOVERY_RESOLVER:?GATEWAY_DISCOVERY_RESOLVER is required in DNS mode}"
fi

if [ "$transport" = https ]; then
  tls_dir=${SHINMON_TLS_SOURCE_DIR:-}
  if [ -z "$tls_dir" ] || [ ! -d "$tls_dir" ]; then
    echo "SHINMON_TLS_SOURCE_DIR must name a readable directory in HTTPS mode." >&2
    exit 1
  fi
  for file in edge.pem internal.crt internal.key internal-ca.pem dashboard.crt dashboard.key; do
    if [ ! -r "$tls_dir/$file" ]; then
      echo "Missing TLS file: $tls_dir/$file" >&2
      exit 1
    fi
  done
  openssl x509 -in "$tls_dir/edge.pem" -noout -checkend 86400 >/dev/null
  openssl x509 -in "$tls_dir/internal.crt" -noout -checkend 86400 >/dev/null
  openssl x509 -in "$tls_dir/dashboard.crt" -noout -checkend 86400 >/dev/null
  openssl pkey -in "$tls_dir/edge.pem" -noout >/dev/null 2>&1
  edge_cert_key=$(openssl x509 -in "$tls_dir/edge.pem" -pubkey -noout | openssl sha256)
  edge_private_key=$(openssl pkey -in "$tls_dir/edge.pem" -pubout 2>/dev/null | openssl sha256)
  internal_cert_key=$(openssl x509 -in "$tls_dir/internal.crt" -pubkey -noout | openssl sha256)
  internal_private_key=$(openssl pkey -in "$tls_dir/internal.key" -pubout 2>/dev/null | openssl sha256)
  dashboard_cert_key=$(openssl x509 -in "$tls_dir/dashboard.crt" -pubkey -noout | openssl sha256)
  dashboard_private_key=$(openssl pkey -in "$tls_dir/dashboard.key" -pubout 2>/dev/null | openssl sha256)
  if [ "$edge_cert_key" != "$edge_private_key" ] || [ "$internal_cert_key" != "$internal_private_key" ] || [ "$dashboard_cert_key" != "$dashboard_private_key" ]; then
    echo "A TLS certificate does not match its private key." >&2
    exit 1
  fi
  openssl verify -CAfile "$tls_dir/internal-ca.pem" "$tls_dir/internal.crt" >/dev/null
  openssl x509 -in "$tls_dir/internal.crt" -noout -checkhost "${GATEWAY_TLS_SERVER_NAME:-${GATEWAY_DISCOVERY_NAME:-gateway.service.internal}}" >/dev/null
  openssl x509 -in "$tls_dir/internal.crt" -noout -checkhost "${MANAGEMENT_DISCOVERY_NAME:-gateway-admin.service.internal}" >/dev/null
elif [ -n "${SHINMON_TLS_SOURCE_DIR:-}" ] && [ ! -d "$SHINMON_TLS_SOURCE_DIR" ]; then
  echo "SHINMON_TLS_SOURCE_DIR is not a readable directory." >&2
  exit 1
fi

echo "Multi-host inventory is valid ($transport, $discovery discovery, $port_min-$port_max)."
