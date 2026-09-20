#!/usr/bin/env bash
# Bootstrap from the official repository, then use the same signed recommendation
# and immutable-archive verification as ordinary updates and HostBox.
set -euo pipefail
[[ ${EUID:-$(id -u)} == 0 ]] || { echo 'Run with sudo or as root.' >&2; exit 1; }
for dependency in curl jq openssl sha256sum flock tar; do
  command -v "$dependency" >/dev/null || { echo "Missing $dependency; no installation was changed." >&2; exit 1; }
done
bootstrap_dir="$(mktemp -d)"
trap 'rm -rf -- "$bootstrap_dir"' EXIT
mkdir "$bootstrap_dir/hostbox"
# Pin both downloaded bootstrap components to the same official repository commit.
bootstrap_commit="$(curl -fsSL --retry 3 --connect-timeout 15 --max-time 60 https://api.github.com/repos/MatchHar/My-T-Companion/commits/main | jq -er '.sha')"
[[ $bootstrap_commit =~ ^[a-f0-9]{40}$ ]] || exit 1
for component in update.sh upgrade-policy.sh; do
  curl -fsSL --retry 3 --connect-timeout 15 --max-time 60 \
    "https://raw.githubusercontent.com/MatchHar/My-T-Companion/$bootstrap_commit/$component" -o "$bootstrap_dir/$component"
done
printf '%s\n' '-----BEGIN PUBLIC KEY-----' \
  'MCowBQYDK2VwAyEA8ZP2xr0aPD7VTKj3aTnK2ghGQA2xVDD3tIalHn1yLUs=' \
  '-----END PUBLIC KEY-----' > "$bootstrap_dir/hostbox/hostbox-catalog-signing-public.pem"
MY_T_VERSION=recommended bash "$bootstrap_dir/update.sh"
