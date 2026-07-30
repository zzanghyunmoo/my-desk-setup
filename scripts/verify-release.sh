#!/bin/sh
set -eu

mds_script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
mds_root=$(CDPATH='' cd -- "$mds_script_dir/.." && pwd)
mds_directory=${1:-"$mds_root/dist"}

cd "$mds_root"
exec go run ./cmd/mds-release verify --directory "$mds_directory"
