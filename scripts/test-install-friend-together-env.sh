#!/usr/bin/env bash
# Regression: install.sh must preserve Friend Together keys when rewriting .env.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_sh="$repo_dir/install.sh"

grep -q 'normalize_friend_together_enabled' "$install_sh"
grep -q "printf 'FRIEND_TOGETHER_ENABLED=%s\\\\n'" "$install_sh"
grep -q "printf 'FRIEND_TOGETHER_GUEST_ORIGIN=%s\\\\n'" "$install_sh"
grep -q "printf 'FRIEND_TOGETHER_STATE_PATH=%s\\\\n'" "$install_sh"
grep -q 'FRIEND_TOGETHER_ENABLED:-$(read_env_value FRIEND_TOGETHER_ENABLED' "$install_sh"

# Exercise the same normalize + rewrite rules install.sh uses.
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
ENV_FILE="$test_dir/.env"
cat > "$ENV_FILE" <<'ENV'
DATABASE_PASS=secret
PUSH_INSTALLATION_ID=
PUSH_RELAY_URL=
PUSH_RELAY_SECRET=
FRIEND_TOGETHER_ENABLED=TRUE
FRIEND_TOGETHER_GUEST_ORIGIN=https://friend.example.my-t.org
FRIEND_TOGETHER_STATE_PATH=/data/friend-together/state.json
ENV

read_env_value() {
  local key="$1" file="$2" line
  line="$(grep -E "^${key}=" "$file" 2>/dev/null | tail -n 1 || true)"
  [[ -n "$line" ]] || return 1
  printf '%s' "${line#*=}"
}

normalize_friend_together_enabled() {
  local raw
  raw="$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"
  case "$raw" in
    true|1|yes|on) printf 'true' ;;
    *) printf 'false' ;;
  esac
}

friend_together_enabled_raw="$(read_env_value FRIEND_TOGETHER_ENABLED "$ENV_FILE" || true)"
friend_together_guest_origin="$(read_env_value FRIEND_TOGETHER_GUEST_ORIGIN "$ENV_FILE" || true)"
friend_together_state_path="$(read_env_value FRIEND_TOGETHER_STATE_PATH "$ENV_FILE" || true)"
friend_together_enabled="$(normalize_friend_together_enabled "$friend_together_enabled_raw")"
[[ -n "$friend_together_state_path" ]] || friend_together_state_path="/data/friend-together/state.json"

umask 077
{
  printf 'DATABASE_PASS=%s\n' "$(read_env_value DATABASE_PASS "$ENV_FILE")"
  printf 'PUSH_INSTALLATION_ID=\n'
  printf 'PUSH_RELAY_URL=\n'
  printf 'PUSH_RELAY_SECRET=\n'
  printf 'FRIEND_TOGETHER_ENABLED=%s\n' "$friend_together_enabled"
  printf 'FRIEND_TOGETHER_GUEST_ORIGIN=%s\n' "$friend_together_guest_origin"
  printf 'FRIEND_TOGETHER_STATE_PATH=%s\n' "$friend_together_state_path"
} > "$ENV_FILE"

grep -qx 'FRIEND_TOGETHER_ENABLED=true' "$ENV_FILE"
grep -qx 'FRIEND_TOGETHER_GUEST_ORIGIN=https://friend.example.my-t.org' "$ENV_FILE"
grep -qx 'FRIEND_TOGETHER_STATE_PATH=/data/friend-together/state.json' "$ENV_FILE"

# Empty prior values must stay off (default closed).
cat > "$ENV_FILE" <<'ENV'
DATABASE_PASS=secret
ENV
friend_together_enabled_raw="$(read_env_value FRIEND_TOGETHER_ENABLED "$ENV_FILE" || true)"
friend_together_guest_origin="$(read_env_value FRIEND_TOGETHER_GUEST_ORIGIN "$ENV_FILE" || true)"
friend_together_state_path="$(read_env_value FRIEND_TOGETHER_STATE_PATH "$ENV_FILE" || true)"
friend_together_enabled="$(normalize_friend_together_enabled "$friend_together_enabled_raw")"
[[ -n "$friend_together_state_path" ]] || friend_together_state_path="/data/friend-together/state.json"
{
  printf 'DATABASE_PASS=secret\n'
  printf 'FRIEND_TOGETHER_ENABLED=%s\n' "$friend_together_enabled"
  printf 'FRIEND_TOGETHER_GUEST_ORIGIN=%s\n' "$friend_together_guest_origin"
  printf 'FRIEND_TOGETHER_STATE_PATH=%s\n' "$friend_together_state_path"
} > "$ENV_FILE"
grep -qx 'FRIEND_TOGETHER_ENABLED=false' "$ENV_FILE"
grep -qx 'FRIEND_TOGETHER_GUEST_ORIGIN=' "$ENV_FILE"

printf 'install Friend Together env preserve tests passed\n'
