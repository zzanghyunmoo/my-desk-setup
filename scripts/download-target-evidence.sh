#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?set GH_TOKEN for GitHub Actions artifact reads}"
: "${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY to owner/repository}"
: "${MDS_COMMIT:?set MDS_COMMIT to the exact release commit}"
: "${MDS_CERTIFICATION_COHORT:?set MDS_CERTIFICATION_COHORT to the selected certification cohort}"
: "${MDS_EVIDENCE_ROOT:?set MDS_EVIDENCE_ROOT to a new evidence directory}"

if [[ ! "$MDS_COMMIT" =~ ^[0-9a-f]{40}$|^[0-9a-f]{64}$ ]]; then
  echo "MDS_COMMIT must be a full lowercase commit SHA" >&2
  exit 2
fi
if [[ ! "$MDS_CERTIFICATION_COHORT" =~ ^cert-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]] ||
  [[ "${MDS_CERTIFICATION_COHORT##*-}" != "${MDS_COMMIT:0:8}" ]]; then
  echo "MDS_CERTIFICATION_COHORT must be canonical and match MDS_COMMIT" >&2
  exit 2
fi
if [[ -e "$MDS_EVIDENCE_ROOT" ]]; then
  echo "MDS_EVIDENCE_ROOT already exists: $MDS_EVIDENCE_ROOT" >&2
  exit 2
fi

mds_tmp=$(mktemp -d)
trap 'rm -rf -- "$mds_tmp"' EXIT

gh api --paginate --slurp \
  "repos/$GITHUB_REPOSITORY/actions/workflows/target-certification.yml/runs?event=workflow_dispatch&status=completed&head_sha=$MDS_COMMIT&per_page=100" \
  > "$mds_tmp/runs.json"

jq --arg commit "$MDS_COMMIT" \
  '[.[].workflow_runs[]
    | select(.head_sha == $commit and .event == "workflow_dispatch" and .conclusion == "success")]
   | unique_by(.id)
   | .[].id' \
  "$mds_tmp/runs.json" > "$mds_tmp/run-ids.txt"

if [[ ! -s "$mds_tmp/run-ids.txt" ]]; then
  echo "no successful target-certification runs found for $MDS_COMMIT" >&2
  exit 1
fi

: > "$mds_tmp/artifacts.tsv"
while IFS= read -r mds_run_id; do
  mds_run_attempt=$(
    jq -er --arg run_id "$mds_run_id" \
      '[.[].workflow_runs[]
        | select((.id | tostring) == $run_id)
        | .run_attempt]
       | unique
       | if length == 1 then .[0] else error("ambiguous run attempt") end' \
      "$mds_tmp/runs.json"
  )
  gh api --paginate --slurp \
    "repos/$GITHUB_REPOSITORY/actions/runs/$mds_run_id/artifacts?per_page=100" \
    > "$mds_tmp/artifacts-$mds_run_id.json"
  jq -r \
    --arg commit "$MDS_COMMIT" \
    --arg cohort "$MDS_CERTIFICATION_COHORT" \
    --arg run_id "$mds_run_id" \
    --arg run_attempt "$mds_run_attempt" \
    '.[].artifacts[]
     | select(.expired == false)
     | select(.name
       | test("^target-evidence-(macos-host|windows-host|wsl-guest|lima-guest)-"
              + $commit + "-" + $cohort + "-" + $run_id + "-" + $run_attempt + "$"))
     | [.name, (.id | tostring)] | @tsv' \
    "$mds_tmp/artifacts-$mds_run_id.json" >> "$mds_tmp/artifacts.tsv"
done < "$mds_tmp/run-ids.txt"

mkdir -m 700 "$MDS_EVIDENCE_ROOT"
for mds_kind in macos-host windows-host wsl-guest lima-guest; do
  mds_prefix="target-evidence-$mds_kind-$MDS_COMMIT-$MDS_CERTIFICATION_COHORT-"
  mapfile -t mds_matches < <(
    awk -F '\t' -v prefix="$mds_prefix" \
      'index($1, prefix) == 1 { print $1 "\t" $2 }' \
      "$mds_tmp/artifacts.tsv"
  )
  if [[ ${#mds_matches[@]} -ne 1 ]]; then
    echo "expected exactly one $mds_kind artifact for $MDS_COMMIT and $MDS_CERTIFICATION_COHORT; found ${#mds_matches[@]}" >&2
    exit 1
  fi
  IFS=$'\t' read -r mds_name mds_artifact_id <<< "${mds_matches[0]}"
  if [[ ! "$mds_name" =~ ^target-evidence-$mds_kind-$MDS_COMMIT-$MDS_CERTIFICATION_COHORT-[0-9]+-[0-9]+$ ]]; then
    echo "artifact name is not canonical: $mds_name" >&2
    exit 1
  fi
  mds_zip="$mds_tmp/$mds_kind.zip"
  gh api "repos/$GITHUB_REPOSITORY/actions/artifacts/$mds_artifact_id/zip" > "$mds_zip"
  unzip -Z1 "$mds_zip" | LC_ALL=C sort > "$mds_tmp/$mds_kind-entries.txt"
  printf '%s\n' \
    checksums.txt \
    doctor.json \
    manifest.json \
    plan.json > "$mds_tmp/expected-entries.txt"
  if ! cmp -s \
    "$mds_tmp/expected-entries.txt" \
    "$mds_tmp/$mds_kind-entries.txt"; then
    echo "evidence artifact $mds_name does not contain the exact four-file bundle" >&2
    exit 1
  fi
  mkdir -m 700 "$MDS_EVIDENCE_ROOT/$mds_kind"
  unzip -q "$mds_zip" -d "$MDS_EVIDENCE_ROOT/$mds_kind"
done
