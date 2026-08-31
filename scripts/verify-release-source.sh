#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tag="${1:-${GITHUB_REF_NAME:-}}"
expected_commit="${2:-${GITHUB_SHA:-HEAD}}"
main_ref="${3:-origin/main}"

if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH [expected-commit] [main-ref]" >&2
  exit 2
fi

version="$(tr -d '[:space:]' < VERSION)"
if [[ "$tag" != "v$version" ]]; then
  echo "release tag $tag does not match VERSION $version" >&2
  exit 1
fi

tag_commit="$(git rev-parse --verify "${tag}^{commit}")"
resolved_expected="$(git rev-parse --verify "${expected_commit}^{commit}")"
main_commit="$(git rev-parse --verify "${main_ref}^{commit}")"

if [[ "$tag_commit" != "$resolved_expected" ]]; then
  echo "release tag $tag points to $tag_commit, not workflow commit $resolved_expected" >&2
  exit 1
fi

if ! git merge-base --is-ancestor "$tag_commit" "$main_commit"; then
  echo "release tag $tag is not contained in $main_ref ($main_commit)" >&2
  exit 1
fi

printf 'release source verified: %s -> %s (contained in %s)\n' "$tag" "$tag_commit" "$main_ref"
