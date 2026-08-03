#!/usr/bin/env bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: $0 --mds <fixed-production-path> --target <id> --output <dir> --cohort <cert-YYYYMMDDThhmmssZ-commit8> --expected-binary-sha256 <sha256> --expected-plan-digest <sha256:digest> [--expected-guest-creation-nonce-commitment <sha256:digest>] <--all|--profile NAME|--component ID...>" >&2
  exit 2
fi

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

exec go run ./cmd/mds-evidence certify "$@"
