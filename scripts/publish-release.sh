#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?set GH_TOKEN for GitHub release writes}"
: "${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY to owner/repository}"
: "${MDS_RELEASE_TAG:?set MDS_RELEASE_TAG to the protected annotated tag}"
: "${MDS_RELEASE_DIR:?set MDS_RELEASE_DIR to the verified release directory}"
: "${MDS_PROMOTION_REPORT:?set MDS_PROMOTION_REPORT to release-promotion.json}"
: "${MDS_COMMIT:?set MDS_COMMIT to the exact tagged commit}"

if [[ ! "$MDS_RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]] ||
  [[ ! "$MDS_COMMIT" =~ ^[0-9a-f]{40}$|^[0-9a-f]{64}$ ]]; then
  echo "release tag or commit identity is invalid" >&2
  exit 2
fi
if [[ ! -d "$MDS_RELEASE_DIR" || ! -f "$MDS_PROMOTION_REPORT" ||
  -L "$MDS_RELEASE_DIR" || -L "$MDS_PROMOTION_REPORT" ]]; then
  echo "verified release assets and promotion report are required" >&2
  exit 2
fi
if [[ "$(git rev-list -n 1 "$MDS_RELEASE_TAG")" != "$MDS_COMMIT" ]]; then
  echo "release tag does not resolve to the expected commit" >&2
  exit 2
fi

mds_tmp=$(mktemp -d)
trap 'rm -rf -- "$mds_tmp"' EXIT
mds_release_response="$mds_tmp/release-response.txt"
mds_was_draft=false

for mds_local in "$MDS_RELEASE_DIR"/* "$MDS_PROMOTION_REPORT"; do
  if [[ ! -f "$mds_local" || -L "$mds_local" ]]; then
    echo "local release asset set contains a non-regular file" >&2
    exit 1
  fi
  basename "$mds_local"
done | LC_ALL=C sort > "$mds_tmp/expected-assets.txt"
mds_expected_count=$(wc -l < "$mds_tmp/expected-assets.txt" | tr -d ' ')
mds_unique_count=$(
  LC_ALL=C sort -u "$mds_tmp/expected-assets.txt" | wc -l | tr -d ' '
)
if [[ "$mds_expected_count" != "$mds_unique_count" ]]; then
  echo "local release asset names must be unique" >&2
  exit 1
fi

mds_api_exit=0
if gh api --include \
  --jq '[.tag_name, .draft] | @tsv' \
  "repos/$GITHUB_REPOSITORY/releases/tags/$MDS_RELEASE_TAG" \
  > "$mds_release_response" 2>"$mds_tmp/release-response.err"; then
  mds_api_exit=0
else
  mds_api_exit=$?
fi
mds_api_status=$(sed -n \
  '1s/^HTTP\/[^ ]* \([0-9][0-9][0-9]\).*/\1/p' \
  "$mds_release_response")

if [[ "$mds_api_status" == 200 && "$mds_api_exit" == 0 ]]; then
  IFS=$'\t' read -r mds_existing_tag mds_was_draft < <(
    tail -n 1 "$mds_release_response"
  )
  if [[ "$mds_existing_tag" != "$MDS_RELEASE_TAG" ]]; then
    echo "existing release has a different tag identity" >&2
    exit 1
  fi
  if [[ "$mds_was_draft" != true && "$mds_was_draft" != false ]]; then
    echo "existing release has an invalid draft state" >&2
    exit 1
  fi
elif [[ "$mds_api_status" == 404 ]]; then
  gh release create "$MDS_RELEASE_TAG" \
    --repo "$GITHUB_REPOSITORY" \
    --draft \
    --verify-tag \
    --generate-notes \
    --title "$MDS_RELEASE_TAG"
  mds_was_draft=true
else
  echo "GitHub release lookup failed with HTTP status ${mds_api_status:-unknown}" >&2
  exit 1
fi

if [[ "$mds_was_draft" == true ]]; then
  gh release upload "$MDS_RELEASE_TAG" \
    "$MDS_RELEASE_DIR"/* \
    "$MDS_PROMOTION_REPORT" \
    --repo "$GITHUB_REPOSITORY" \
    --clobber
fi

mkdir -m 700 "$mds_tmp/remote"
gh release download "$MDS_RELEASE_TAG" \
  --repo "$GITHUB_REPOSITORY" \
  --dir "$mds_tmp/remote"

find "$mds_tmp/remote" -mindepth 1 -maxdepth 1 -type f \
  -exec basename {} \; | LC_ALL=C sort > "$mds_tmp/remote-assets.txt"
if ! cmp -s "$mds_tmp/expected-assets.txt" "$mds_tmp/remote-assets.txt"; then
  echo "draft release does not contain the exact verified asset set" >&2
  exit 1
fi
while IFS= read -r mds_name; do
  mds_local="$MDS_RELEASE_DIR/$mds_name"
  if [[ "$mds_name" == "$(basename "$MDS_PROMOTION_REPORT")" ]]; then
    mds_local="$MDS_PROMOTION_REPORT"
  fi
  if ! cmp -s "$mds_local" "$mds_tmp/remote/$mds_name"; then
    echo "release asset bytes differ from the verified local artifact: $mds_name" >&2
    exit 1
  fi
done < "$mds_tmp/expected-assets.txt"

if [[ "$mds_was_draft" == true ]]; then
  gh release edit "$MDS_RELEASE_TAG" \
    --repo "$GITHUB_REPOSITORY" \
    --draft=false
fi
