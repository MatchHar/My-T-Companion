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

release_exists=true
if ! gh release view "$tag" >/dev/null 2>&1; then
  release_exists=false
  # Keep the release non-public until every asset is uploaded and its GitHub
  # digest matches the locally verified file. Failed jobs leave a safe draft.
  gh release create "$tag" \
    --draft \
    --verify-tag \
    --title "$title" \
    --notes-file "$notes_file"
fi

release_state="$(gh release view "$tag" --json isDraft,isImmutable --jq '[.isDraft, .isImmutable] | @tsv')"
is_draft="$(printf '%s\n' "$release_state" | awk -F '\t' '{print $1}')"
is_immutable="$(printf '%s\n' "$release_state" | awk -F '\t' '{print $2}')"
if [[ "$is_draft" != "true" && "$is_draft" != "false" ]]; then
  echo "unable to determine draft state for $tag" >&2
  exit 1
fi

# A manual draft may win the race with the tag workflow. Reuse it only when
# every existing same-named asset has the exact local digest. Once public,
# never edit the release or upload a missing file.
remote_assets="$(gh release view "$tag" --json assets --jq '.assets[] | [.name, .digest] | @tsv')"
missing_assets=()
for asset in "${assets[@]}"; do
  name="$(basename "$asset")"
  local_digest="sha256:$(sha256_of "$asset")"
  remote_digest="$(printf '%s\n' "$remote_assets" | awk -F '\t' -v asset_name="$name" \
    '$1 == asset_name { print $2; exit }')"
  if [[ -z "$remote_digest" ]]; then
    missing_assets+=("$asset")
  elif [[ "$remote_digest" != "$local_digest" ]]; then
    echo "existing release asset digest mismatch: $name ($remote_digest != $local_digest)" >&2
    exit 1
  fi
done

if [[ "$is_draft" == "false" ]]; then
  if ((${#missing_assets[@]})); then
    printf 'published release %s is missing immutable assets:\n' "$tag" >&2
    printf '  %s\n' "${missing_assets[@]}" >&2
    exit 1
  fi
  printf 'published release %s already matches all local assets (immutable=%s)\n' "$tag" "$is_immutable"
  exit 0
fi

if ((${#missing_assets[@]})); then
  gh release upload "$tag" "${missing_assets[@]}"
fi

# Re-read GitHub's digests after upload; do not publish on a partial or changed
# asset set even when the upload command itself returned success.
remote_assets="$(gh release view "$tag" --json assets --jq '.assets[] | [.name, .digest] | @tsv')"
for asset in "${assets[@]}"; do
  name="$(basename "$asset")"
  local_digest="sha256:$(sha256_of "$asset")"
  remote_digest="$(printf '%s\n' "$remote_assets" | awk -F '\t' -v asset_name="$name" \
    '$1 == asset_name { print $2; exit }')"
  if [[ -z "$remote_digest" ]]; then
    echo "draft release asset is missing after upload: $name" >&2
    exit 1
  fi
  if [[ "$remote_digest" != "$local_digest" ]]; then
    echo "draft release asset digest mismatch: $name ($remote_digest != $local_digest)" >&2
    exit 1
  fi
done

gh release edit "$tag" \
  --verify-tag \
  --title "$title" \
  --notes-file "$notes_file" \
  --draft=false

published_state="$(gh release view "$tag" --json isDraft,isImmutable --jq '[.isDraft, .isImmutable] | @tsv')"
if [[ "$(printf '%s\n' "$published_state" | awk -F '\t' '{print $1}')" != "false" ]]; then
  echo "release $tag did not leave draft state" >&2
  exit 1
fi
printf 'published verified release %s (created=%s, immutable=%s)\n' \
  "$tag" "$([[ "$release_exists" == false ]] && printf true || printf false)" \
  "$(printf '%s\n' "$published_state" | awk -F '\t' '{print $2}')"
