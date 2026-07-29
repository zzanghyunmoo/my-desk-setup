#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "macOS bootstrap must run on Darwin" >&2
  exit 2
fi

: "${MDS_VERSION:?set MDS_VERSION to an exact released version}"
: "${MDS_SHA256:?set MDS_SHA256 to the published archive checksum}"

case "$(uname -m)" in
  arm64) mds_arch="arm64" ;;
  x86_64) mds_arch="amd64" ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;;
esac

mds_tmp="$(mktemp -d)"
trap 'rm -rf "$mds_tmp"' EXIT HUP INT TERM
mds_archive="$mds_tmp/mds.tar.gz"
mds_url="https://github.com/zzanghyunmoo/my-desk-setup/releases/download/v${MDS_VERSION}/mds_${MDS_VERSION}_darwin_${mds_arch}.tar.gz"

curl -fsSL "$mds_url" -o "$mds_archive"
printf '%s  %s\n' "$MDS_SHA256" "$mds_archive" | shasum -a 256 -c -
tar -xzf "$mds_archive" -C "$mds_tmp" mds
install -d "$HOME/.local/bin"
install -m 0755 "$mds_tmp/mds" "$HOME/.local/bin/mds"

echo "Installed mds ${MDS_VERSION}. Authentication remains a manual user action."
echo "Next: $HOME/.local/bin/mds plan --profile owner"
