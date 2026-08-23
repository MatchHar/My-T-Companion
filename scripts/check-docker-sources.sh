#!/usr/bin/env bash
# Fail if a production *.go file will not be in the Docker build context.
# 1.10.24 listed sources in Dockerfile and missed push_subscribers.go.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
df="$repo_dir/Dockerfile"
[[ -f "$df" ]] || { echo "missing Dockerfile" >&2; exit 1; }

prod=""
for f in "$repo_dir"/*.go; do
  base="$(basename "$f")"
  case "$base" in *_test.go) continue ;; esac
  prod="$prod $base"
done
prod="${prod# }"
[[ -n "$prod" ]] || { echo "no production Go files" >&2; exit 1; }

if grep -qE '^COPY[[:space:]]+\*\.go' "$df"; then
  printf 'Dockerfile COPY *.go covers:%s\n' "$prod"
else
  missing=""
  for f in $prod; do
    grep -qF "$f" "$df" || missing="$missing $f"
  done
  if [[ -n "$missing" ]]; then
    printf 'Dockerfile does not copy production sources:%s\n' "$missing" >&2
    exit 1
  fi
fi

required=(
  main.go
  teslamate_version.go
  notification.go
  charging_notification.go
  navigation_notification.go
  parking_event_monitor.go
  storage_policy.go
  lock_secure_notification.go
  push_subscribers.go
)
for f in "${required[@]}"; do
  [[ -f "$repo_dir/$f" ]] || { echo "missing required $f" >&2; exit 1; }
  grep -qE 'test -f '"$f" "$df" || true
done
for f in lock_secure_notification.go push_subscribers.go teslamate_version.go; do
  grep -qF "test -f $f" "$df" || {
    echo "Dockerfile must test -f $f before go build" >&2
    exit 1
  }
done

count=0
for _ in $prod; do
  count=$((count + 1))
done
echo "docker source check passed ($count production files)"
