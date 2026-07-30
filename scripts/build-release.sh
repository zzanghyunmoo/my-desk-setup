#!/bin/sh
set -eu

mds_script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
mds_root=$(CDPATH='' cd -- "$mds_script_dir/.." && pwd)
mds_output=${1:-"$mds_root/dist"}

: "${MDS_VERSION:?set MDS_VERSION to an exact version without a v prefix}"

if [ "${MDS_VERSION#v}" != "$MDS_VERSION" ]; then
  echo "MDS_VERSION must not include a v prefix" >&2
  exit 2
fi

if [ -z "${MDS_COMMIT:-}" ]; then
  MDS_COMMIT=$(git -C "$mds_root" rev-parse HEAD)
fi
if [ -z "${MDS_DATE:-}" ]; then
  MDS_DATE=$(git -C "$mds_root" show -s --format=%cI "$MDS_COMMIT")
fi

cd "$mds_root"
exec go run ./cmd/mds-release build \
  --source "$mds_root" \
  --output "$mds_output" \
  --version "$MDS_VERSION" \
  --commit "$MDS_COMMIT" \
  --date "$MDS_DATE"
