#!/usr/bin/env bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: $0 --mds-evidence <fixed-release-path> --expected-mds-evidence-sha256 <sha256> --mds <fixed-production-path> --target <id> --output <dir> --cohort <cert-YYYYMMDDThhmmssZ-commit8> --expected-binary-sha256 <sha256> --expected-plan-digest <sha256:digest> [--expected-guest-creation-nonce-commitment <sha256:digest>] <--all|--profile NAME|--component ID...>" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
exec "$script_dir/run-release-certifier.sh" certify "$@"
