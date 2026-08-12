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
assets=(dist/*)
if [[ ! -e "${assets[0]}" ]]; then
  echo "release assets are missing; run scripts/build-release.sh first" >&2
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
if printf '%s\n' "$remote_assets" | awk -F '\t' '$1 ~ /\.tar\.gz$/ { found=1 } END { exit !found }' &&
   printf '%s\n' "$remote_assets" | awk -F '\t' '$1 ~ /\.tar\.gz\.sha256$/ { found=1 } END { exit !found }'; then
  gh release download "$tag" \
    --pattern 'my-t-companion-*.tar.gz' \
    --pattern 'my-t-companion-*.tar.gz.sha256' \
    --dir "$verification_dir"
  (
    cd "$verification_dir"
    sha256sum --check ./*.tar.gz.sha256
  )
fi

missing_assets=()
for asset in "${assets[@]}"; do
  name="$(basename "$asset")"
  local_digest="sha256:$(sha256sum "$asset" | awk '{print $1}')"
  remote_digest="$(printf '%s\n' "$remote_assets" | awk -F '\t' -v asset_name="$name" \
    '$1 == asset_name { print $2; exit }')"
  if [[ -z "$remote_digest" ]]; then
    missing_assets+=("$asset")
  elif [[ "$remote_digest" != "$local_digest" ]]; then
    echo "keeping existing immutable asset $name ($remote_digest)"
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
