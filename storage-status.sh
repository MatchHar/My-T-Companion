#!/usr/bin/env bash
set -euo pipefail

COMPOSE_PROJECT="${COMPOSE_PROJECT:-my-t-companion}"
volume="${COMPOSE_PROJECT}_notification-state"
docker volume inspect "$volume" >/dev/null
printf 'My T Companion storage policy\n'
printf '  Parking events: long-term, newest 50,000 by default\n'
printf '  Software push deduplication: 180 days / 1,000 entries\n'
printf '  Charging push deduplication: 14 days / 2,000 entries\n'
printf '  Navigation push deduplication: 7 days / 2,000 entries\n'
printf '  Active charging/navigation snapshots: 48 hours / 12 hours\n'
printf '\nStored files (contents and secrets are never printed):\n'
docker run --rm -v "$volume:/data:ro" alpine:3.20 sh -c '
  for f in /data/*.json; do
    [ -f "$f" ] || continue
    printf "  %-38s %10s bytes\n" "$(basename "$f")" "$(wc -c < "$f")"
  done
  printf "  %-38s %10s bytes\n" "total" "$(du -sb /data | cut -f1)"
'
