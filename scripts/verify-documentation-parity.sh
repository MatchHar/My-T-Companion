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

rg -Uq 'every[[:space:]]+vehicle on that paired TeslaMate server' README.md || {
  echo 'English README must state the all-vehicle notification scope' >&2
  exit 1
}
rg -Uq '该[[:space:]]+iPhone 所配对 TeslaMate 服务器上的全部车辆' README.zh-Hans.md || {
  echo 'Simplified Chinese README must state the all-vehicle notification scope' >&2
  exit 1
}
rg -Uq 'iPhone 所配對 TeslaMate 伺服器上的全部車輛' README.zh-Hant.md || {
  echo 'Traditional Chinese README must state the all-vehicle notification scope' >&2
  exit 1
}

if rg -n 'selected vehicle is observed|its selected vehicle|指定车辆|指定車輛' \
  README.md README.zh-Hans.md README.zh-Hant.md; then
  echo 'Companion notification docs must not imply selected-vehicle filtering' >&2
  exit 1
fi

echo "Required Companion documentation is present in all three languages"
