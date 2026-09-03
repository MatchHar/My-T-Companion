#!/usr/bin/env bash
# shellcheck disable=SC2016 # Literal snippets are written into mock installers.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

make_release() {
  local version="$1"
  local install_body="$2"
  local release_dir="$test_dir/release-$version"
  local source_dir="$test_dir/source-$version/my-t-companion-$version"
  local archive="my-t-companion-$version.tar.gz"
  mkdir -p "$release_dir" "$source_dir"
  printf '%s\n' "$version" > "$source_dir/VERSION"
  printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' "$install_body" \
    > "$source_dir/install.sh"
  chmod +x "$source_dir/install.sh"
  cp "$repo_dir/install-source-transaction.sh" "$source_dir/install-source-transaction.sh"
  tar -C "$test_dir/source-$version" -czf "$release_dir/$archive" \
    "my-t-companion-$version"
  (
    cd "$release_dir"
    sha256sum "$archive" > "$archive.sha256"
  )
}

success_install="$test_dir/success-install"
mkdir "$success_install"
printf '%s\n' '#!/usr/bin/env bash' 'printf restored > "$INSTALL_DIR/state"' \
  > "$success_install/install.sh"
chmod +x "$success_install/install.sh"
printf old > "$success_install/state"
make_release "9.9.7" 'source "$(dirname "${BASH_SOURCE[0]}")/install-source-transaction.sh"; myt_begin_source_transaction; printf new > "$INSTALL_DIR/state"'
INSTALL_DIR="$success_install" \
MY_T_VERSION="9.9.7" \
MY_T_RELEASE_BASE_URL="file://$test_dir/release-9.9.7" \
  "$repo_dir/update.sh"
[[ "$(cat "$success_install/state")" == "new" ]]
compgen -G "$success_install.before-9.9.7-*" >/dev/null

failure_install="$test_dir/failure-install"
mkdir "$failure_install"
printf '%s\n' '#!/usr/bin/env bash' 'printf restored > "$INSTALL_DIR/state"' \
  > "$failure_install/install.sh"
chmod +x "$failure_install/install.sh"
printf old > "$failure_install/state"
make_release "9.9.8" 'printf broken > "$INSTALL_DIR/state"; exit 1'
if INSTALL_DIR="$failure_install" \
  MY_T_VERSION="9.9.8" \
  MY_T_RELEASE_BASE_URL="file://$test_dir/release-9.9.8" \
    "$repo_dir/update.sh"; then
  printf 'expected failed update to return non-zero\n' >&2
  exit 1
fi
[[ "$(cat "$failure_install/state")" == "restored" ]]

# The updater itself is unchanged from 1.10.39: the new installer must clean
# its novel source files BEFORE the older overwrite-only installer runs.
guarded_install="$test_dir/guarded-install"
mkdir "$guarded_install"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' \
  '[[ ! -e "$INSTALL_DIR/new-source.go" ]]' \
  'printf restored > "$INSTALL_DIR/state"' > "$guarded_install/install.sh"
chmod +x "$guarded_install/install.sh"
printf old > "$guarded_install/state"
printf user-owned > "$guarded_install/operator-notes"
make_release "9.9.9" 'source "$(dirname "${BASH_SOURCE[0]}")/install-source-transaction.sh"; myt_begin_source_transaction; printf broken > "$INSTALL_DIR/state"; printf incompatible > "$INSTALL_DIR/new-source.go"; exit 19'
if INSTALL_DIR="$guarded_install" \
  MY_T_VERSION="9.9.9" \
  MY_T_RELEASE_BASE_URL="file://$test_dir/release-9.9.9" \
    "$repo_dir/update.sh"; then
  printf 'expected guarded update failure\n' >&2; exit 1
fi
[[ "$(cat "$guarded_install/state")" == restored ]]
[[ "$(cat "$guarded_install/operator-notes")" == user-owned ]]
[[ ! -e "$guarded_install/new-source.go" ]]

printf 'updater lifecycle tests passed\n'
