#!/bin/sh
set -eu

artifact_url=$1
expected_sha256=$2

case "$artifact_url" in
  https://*) ;;
  *)
    echo "guest bootstrap requires an HTTPS artifact URL" >&2
    exit 2
    ;;
esac

umask 077
temporary_directory=$(mktemp -d)
staged_binary=
staged_marker=
cleanup() {
  rm -rf "$temporary_directory"
  if [ -n "$staged_binary" ]; then
    rm -f "$staged_binary"
  fi
  if [ -n "$staged_marker" ]; then
    rm -f "$staged_marker"
  fi
}
trap cleanup EXIT HUP INT TERM

archive="$temporary_directory/mds.tar.gz"
if command -v curl >/dev/null 2>&1; then
  curl --fail --location --proto '=https' --tlsv1.2 \
    --output "$archive" "$artifact_url"
elif command -v wget >/dev/null 2>&1; then
  wget --https-only --output-document="$archive" "$artifact_url"
else
  echo "guest bootstrap requires curl or wget" >&2
  exit 69
fi

printf '%s  %s\n' "$expected_sha256" "$archive" | sha256sum -c -
tar -xzf "$archive" -C "$temporary_directory" mds

binary_directory="$HOME/.local/bin"
state_directory="$HOME/.local/share/mds"
destination="$binary_directory/mds"
marker="$state_directory/bootstrap-owner-v1"
for managed_directory in "$binary_directory" "$state_directory"; do
  if [ -L "$managed_directory" ] || {
    [ -e "$managed_directory" ] && [ ! -d "$managed_directory" ]
  }; then
    echo "refusing to use a non-directory or symlinked mds managed directory" >&2
    exit 73
  fi
done
install -d -m 0700 "$binary_directory" "$state_directory"

if [ -e "$destination" ] || [ -L "$destination" ]; then
  if [ -L "$destination" ] || [ ! -f "$destination" ] || [ ! -f "$marker" ]; then
    echo "refusing to replace guest-local mds without the mds ownership marker" >&2
    exit 73
  fi
fi

staged_binary="$binary_directory/.mds.new.$$"
staged_marker="$state_directory/.bootstrap-owner-v1.new.$$"
install -m 0700 "$temporary_directory/mds" "$staged_binary"
{
  printf 'schema=mds.guest-bootstrap/v1\n'
  printf 'archive_sha256=%s\n' "$expected_sha256"
} > "$staged_marker"
chmod 0600 "$staged_marker"
mv -f "$staged_binary" "$destination"
staged_binary=
mv -f "$staged_marker" "$marker"
staged_marker=
