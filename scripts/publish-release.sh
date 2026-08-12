#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

version="${tag#v}"
notes_file="RELEASE_NOTES_${version}.md"
title="My T Companion ${version}"
archive="dist/my-t-companion-${version}.tar.gz"
checksum="${archive}.sha256"
assets=("$archive" "$checksum")
for required in "${assets[@]}"; do
  if [[ ! -f "$required" ]]; then
    echo "release asset is missing: $required; run scripts/build-release.sh first" >&2
    exit 1
  fi
done

check_sha256() {
  local checksum_file="$1"
  local checksum_dir
  checksum_dir="$(cd "$(dirname "$checksum_file")" && pwd)"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$checksum_dir" && sha256sum --check "$(basename "$checksum_file")")
  else
    (cd "$checksum_dir" && shasum -a 256 --check "$(basename "$checksum_file")")
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

check_sha256 "$checksum"
if [[ ! -f "$notes_file" ]]; then
  echo "release notes are missing: $notes_file" >&2
  exit 1
fi
for localized_notes in "RELEASE_NOTES_${version}".*.md; do
  [[ -f "$localized_notes" ]] || continue
  assets+=("$localized_notes")
done

if ! gh release view "$tag" >/dev/null 2>&1; then
  create_args=("$tag" "${assets[@]}" --verify-tag --title "$title")
  if [[ -f "$notes_file" ]]; then
    create_args+=(--notes-file "$notes_file")
  else
    create_args+=(--generate-notes)
  fi
  gh release create "${create_args[@]}"
  exit 0
fi

# A manual release may win the race with the tag workflow. Reuse it only when
# every existing same-named asset has the exact local digest; never delete a
# valid published asset merely to make a retry green.
remote_assets="$(gh release view "$tag" --json assets --jq '.assets[] | [.name, .digest] | @tsv')"

verification_dir="$(mktemp -d)"
trap 'rm -rf "$verification_dir"' EXIT
archive_name="$(basename "$archive")"
checksum_name="$(basename "$checksum")"
if printf '%s\n' "$remote_assets" | awk -F '\t' -v name="$archive_name" '$1 == name { found=1 } END { exit !found }' &&
   printf '%s\n' "$remote_assets" | awk -F '\t' -v name="$checksum_name" '$1 == name { found=1 } END { exit !found }'; then
  gh release download "$tag" \
    --pattern "$archive_name" \
    --pattern "$checksum_name" \
    --dir "$verification_dir"
  check_sha256 "$verification_dir/$checksum_name"
fi

missing_assets=()
for asset in "${assets[@]}"; do
  name="$(basename "$asset")"
  local_digest="sha256:$(sha256_of "$asset")"
  remote_digest="$(printf '%s\n' "$remote_assets" | awk -F '\t' -v asset_name="$name" \
    '$1 == asset_name { print $2; exit }')"
  if [[ -z "$remote_digest" ]]; then
    missing_assets+=("$asset")
  elif [[ "$remote_digest" != "$local_digest" ]]; then
    echo "existing immutable asset digest mismatch: $name ($remote_digest != $local_digest)" >&2
    exit 1
  fi
done

if [[ -f "$notes_file" ]]; then
  gh release edit "$tag" --verify-tag --title "$title" --notes-file "$notes_file"
else
  gh release edit "$tag" --verify-tag --title "$title"
fi
if ((${#missing_assets[@]})); then
  gh release upload "$tag" "${missing_assets[@]}"
fi
