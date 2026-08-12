#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SHINMON_DEPLOY_COMMAND=$0 exec "$repository_root/scripts/deploy-stack.sh" development "$@"
