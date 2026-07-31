#!/bin/sh
set -eu

source_mode=$1
artifact_url=$2
expected_sha256=$3

case "$artifact_url" in
  https://*[\?\#]* | https://*@*)
    echo "guest bootstrap artifact URL must be credential-free HTTPS without query or fragment" >&2
    exit 2
    ;;
  https://*) ;;
  *)
    echo "guest bootstrap requires an HTTPS artifact URL" >&2
    exit 2
    ;;
esac

umask 077
ulimit -f 524288
temporary_directory=$(mktemp -d)
staged_binary=
staged_marker=
staged_transaction=
cleanup() {
  rm -rf "$temporary_directory"
  if [ -n "$staged_binary" ]; then
    rm -f "$staged_binary"
  fi
  if [ -n "$staged_marker" ]; then
    rm -f "$staged_marker"
  fi
  if [ -n "$staged_transaction" ]; then
    rm -f "$staged_transaction"
  fi
}
trap cleanup EXIT HUP INT TERM

archive="$temporary_directory/mds.tar.gz"
case "$source_mode" in
  url)
    if command -v curl >/dev/null 2>&1; then
      effective_url=$(curl --fail --show-error --silent \
        --location --max-redirs 3 --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --connect-timeout 30 --max-time 600 --max-filesize 268435456 \
        --output "$archive" --write-out '%{url_effective}' "$artifact_url")
      case "$effective_url" in
        https://*) ;;
        *)
          echo "guest bootstrap redirect must remain HTTPS" >&2
          exit 1
          ;;
      esac
      effective_authority=${effective_url#https://}
      effective_authority=${effective_authority%%/*}
      case "$effective_authority" in
        "" | *@*)
          echo "guest bootstrap redirect must not contain userinfo" >&2
          exit 1
          ;;
      esac
    else
      echo "guest bootstrap requires curl" >&2
      exit 69
    fi
    ;;
  stdin)
    if ! cat > "$archive"; then
      echo "guest bootstrap local archive exceeds the 256 MiB limit" >&2
      exit 1
    fi
    ;;
  *)
    echo "guest bootstrap source mode must be url or stdin" >&2
    exit 2
    ;;
esac

printf '%s  %s\n' "$expected_sha256" "$archive" | sha256sum -c -
if [ "$(tar -tzf "$archive")" != "mds" ]; then
  echo "guest bootstrap archive must contain exactly one mds entry" >&2
  exit 1
fi
tar -xzf "$archive" -C "$temporary_directory" mds
if [ ! -f "$temporary_directory/mds" ] || [ -L "$temporary_directory/mds" ]; then
  echo "guest bootstrap mds entry must be a regular file" >&2
  exit 1
fi

binary_directory="$HOME/.local/bin"
state_directory="$HOME/.local/share/mds"
destination="$binary_directory/mds"
marker="$state_directory/bootstrap-owner-v1"
transaction="$state_directory/bootstrap-transaction-v1"
for managed_directory in "$binary_directory" "$state_directory"; do
  if [ -L "$managed_directory" ] || {
    [ -e "$managed_directory" ] && [ ! -d "$managed_directory" ]
  }; then
    echo "refusing to use a non-directory or symlinked mds managed directory" >&2
    exit 73
  fi
done
install -d -m 0700 "$binary_directory" "$state_directory"
if ! command -v sync >/dev/null 2>&1; then
  echo "guest bootstrap requires sync for durable publication" >&2
  exit 69
fi

staged_binary="$binary_directory/.mds.new.$$"
staged_marker="$state_directory/.bootstrap-owner-v1.new.$$"
staged_transaction="$state_directory/.bootstrap-transaction-v1.new.$$"
install -m 0700 "$temporary_directory/mds" "$staged_binary"
sync -f "$staged_binary"
next_binary_sha256=$(sha256sum "$staged_binary")
next_binary_sha256=${next_binary_sha256%% *}

valid_sha256() {
  [ "${#1}" -eq 64 ] || return 1
  case "$1" in
    *[!0-9a-f]*) return 1 ;;
  esac
}

read_owner_marker() {
  owner_schema=
  owner_archive_sha256=
  owner_binary_sha256=
  owner_lines=0
  while IFS= read -r owner_line || [ -n "$owner_line" ]; do
    owner_lines=$((owner_lines + 1))
    case "$owner_line" in
      schema=mds.guest-bootstrap/v1)
        [ -z "$owner_schema" ] || return 1
        owner_schema=mds.guest-bootstrap/v1
        ;;
      archive_sha256=*)
        [ -z "$owner_archive_sha256" ] || return 1
        owner_archive_sha256=${owner_line#archive_sha256=}
        ;;
      binary_sha256=*)
        [ -z "$owner_binary_sha256" ] || return 1
        owner_binary_sha256=${owner_line#binary_sha256=}
        ;;
      *) return 1 ;;
    esac
  done < "$marker"
  [ "$owner_lines" -eq 3 ] &&
    [ "$owner_schema" = mds.guest-bootstrap/v1 ] &&
    valid_sha256 "$owner_archive_sha256" &&
    valid_sha256 "$owner_binary_sha256"
}

read_transaction_marker() {
  transaction_schema=
  transaction_archive_sha256=
  transaction_previous_binary_sha256=
  transaction_next_binary_sha256=
  transaction_lines=0
  while IFS= read -r transaction_line || [ -n "$transaction_line" ]; do
    transaction_lines=$((transaction_lines + 1))
    case "$transaction_line" in
      schema=mds.guest-bootstrap-transaction/v1)
        [ -z "$transaction_schema" ] || return 1
        transaction_schema=mds.guest-bootstrap-transaction/v1
        ;;
      archive_sha256=*)
        [ -z "$transaction_archive_sha256" ] || return 1
        transaction_archive_sha256=${transaction_line#archive_sha256=}
        ;;
      previous_binary_sha256=*)
        [ -z "$transaction_previous_binary_sha256" ] || return 1
        transaction_previous_binary_sha256=${transaction_line#previous_binary_sha256=}
        ;;
      next_binary_sha256=*)
        [ -z "$transaction_next_binary_sha256" ] || return 1
        transaction_next_binary_sha256=${transaction_line#next_binary_sha256=}
        ;;
      *) return 1 ;;
    esac
  done < "$transaction"
  [ "$transaction_lines" -eq 4 ] &&
    [ "$transaction_schema" = mds.guest-bootstrap-transaction/v1 ] &&
    valid_sha256 "$transaction_archive_sha256" &&
    {
      [ "$transaction_previous_binary_sha256" = absent ] ||
        valid_sha256 "$transaction_previous_binary_sha256"
    } &&
    valid_sha256 "$transaction_next_binary_sha256"
}

current_binary_sha256=absent
if [ -e "$destination" ] || [ -L "$destination" ]; then
  if [ -L "$destination" ] || [ ! -f "$destination" ]; then
    echo "refusing to replace a non-regular or symlinked guest-local mds" >&2
    exit 73
  fi
  current_binary_sha256=$(sha256sum "$destination")
  current_binary_sha256=${current_binary_sha256%% *}
fi

if [ -e "$transaction" ] || [ -L "$transaction" ]; then
  if [ -L "$transaction" ] || [ ! -f "$transaction" ] ||
    ! read_transaction_marker ||
    [ "$transaction_archive_sha256" != "$expected_sha256" ] ||
    [ "$transaction_next_binary_sha256" != "$next_binary_sha256" ] ||
    {
      [ "$current_binary_sha256" != "$transaction_previous_binary_sha256" ] &&
        [ "$current_binary_sha256" != "$transaction_next_binary_sha256" ]
    }; then
    echo "refusing to resume an invalid or divergent guest bootstrap transaction" >&2
    exit 73
  fi
else
  if [ "$current_binary_sha256" != absent ]; then
    if [ -L "$marker" ] || [ ! -f "$marker" ] ||
      ! read_owner_marker ||
      [ "$owner_binary_sha256" != "$current_binary_sha256" ]; then
      echo "refusing to replace guest-local mds without a matching mds ownership marker" >&2
      exit 73
    fi
  fi
  {
    printf 'schema=mds.guest-bootstrap-transaction/v1\n'
    printf 'archive_sha256=%s\n' "$expected_sha256"
    printf 'previous_binary_sha256=%s\n' "$current_binary_sha256"
    printf 'next_binary_sha256=%s\n' "$next_binary_sha256"
  } > "$staged_transaction"
  chmod 0600 "$staged_transaction"
  sync -f "$staged_transaction"
  mv -f "$staged_transaction" "$transaction"
  staged_transaction=
  sync -f "$state_directory"
fi

if [ "$current_binary_sha256" != "$next_binary_sha256" ]; then
  mv -f "$staged_binary" "$destination"
  staged_binary=
  sync -f "$binary_directory"
else
  rm -f "$staged_binary"
  staged_binary=
fi
{
  printf 'schema=mds.guest-bootstrap/v1\n'
  printf 'archive_sha256=%s\n' "$expected_sha256"
  printf 'binary_sha256=%s\n' "$next_binary_sha256"
} > "$staged_marker"
chmod 0600 "$staged_marker"
sync -f "$staged_marker"
mv -f "$staged_marker" "$marker"
staged_marker=
sync -f "$state_directory"
rm -f "$transaction"
sync -f "$state_directory"
