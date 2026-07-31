#!/bin/sh
set -eu

mds_script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
mds_root=$(CDPATH='' cd -- "$mds_script_dir/.." && pwd)

: "${MDS_RELEASE_DIR:?set MDS_RELEASE_DIR to the downloaded release directory}"
: "${MDS_PROMOTION_REPORT:?set MDS_PROMOTION_REPORT to the downloaded promotion report}"
: "${MDS_COMMIT:?set MDS_COMMIT to the exact release commit}"
: "${MDS_CERTIFICATION_COHORT:?set MDS_CERTIFICATION_COHORT to the selected certification cohort}"

cd "$mds_root"
exec go run ./cmd/mds-release verify-promotion \
  --directory "$MDS_RELEASE_DIR" \
  --report "$MDS_PROMOTION_REPORT" \
  --commit "$MDS_COMMIT" \
  --cohort "$MDS_CERTIFICATION_COHORT"
