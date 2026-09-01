#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

failed=0

require_literal() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq -- "$expected" "$file"; then
    printf 'missing required network boundary in %s: %s\n' "$file" "$expected" >&2
    failed=1
  fi
}

forbid_regex() {
  local file="$1"
  local forbidden="$2"
  if grep -nE -- "$forbidden" "$file"; then
    printf 'forbidden network boundary pattern in %s: %s\n' "$file" "$forbidden" >&2
    failed=1
  fi
}

# The local diagnostic port must never be published on every host interface.
require_literal install.sh '"127.0.0.1:8083:8080"'
require_literal docker-compose.snippet.yml '"127.0.0.1:8083:8080"'
forbid_regex install.sh "^[[:space:]-]+[\"']?8083:8080[\"']?[[:space:]]*$"
forbid_regex docker-compose.snippet.yml "^[[:space:]-]+[\"']?8083:8080[\"']?[[:space:]]*$"
forbid_regex install.sh 'sed .*8083:8080.*8083:8080'

# A containerized Caddy reaches Companion over their verified shared Docker
# network. It must never require widening the host-side port binding.
require_literal install.sh 'reverse_proxy companion:8080'
require_literal install.sh '@my_t_companion_status path_regexp my_t_companion_status ^/api/v1/cars/[0-9]+/companion-status$'
forbid_regex install.sh '^[[:space:]]+reverse_proxy host\.docker\.internal:8083[[:space:]]*$'
require_literal install.sh 'does not share $database_network; refusing to expose port 8083'
shared_route_count="$(grep -Fc 'reverse_proxy companion:8080' install.sh || true)"
if [[ "$shared_route_count" -lt 5 ]]; then
  printf 'all five Companion route groups must use the shared Docker network (found %s)\n' \
    "$shared_route_count" >&2
  failed=1
fi

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi

printf 'network boundary check passed\n'
