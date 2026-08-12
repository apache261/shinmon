#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

if ! command -v govulncheck >/dev/null 2>&1; then
  echo "govulncheck is required; install golang.org/x/vuln/cmd/govulncheck@v1.6.0" >&2
  exit 2
fi
govulncheck ./...

if rg -n --hidden \
  --glob '!.git/**' --glob '!web/admin/vendor/**' --glob '!**/*_test.go' \
  --glob '!deploy.env' --glob '!deploy-dev.env' --glob '!deploy-prod.env' --glob '!.env' --glob '!.env.test' --glob '!dashboard.env' \
  '(-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----|AKIA[0-9A-Z]{16}|shn_[a-f0-9]{12}\.[a-f0-9]{52})' .
then
  echo "potential committed secret detected" >&2
  exit 1
fi

if [ "${SKIP_CONTAINER_SCAN:-0}" != "1" ]; then
  if ! command -v grype >/dev/null 2>&1; then
    echo "grype is required for credential-free local container scanning" >&2
    exit 2
  fi
  image_tag=${SHINMON_IMAGE_TAG:-development}
  grype "docker:shinmon/gateway:$image_tag" --fail-on critical
  grype "docker:shinmon/gateway-admin:$image_tag" --fail-on critical
fi

echo "security scans passed"
