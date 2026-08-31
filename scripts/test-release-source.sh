#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_repo="$(mktemp -d)"
trap 'rm -rf "$test_repo"' EXIT

git -C "$test_repo" init -q -b main
git -C "$test_repo" config user.name 'Release Source Test'
git -C "$test_repo" config user.email 'release-source-test@example.invalid'
mkdir -p "$test_repo/scripts"
cp "$repo_root/scripts/verify-release-source.sh" "$test_repo/scripts/verify-release-source.sh"
printf '9.9.9\n' > "$test_repo/VERSION"
git -C "$test_repo" add VERSION scripts/verify-release-source.sh
git -C "$test_repo" commit -qm 'main release source'
git -C "$test_repo" tag v9.9.9

(cd "$test_repo" && scripts/verify-release-source.sh v9.9.9 HEAD main)

git -C "$test_repo" switch -qc side-release
printf '9.9.10\n' > "$test_repo/VERSION"
git -C "$test_repo" add VERSION
git -C "$test_repo" commit -qm 'unmerged release source'
git -C "$test_repo" tag v9.9.10

if (cd "$test_repo" && scripts/verify-release-source.sh v9.9.10 HEAD main); then
  echo 'expected an unmerged release tag to fail provenance verification' >&2
  exit 1
fi

printf 'release source tests passed\n'
