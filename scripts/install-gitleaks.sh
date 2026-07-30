#!/bin/sh
set -eu

: "${1:?usage: scripts/install-gitleaks.sh OUTPUT_DIRECTORY}"

mds_output=$1
mds_version=8.30.1

case "$(uname -s):$(uname -m)" in
  Darwin:arm64)
    mds_platform=darwin_arm64
    mds_sha256=b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5
    ;;
  Darwin:x86_64)
    mds_platform=darwin_x64
    mds_sha256=dfe101a4db2255fc85120ac7f3d25e4342c3c20cf749f2c20a18081af1952709
    ;;
  Linux:aarch64 | Linux:arm64)
    mds_platform=linux_arm64
    mds_sha256=e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080
    ;;
  Linux:x86_64)
    mds_platform=linux_x64
    mds_sha256=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb
    ;;
  *)
    echo "unsupported gitleaks platform: $(uname -s)/$(uname -m)" >&2
    exit 2
    ;;
esac

mds_archive_name="gitleaks_${mds_version}_${mds_platform}.tar.gz"
mds_url="https://github.com/gitleaks/gitleaks/releases/download/v${mds_version}/${mds_archive_name}"
mds_tmp=$(mktemp -d)
trap 'rm -rf "$mds_tmp"' EXIT HUP INT TERM
mds_archive="$mds_tmp/$mds_archive_name"

mds_effective_url=$(
  ulimit -f 1048576
  curl --fail --show-error --silent --location --max-redirs 3 \
    --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 30 --max-time 600 --max-filesize 536870912 \
    --output "$mds_archive" --write-out '%{url_effective}' "$mds_url"
)
case "$mds_effective_url" in
  https://*) ;;
  *)
    echo "gitleaks redirect must remain HTTPS" >&2
    exit 1
    ;;
esac
mds_effective_authority=${mds_effective_url#https://}
mds_effective_authority=${mds_effective_authority%%/*}
case "$mds_effective_authority" in
  "" | *@*)
    echo "gitleaks redirect must not contain userinfo" >&2
    exit 1
    ;;
esac
printf '%s  %s\n' "$mds_sha256" "$mds_archive" | shasum -a 256 -c -
mds_binary_entries=$(
  tar -tzf "$mds_archive" |
    awk '$0 == "gitleaks" { count++ } END { print count + 0 }'
)
if [ "$mds_binary_entries" -ne 1 ]; then
  echo "gitleaks archive must contain one exact gitleaks entry" >&2
  exit 1
fi
tar -xzf "$mds_archive" -C "$mds_tmp" gitleaks
if [ ! -f "$mds_tmp/gitleaks" ] || [ -L "$mds_tmp/gitleaks" ]; then
  echo "gitleaks archive did not contain a regular binary" >&2
  exit 1
fi
mkdir -p "$mds_output"
install -m 0755 "$mds_tmp/gitleaks" "$mds_output/gitleaks"
"$mds_output/gitleaks" version
