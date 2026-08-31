#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# Public Companion must never contain developer-wide APNs provider credentials.
# Exclude this guard itself and the security documentation, where the forbidden
# marker names are intentionally documented as examples.
exclude_re='^(scripts/check-push-secret-boundary\.sh|PUSH_SECURITY\.md)$'

patterns=(
  '-----BEGIN PRIVATE KEY-----'
  '-----BEGIN EC PRIVATE KEY-----'
  'APNS_PRIVATE_KEY'
  'APNS_AUTH_KEY'
  'APNS_P8'
  'APPLE_PUSH_PRIVATE_KEY'
  'APNS_PROVIDER_TOKEN'
)

failed=0
for pattern in "${patterns[@]}"; do
  while IFS= read -r match; do
    path="${match%%:*}"
    if [[ "$path" =~ $exclude_re ]]; then
      continue
    fi
    printf 'forbidden push-provider secret marker: %s\n' "$match" >&2
    failed=1
  done < <(git grep -n -I -F -- "$pattern" -- . || true)
done

# A real Apple APNs key file should never be tracked in this public repo.
while IFS= read -r path; do
  case "$path" in
    *.p8)
      printf 'forbidden tracked APNs key file: %s\n' "$path" >&2
      failed=1
      ;;
  esac
done < <(git ls-files)

if [[ "$failed" -ne 0 ]]; then
  cat >&2 <<'EOF'

My T Companion is public/self-hosted. Apple APNs provider credentials belong
only in the developer-operated private Push Relay and Cloudflare encrypted
secrets. See PUSH_SECURITY.md.
EOF
  exit 1
fi

printf 'push secret boundary check passed\n'
