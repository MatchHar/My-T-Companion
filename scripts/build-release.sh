#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(tr -d '[:space:]' < "$repo_dir/VERSION")"
output_dir="${1:-$repo_dir/dist}"
archive_name="my-t-companion-${version}.tar.gz"
stage_dir="$(mktemp -d)"
trap 'rm -rf "$stage_dir"' EXIT

mkdir -p "$output_dir" "$stage_dir/my-t-companion-${version}"
git -C "$repo_dir" archive HEAD \
  | tar -x -C "$stage_dir/my-t-companion-${version}"
COPYFILE_DISABLE=1 tar -C "$stage_dir" -czf "$output_dir/$archive_name" \
  "my-t-companion-${version}"
(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$archive_name" > "$archive_name.sha256"
  else
    shasum -a 256 "$archive_name" > "$archive_name.sha256"
  fi
)
printf '%s\n' "$output_dir/$archive_name"
