#!/usr/bin/env bash
# Synthetic signed catalog and archive only. No network or live install paths.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
export fixture
mkdir -p "$fixture/runner/hostbox" "$fixture/install" "$fixture/source/my-t-companion-9.9.9"
cp "$repo_dir/update.sh" "$repo_dir/upgrade-policy.sh" "$fixture/runner/"
printf '9.9.8\n' > "$fixture/install/VERSION"
printf '9.9.9\n' > "$fixture/source/my-t-companion-9.9.9/VERSION"
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' 'printf "9.9.9\n" > "$INSTALL_DIR/VERSION"' > "$fixture/source/my-t-companion-9.9.9/install.sh"
chmod +x "$fixture/source/my-t-companion-9.9.9/install.sh"
tar -C "$fixture/source" -czf "$fixture/my-t-companion-9.9.9.tar.gz" my-t-companion-9.9.9
(cd "$fixture" && sha256sum my-t-companion-9.9.9.tar.gz > my-t-companion-9.9.9.tar.gz.sha256)
digest="$(sha256sum "$fixture/my-t-companion-9.9.9.tar.gz" | awk '{print $1}')"
jq -n --arg digest "$digest" '{schema_version:1,template_id:"myt-stack",channel:"stable",upstream:{companion:{follow_latest_release:false}},images:{companion:"myt/companion:9.9.9"},artifacts:{companion_archive_sha256:$digest}}' > "$fixture/catalog.json"
openssl genpkey -algorithm ED25519 -out "$fixture/test-only.pem" >/dev/null 2>&1
openssl pkey -in "$fixture/test-only.pem" -pubout -out "$fixture/runner/hostbox/hostbox-catalog-signing-public.pem" >/dev/null 2>&1
openssl pkeyutl -sign -inkey "$fixture/test-only.pem" -rawin -in "$fixture/catalog.json" -out "$fixture/signature.bin"
openssl base64 -A -in "$fixture/signature.bin" -out "$fixture/catalog.sig"
curl() {
  local output="" url=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -o|--output) output="$2"; shift 2 ;;
      https://*) url="$1"; shift ;;
      *) shift ;;
    esac
  done
  [[ -n "$output" && -n "$url" ]] || return 91
  case "$url" in
    */hostbox/myt-stack.json) cp "$fixture/catalog.json" "$output" ;;
    */hostbox/myt-stack.json.sig) cp "$fixture/catalog.sig" "$output" ;;
    */my-t-companion-9.9.9.tar.gz) cp "$fixture/my-t-companion-9.9.9.tar.gz" "$output" ;;
    */my-t-companion-9.9.9.tar.gz.sha256) cp "$fixture/my-t-companion-9.9.9.tar.gz.sha256" "$output" ;;
    *) return 92 ;;
  esac
}
export -f curl
INSTALL_DIR="$fixture/install" bash "$fixture/runner/update.sh"
[[ "$(cat "$fixture/install/VERSION")" == 9.9.9 ]]
[[ "$(cat "$fixture/install/RELEASE_SHA256")" == "$digest" ]]
# Repeated recommendation is a verified no-op.
INSTALL_DIR="$fixture/install" bash "$fixture/runner/update.sh"
# Same version with conflicting receipt fails closed.
printf '%064d\n' 0 > "$fixture/install/RELEASE_SHA256"
if INSTALL_DIR="$fixture/install" bash "$fixture/runner/update.sh"; then exit 1; fi
# A signature failure must not fall through to GitHub latest or another URL.
printf '\n' >> "$fixture/catalog.json"
if INSTALL_DIR="$fixture/install" bash "$fixture/runner/update.sh"; then exit 1; fi
[[ "$(cat "$fixture/install/VERSION")" == 9.9.9 ]]
printf 'signed recommendation updater tests passed\n'
