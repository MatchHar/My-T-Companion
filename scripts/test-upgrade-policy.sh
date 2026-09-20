#!/usr/bin/env bash
# Isolated filesystem tests only; never read or mutate a real installation.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
source "$repo_dir/upgrade-policy.sh"
fail() { printf '%s\n' "$*" >&2; exit 19; }
INSTALL_DIR="$test_dir/install"
mkdir "$INSTALL_DIR"
myt_guard_version 1.10.52
printf '1.10.52\n' > "$INSTALL_DIR/VERSION"
myt_guard_version 1.10.52
myt_guard_version 1.10.100
myt_guard_version 2.0.0
if (myt_guard_version 1.10.51); then exit 1; fi
if (myt_guard_version 1.9.100); then exit 1; fi
MY_T_ALLOW_DOWNGRADE=1 myt_guard_version 1.10.51
printf unknown > "$INSTALL_DIR/VERSION"
if (myt_guard_version 1.10.52); then exit 1; fi
mv "$INSTALL_DIR/VERSION" "$INSTALL_DIR/install.sh"
if (myt_guard_version 1.10.52); then exit 1; fi
if (myt_guard_version '1.10.52;bad'); then exit 1; fi
INSTALL_DIR="$test_dir/fresh"
myt_acquire_operation_lock
export INSTALL_DIR
export -f fail
# Child installer retains our fd and must not deadlock on its parent's lock.
bash -c 'source "$1"; myt_acquire_operation_lock' _ "$repo_dir/upgrade-policy.sh"
# A separate process without the inherited marker/fd must fail immediately.
if MY_T_OPERATION_LOCK_HELD=0 bash -c 'exec 9>&-; source "$1"; myt_acquire_operation_lock' _ "$repo_dir/upgrade-policy.sh"; then exit 1; fi
printf 'upgrade-policy tests passed\n'
