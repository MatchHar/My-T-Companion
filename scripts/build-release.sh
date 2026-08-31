#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"$repo_dir/scripts/check-docker-sources.sh"
"$repo_dir/scripts/check-network-boundary.sh"
"$repo_dir/scripts/check-push-secret-boundary.sh"
version="$(tr -d '[:space:]' < "$repo_dir/VERSION")"
output_dir="${1:-$repo_dir/dist}"
archive_name="my-t-companion-${version}.tar.gz"
mkdir -p "$output_dir"
# Let Git produce the compressed archive directly. Unlike a second tar/gzip
# pass, this keeps identical commits byte-for-byte reproducible across reruns.
git -C "$repo_dir" archive \
  --format=tar.gz \
  --prefix="my-t-companion-${version}/" \
  --output="$output_dir/$archive_name" \
  HEAD
(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$archive_name" > "$archive_name.sha256"
  else
    shasum -a 256 "$archive_name" > "$archive_name.sha256"
  fi
)
printf '%s\n' "$output_dir/$archive_name"
