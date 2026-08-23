#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

documents=(COMPATIBILITY SECURITY SUPPORT DATA_LIFECYCLE)
for base in "${documents[@]}"; do
  for file in "$base.md" "$base.zh-Hans.md" "$base.zh-Hant.md"; do
    [[ -s "$file" ]] || { echo "Missing localized document: $file" >&2; exit 1; }
    grep -q 'English' "$file" || { echo "Missing language navigation: $file" >&2; exit 1; }
    grep -q '简体中文' "$file" || { echo "Missing Simplified Chinese navigation: $file" >&2; exit 1; }
    grep -q '繁體中文' "$file" || { echo "Missing Traditional Chinese navigation: $file" >&2; exit 1; }
  done
  en_sections=$(grep -c '^## ' "$base.md" || true)
  hans_sections=$(grep -c '^## ' "$base.zh-Hans.md" || true)
  hant_sections=$(grep -c '^## ' "$base.zh-Hant.md" || true)
  [[ "$en_sections" == "$hans_sections" && "$en_sections" == "$hant_sections" ]] || {
    echo "Section-count mismatch for $base" >&2
    exit 1
  }
done

echo "Required Companion documentation is present in all three languages"
