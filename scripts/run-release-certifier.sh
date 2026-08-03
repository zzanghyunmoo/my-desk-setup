#!/usr/bin/env bash
set -euo pipefail

if (( $# < 4 )); then
  echo "usage: $0 <prepare|certify|verify> --mds-evidence <fixed-release-path> --expected-mds-evidence-sha256 <sha256> [command options...]" >&2
  exit 2
fi

mode=$1
shift
case "$mode" in
  prepare|certify|verify) ;;
  *)
    echo "unsupported mds-evidence mode: $mode" >&2
    exit 2
    ;;
esac

certifier_path=
expected_sha256=
arguments=()
while (( $# > 0 )); do
  case "$1" in
    --mds-evidence)
      if (( $# < 2 )); then
        echo "--mds-evidence requires a value" >&2
        exit 2
      fi
      certifier_path=$2
      shift 2
      ;;
    --expected-mds-evidence-sha256)
      if (( $# < 2 )); then
        echo "--expected-mds-evidence-sha256 requires a value" >&2
        exit 2
      fi
      expected_sha256=$2
      shift 2
      ;;
    *)
      arguments+=("$1")
      shift
      ;;
  esac
done

if [[ ! "$certifier_path" = /* && ! "$certifier_path" =~ ^[A-Za-z]:[/\\] ]]; then
  echo "--mds-evidence must be an absolute fixed path" >&2
  exit 2
fi
if [[ ! "$expected_sha256" =~ ^[0-9a-f]{64}$ ]]; then
  echo "--expected-mds-evidence-sha256 must be a lowercase SHA-256" >&2
  exit 2
fi
if [[ ! -f "$certifier_path" || -L "$certifier_path" ]]; then
  echo "mds-evidence must be a regular non-symlink file: $certifier_path" >&2
  exit 2
fi

certifier_tmp=$(mktemp -d "${TMPDIR:-/tmp}/mds-release-certifier.XXXXXX")
cleanup() {
  rm -rf -- "$certifier_tmp"
}
trap cleanup EXIT HUP INT TERM

case "$certifier_path" in
  *.exe) snapshot="$certifier_tmp/mds-evidence.exe" ;;
  *) snapshot="$certifier_tmp/mds-evidence" ;;
esac
cp -- "$certifier_path" "$snapshot"
chmod u+x "$snapshot"

if command -v sha256sum >/dev/null 2>&1; then
  observed_sha256=$(sha256sum "$snapshot" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  observed_sha256=$(shasum -a 256 "$snapshot" | awk '{print $1}')
else
  echo "sha256sum or shasum is required" >&2
  exit 2
fi
if [[ "$observed_sha256" != "$expected_sha256" ]]; then
  echo "mds-evidence SHA-256 mismatch: got $observed_sha256" >&2
  exit 1
fi

"$snapshot" "$mode" "${arguments[@]}"
