#!/bin/sh
set -eu

mds_script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
mds_root=$(CDPATH='' cd -- "$mds_script_dir/.." && pwd)

: "${MDS_RELEASE_DIR:?set MDS_RELEASE_DIR to the downloaded release directory}"
: "${MDS_EVIDENCE_ROOT:?set MDS_EVIDENCE_ROOT to the downloaded actual-target evidence root}"
: "${MDS_COMMIT:?set MDS_COMMIT to the exact release commit}"

mds_max_age=${MDS_EVIDENCE_MAX_AGE:-24h}
mds_report=${MDS_PROMOTION_REPORT:-"$mds_root/promotion-report.json"}

cd "$mds_root"
exec go run ./cmd/mds-release promote \
  --directory "$MDS_RELEASE_DIR" \
  --evidence-root "$MDS_EVIDENCE_ROOT" \
  --commit "$MDS_COMMIT" \
  --max-age "$mds_max_age" \
  --report "$mds_report"
