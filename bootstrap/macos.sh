#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "macOS bootstrap must run on Darwin" >&2
  exit 2
fi

: "${MDS_VERSION:?set MDS_VERSION to an exact released version}"
: "${MDS_SHA256:?set MDS_SHA256 to the published archive checksum}"

if ! printf '%s\n' "$MDS_VERSION" |
  grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'; then
  echo "MDS_VERSION must be an exact version without a v prefix" >&2
  exit 2
fi
case "$MDS_SHA256" in
  *[!0-9a-fA-F]*) echo "MDS_SHA256 must contain only hexadecimal characters" >&2; exit 2 ;;
esac
if [ "${#MDS_SHA256}" -ne 64 ]; then
  echo "MDS_SHA256 must be exactly 64 hexadecimal characters" >&2
  exit 2
fi

case "$(uname -m)" in
  arm64) mds_arch="arm64" ;;
  x86_64) mds_arch="amd64" ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;;
esac

mds_tmp="$(mktemp -d)"
trap 'rm -rf "$mds_tmp"' EXIT HUP INT TERM
mds_archive="$mds_tmp/mds.tar.gz"
mds_archive_name="mds_${MDS_VERSION}_darwin_${mds_arch}.tar.gz"
mds_base_url="${MDS_BASE_URL:-https://github.com/zzanghyunmoo/my-desk-setup/releases/download/v${MDS_VERSION}}"
mds_url="${mds_base_url%/}/${mds_archive_name}"
mds_install_dir="${MDS_INSTALL_DIR:-$HOME/.local/bin}"
case "$mds_url" in
  https://*[\?\#]* | https://*@*)
    echo "release URL must be credential-free HTTPS without query or fragment" >&2
    exit 2
    ;;
  https://*) ;;
  *)
    echo "release URL must be absolute HTTPS" >&2
    exit 2
    ;;
esac

if [ -n "${MDS_ARCHIVE:-}" ]; then
  if [ ! -f "$MDS_ARCHIVE" ] || [ -L "$MDS_ARCHIVE" ]; then
    echo "MDS_ARCHIVE must be a regular, non-symlink file" >&2
    exit 2
  fi
  cp "$MDS_ARCHIVE" "$mds_archive"
else
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
      echo "release redirect must remain HTTPS" >&2
      exit 1
      ;;
  esac
  mds_effective_authority=${mds_effective_url#https://}
  mds_effective_authority=${mds_effective_authority%%/*}
  case "$mds_effective_authority" in
    "" | *@*)
      echo "release redirect must not contain userinfo" >&2
      exit 1
      ;;
  esac
fi
printf '%s  %s\n' "$MDS_SHA256" "$mds_archive" | shasum -a 256 -c -
if [ "$(tar -tzf "$mds_archive")" != "mds" ]; then
  echo "release archive must contain exactly one mds entry" >&2
  exit 1
fi
tar -xzf "$mds_archive" -C "$mds_tmp" mds
if [ ! -f "$mds_tmp/mds" ] || [ -L "$mds_tmp/mds" ]; then
  echo "release archive mds entry must be a regular file" >&2
  exit 1
fi
if [ -L "$mds_install_dir" ] || [ -L "$mds_install_dir/mds" ]; then
  echo "refusing to install through a symlink or replace a symlink" >&2
  exit 1
fi
install -d "$mds_install_dir"
install -m 0755 "$mds_tmp/mds" "$mds_install_dir/mds"

echo "Installed mds ${MDS_VERSION}. Authentication remains a manual user action."
"$mds_install_dir/mds" --version
if [ "${MDS_PLAN_SMOKE:-0}" = "1" ]; then
  "$mds_install_dir/mds" plan --profile owner --format json
fi
echo "Next: $mds_install_dir/mds plan --profile owner"
