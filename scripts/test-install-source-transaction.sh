#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
export INSTALL_DIR="$test_dir/installed"
mkdir -p "$INSTALL_DIR"
printf old > "$INSTALL_DIR/main.go"
printf private > "$INSTALL_DIR/.env"
chmod 0600 "$INSTALL_DIR/.env"
printf user-owned > "$INSTALL_DIR/operator-notes"
cp -a "$INSTALL_DIR" "$test_dir/expected"

# Exercise the actual installer guard, including a novel incompatible source
# file: the precise case missed by the previous overwrite-only rollback test.
if bash -c 'set -euo pipefail; source "$1"; myt_begin_source_transaction;
  printf broken > "$INSTALL_DIR/main.go";
  printf incompatible > "$INSTALL_DIR/navigation_leg.go";
  printf changed > "$INSTALL_DIR/.env"; exit 17' bash "$repo_dir/install-source-transaction.sh"; then
  printf 'expected installer failure\n' >&2; exit 1
fi
diff -r "$test_dir/expected" "$INSTALL_DIR"
[[ ! -f "$INSTALL_DIR/navigation_leg.go" ]]

# A successful install retains its new sources; guard also works in-place.
bash -c 'set -euo pipefail; source "$1"; myt_begin_source_transaction;
  printf new > "$INSTALL_DIR/navigation_leg.go"' bash "$repo_dir/install-source-transaction.sh"
[[ "$(cat "$INSTALL_DIR/navigation_leg.go")" == new ]]
[[ "$(cat "$INSTALL_DIR/operator-notes")" == user-owned ]]

# Interrupted / first-time installations return failure without stray context.
export INSTALL_DIR="$test_dir/new-install"
if bash -c 'set -euo pipefail; source "$1"; myt_begin_source_transaction;
  mkdir "$INSTALL_DIR"; printf partial > "$INSTALL_DIR/main.go"; kill -TERM $$' bash "$repo_dir/install-source-transaction.sh"; then
  printf 'expected interrupted installer failure\n' >&2; exit 1
fi
[[ ! -e "$INSTALL_DIR" ]]
grep -q 'myt_begin_source_transaction' "$repo_dir/install.sh"
printf 'installer source transaction tests passed\n'
