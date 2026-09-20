#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/my-t-companion}"
REPOSITORY="${MY_T_GITHUB_REPOSITORY:-MatchHar/My-T-Companion}"
REQUESTED_VERSION="${MY_T_VERSION:-recommended}"
SOURCE_OVERRIDE="${MY_T_UPDATE_SOURCE_DIR:-}"
RELEASE_BASE_OVERRIDE="${MY_T_RELEASE_BASE_URL:-}"
EXPECTED_SHA256="${MY_T_EXPECTED_SHA256:-}"

fail() {
  printf '[My T VPS Companion] ERROR: %s\n' "$*" >&2
  exit 1
}

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  fail "Run with sudo or as root."
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ -f "$SCRIPT_DIR/upgrade-policy.sh" ]] || fail "Release missing upgrade policy."
source "$SCRIPT_DIR/upgrade-policy.sh"
myt_acquire_operation_lock

if [[ -n "$SOURCE_OVERRIDE" ]]; then
  [[ -x "$SOURCE_OVERRIDE/install.sh" ]] || fail "Invalid MY_T_UPDATE_SOURCE_DIR."
  [[ -f "$SOURCE_OVERRIDE/VERSION" ]] || fail "Source override has no VERSION."
  myt_guard_version "$(tr -d '[:space:]' < "$SOURCE_OVERRIDE/VERSION")"
  exec "$SOURCE_OVERRIDE/install.sh"
fi

for command_name in curl sha256sum tar mktemp; do
  command -v "$command_name" >/dev/null 2>&1 || fail "Missing required command: $command_name"
done

work_dir="$(mktemp -d)"
cleanup() { rm -rf "$work_dir"; }
trap cleanup EXIT

if [[ "$REQUESTED_VERSION" == "latest" || "$REQUESTED_VERSION" == "recommended" ]]; then
  [[ -z "$RELEASE_BASE_OVERRIDE" ]] \
    || fail "MY_T_VERSION is required when MY_T_RELEASE_BASE_URL is used."
  for dependency in jq openssl; do
    command -v "$dependency" >/dev/null 2>&1 || fail "Missing $dependency; no installation was changed."
  done
  catalog_root="https://raw.githubusercontent.com/MatchHar/My-T-Companion/main/hostbox"
  public_key="$SCRIPT_DIR/hostbox/hostbox-catalog-signing-public.pem"
  [[ -s "$public_key" ]] || fail "Pinned catalog verification key missing."
  curl -fsSL --retry 3 --connect-timeout 15 --max-time 60 "$catalog_root/myt-stack.json" -o "$work_dir/catalog.json"
  curl -fsSL --retry 3 --connect-timeout 15 --max-time 60 "$catalog_root/myt-stack.json.sig" -o "$work_dir/catalog.sig"
  openssl base64 -d -A -in "$work_dir/catalog.sig" -out "$work_dir/catalog.signature"
  openssl pkeyutl -verify -pubin -inkey "$public_key" -rawin \
    -in "$work_dir/catalog.json" -sigfile "$work_dir/catalog.signature" >/dev/null \
    || fail "Recommended catalog signature is invalid."
  jq -e '.schema_version == 1 and .template_id == "myt-stack" and .channel == "stable" and .upstream.companion.follow_latest_release == false' "$work_dir/catalog.json" >/dev/null \
    || fail "Unsupported recommended catalog."
  REQUESTED_VERSION="$(jq -er '.images.companion | select(startswith("myt/companion:")) | ltrimstr("myt/companion:")' "$work_dir/catalog.json")"
  recommended_hash="$(jq -er '.artifacts.companion_archive_sha256' "$work_dir/catalog.json")"
  [[ -z "$EXPECTED_SHA256" || "$EXPECTED_SHA256" == "$recommended_hash" ]] || fail "Expected digest conflicts with signed recommendation."
  EXPECTED_SHA256="$recommended_hash"
fi
[[ "$REQUESTED_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
  || fail "Invalid release version: $REQUESTED_VERSION"
if [[ -n "$EXPECTED_SHA256" && ! "$EXPECTED_SHA256" =~ ^[a-f0-9]{64}$ ]]; then
  fail "MY_T_EXPECTED_SHA256 must be a lowercase SHA-256 digest."
fi

archive_name="my-t-companion-${REQUESTED_VERSION}.tar.gz"
release_base="${RELEASE_BASE_OVERRIDE:-https://github.com/${REPOSITORY}/releases/download/v${REQUESTED_VERSION}}"
myt_guard_version "$REQUESTED_VERSION"
if [[ -f "$INSTALL_DIR/VERSION" && "$(tr -d '[:space:]' < "$INSTALL_DIR/VERSION")" == "$REQUESTED_VERSION" ]]; then
  if [[ -n "$EXPECTED_SHA256" ]]; then
    [[ -f "$INSTALL_DIR/RELEASE_SHA256" && "$(tr -d '[:space:]' < "$INSTALL_DIR/RELEASE_SHA256")" == "$EXPECTED_SHA256" ]] \
      || fail "Same-version artifact cannot be verified or digest conflicts; refusing repair overwrite."
  fi
  printf '[My T VPS Companion] Already installed: %s\n' "$REQUESTED_VERSION"
  exit 0
fi

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
actual_sha256="$(sha256sum "$work_dir/$archive_name" | awk '{print $1}')"
if [[ -n "$EXPECTED_SHA256" && "$actual_sha256" != "$EXPECTED_SHA256" ]]; then
  fail "Release archive does not match MY_T_EXPECTED_SHA256."
fi

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
    if MY_T_ALLOW_DOWNGRADE=1 INSTALL_DIR="$INSTALL_DIR" "$backup_dir/install.sh"; then
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
printf '%s\n' "$actual_sha256" > "$INSTALL_DIR/RELEASE_SHA256"

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
