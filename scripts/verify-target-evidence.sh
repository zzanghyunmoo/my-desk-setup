#!/usr/bin/env bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: $0 --mds-evidence <fixed-release-path> --expected-mds-evidence-sha256 <sha256> --bundle <dir> --expected-cli-revision <revision> --expected-catalog-revision <revision> [verify options...]" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
exec "$script_dir/run-release-certifier.sh" verify "$@"
