#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/my-t-companion}"
REPOSITORY="${MY_T_GITHUB_REPOSITORY:-MatchHar/My-T-Companion}"
REQUESTED_VERSION="${MY_T_VERSION:-latest}"
SOURCE_OVERRIDE="${MY_T_UPDATE_SOURCE_DIR:-}"
RELEASE_BASE_OVERRIDE="${MY_T_RELEASE_BASE_URL:-}"

fail() {
  printf '[My T VPS Companion] ERROR: %s\n' "$*" >&2
  exit 1
}

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  fail "Run with sudo or as root."
fi

if [[ -n "$SOURCE_OVERRIDE" ]]; then
  [[ -x "$SOURCE_OVERRIDE/install.sh" ]] || fail "Invalid MY_T_UPDATE_SOURCE_DIR."
  exec "$SOURCE_OVERRIDE/install.sh"
fi

for command_name in curl sha256sum tar mktemp; do
  command -v "$command_name" >/dev/null 2>&1 || fail "Missing required command: $command_name"
done

if [[ "$REQUESTED_VERSION" == "latest" ]]; then
  [[ -z "$RELEASE_BASE_OVERRIDE" ]] \
    || fail "MY_T_VERSION is required when MY_T_RELEASE_BASE_URL is used."
  release_url="$(
    curl --fail --silent --show-error --location \
      --retry 3 --retry-delay 2 --connect-timeout 15 \
      --output /dev/null --write-out '%{url_effective}' \
      "https://github.com/${REPOSITORY}/releases/latest"
  )"
  REQUESTED_VERSION="${release_url##*/v}"
fi
[[ "$REQUESTED_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
  || fail "Invalid release version: $REQUESTED_VERSION"

archive_name="my-t-companion-${REQUESTED_VERSION}.tar.gz"
release_base="${RELEASE_BASE_OVERRIDE:-https://github.com/${REPOSITORY}/releases/download/v${REQUESTED_VERSION}}"
work_dir="$(mktemp -d)"
cleanup() { rm -rf "$work_dir"; }
trap cleanup EXIT

printf '[My T VPS Companion] Downloading verified release %s\n' "$REQUESTED_VERSION"
curl --fail --silent --show-error --location \
  --retry 3 --retry-delay 2 --connect-timeout 15 \
  --output "$work_dir/$archive_name" "$release_base/$archive_name"
curl --fail --silent --show-error --location \
  --retry 3 --retry-delay 2 --connect-timeout 15 \
  --output "$work_dir/$archive_name.sha256" "$release_base/$archive_name.sha256"
(
  cd "$work_dir"
  sha256sum --check "$archive_name.sha256"
)

mkdir "$work_dir/source"
tar --extract --gzip --file "$work_dir/$archive_name" \
  --directory "$work_dir/source" --strip-components=1
[[ -x "$work_dir/source/install.sh" ]] || fail "Release archive is incomplete."
[[ "$(tr -d '[:space:]' < "$work_dir/source/VERSION")" == "$REQUESTED_VERSION" ]] \
  || fail "Release VERSION does not match the requested version."

backup_dir=""
if [[ -d "$INSTALL_DIR" ]]; then
  backup_dir="${INSTALL_DIR}.before-${REQUESTED_VERSION}-$(date +%Y%m%d-%H%M%S)"
  cp -a "$INSTALL_DIR" "$backup_dir"
  printf '[My T VPS Companion] Existing installation backed up to %s\n' "$backup_dir"
fi

if ! INSTALL_DIR="$INSTALL_DIR" "$work_dir/source/install.sh"; then
  if [[ -n "$backup_dir" && -x "$backup_dir/install.sh" ]]; then
    printf '[My T VPS Companion] Update failed; restoring the previous version\n' >&2
    if INSTALL_DIR="$INSTALL_DIR" "$backup_dir/install.sh"; then
      printf '[My T VPS Companion] Previous version restored; backup retained at %s\n' "$backup_dir" >&2
    else
      printf '[My T VPS Companion] Automatic restore failed; backup retained at %s\n' "$backup_dir" >&2
    fi
  else
    printf '[My T VPS Companion] Update failed before an existing installation could be backed up\n' >&2
  fi
  exit 1
fi

printf '[My T VPS Companion] Updated successfully to %s\n' "$REQUESTED_VERSION"

# Retain only the three newest source/configuration rollback copies. Durable
# parking data lives in the named volume and is handled by backup.sh.
mapfile -t old_backups < <(
  find "$(dirname "$INSTALL_DIR")" -maxdepth 1 -type d \
    -name "$(basename "$INSTALL_DIR").before-*" -printf '%T@ %p\n' |
    sort -rn | tail -n +4 | cut -d' ' -f2-
)
for old_backup in "${old_backups[@]}"; do
  rm -rf -- "$old_backup"
done
