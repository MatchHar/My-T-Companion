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

contains_flattened() {
  local document="$1"
  local expected="$2"
  local flattened
  flattened=$(tr '\n' ' ' < "$document")
  grep -Fq "$expected" <<<"$flattened"
}

contains_flattened README.md 'every vehicle on that paired TeslaMate server' || {
  echo 'English README must state the all-vehicle notification scope' >&2
  exit 1
}
contains_flattened README.zh-Hans.md '该 iPhone 所配对 TeslaMate 服务器上的全部车辆' || {
  echo 'Simplified Chinese README must state the all-vehicle notification scope' >&2
  exit 1
}
contains_flattened README.zh-Hant.md 'iPhone 所配對 TeslaMate 伺服器上的全部車輛' || {
  echo 'Traditional Chinese README must state the all-vehicle notification scope' >&2
  exit 1
}

contains_flattened README.md 'one stable alias for every TeslaMate car on the paired server' || {
  echo 'English README must state the all-vehicle anonymous inventory scope' >&2
  exit 1
}
contains_flattened README.zh-Hans.md '为该已配对服务器上的每辆 TeslaMate 车辆' || {
  echo 'Simplified Chinese README must state the all-vehicle anonymous inventory scope' >&2
  exit 1
}
contains_flattened README.zh-Hant.md '為該已配對伺服器上的每輛 TeslaMate 車輛' || {
  echo 'Traditional Chinese README must state the all-vehicle anonymous inventory scope' >&2
  exit 1
}

contains_flattened README.md 'My T iOS source, the developer-operated relay implementation' || {
  echo 'English README must state the public/private project boundary' >&2
  exit 1
}
contains_flattened README.zh-Hans.md 'My T iOS 源代码、开发者运营的中继实现' || {
  echo 'Simplified Chinese README must state the public/private project boundary' >&2
  exit 1
}
contains_flattened README.zh-Hant.md 'My T iOS 原始碼、開發者營運的中繼實作' || {
  echo 'Traditional Chinese README must state the public/private project boundary' >&2
  exit 1
}

if grep -nE 'selected vehicle is observed|its selected vehicle|selected TeslaMate car|指定车辆|指定車輛' \
  README.md README.zh-Hans.md README.zh-Hant.md; then
  echo 'Companion notification docs must not imply selected-vehicle filtering' >&2
  exit 1
fi

echo "Required Companion documentation is present in all three languages"
