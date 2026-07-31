#!/usr/bin/env bash
set -euo pipefail

mds_usage() {
  echo "usage: $0 verify | cohort <full-lowercase-commit-sha>" >&2
}

mds_parse_http_date() {
  local value=$1
  if date -u -d "$value" +%s 2>/dev/null; then
    return
  fi
  LC_ALL=C date -u -j -f "%a, %d %b %Y %H:%M:%S GMT" "$value" +%s
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  mds_usage
  exit 2
fi

mds_mode=$1
mds_commit=${2:-}
case "$mds_mode" in
  verify)
    if [[ -n "$mds_commit" ]]; then
      mds_usage
      exit 2
    fi
    ;;
  cohort)
    if [[ ! "$mds_commit" =~ ^[0-9a-f]{40}$|^[0-9a-f]{64}$ ]]; then
      echo "cohort requires a full lowercase commit SHA" >&2
      exit 2
    fi
    ;;
  *)
    mds_usage
    exit 2
    ;;
esac

mds_headers=$(mktemp)
trap 'rm -f -- "$mds_headers"' EXIT
mds_local_before=$(date -u +%s)
curl -fsSI --max-time 15 https://github.com/ > "$mds_headers"
mds_local_after=$(date -u +%s)
mds_server_date=$(
  sed -n 's/^[Dd]ate:[[:space:]]*//p' "$mds_headers" |
    tr -d '\r' |
    tail -n 1
)
if [[ -z "$mds_server_date" ]]; then
  echo "GitHub response did not include a Date header" >&2
  exit 1
fi
mds_server_epoch=$(mds_parse_http_date "$mds_server_date")
mds_local_midpoint=$(((mds_local_before + mds_local_after) / 2))
mds_skew=$((mds_server_epoch - mds_local_midpoint))
if ((mds_skew < 0)); then
  mds_skew=$((-mds_skew))
fi
if ((mds_skew > 60)); then
  echo "runner UTC clock differs from GitHub server time by more than 60 seconds" >&2
  exit 1
fi

if [[ "$mds_mode" == verify ]]; then
  echo "runner UTC clock is within 60 seconds of GitHub server time"
  exit 0
fi

mds_timestamp=$(date -u -r "$mds_server_epoch" +%Y%m%dT%H%M%SZ 2>/dev/null || true)
if [[ -z "$mds_timestamp" ]]; then
  mds_timestamp=$(date -u -d "@$mds_server_epoch" +%Y%m%dT%H%M%SZ)
fi
printf 'cert-%s-%s\n' "$mds_timestamp" "${mds_commit:0:8}"
