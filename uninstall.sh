#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/my-t-parking-monitor}"
COMPOSE_PROJECT="${COMPOSE_PROJECT:-my-t-parking-monitor}"
CADDY_FILE="${CADDY_FILE:-/etc/caddy/Caddyfile}"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  printf '[My T VPS Companion] ERROR: Run with sudo or as root.\n' >&2
  exit 1
fi

if [[ -f "$INSTALL_DIR/docker-compose.yml" && -f "$INSTALL_DIR/.env" ]]; then
  docker compose \
    --project-name "$COMPOSE_PROJECT" \
    --env-file "$INSTALL_DIR/.env" \
    --file "$INSTALL_DIR/docker-compose.yml" \
    down
fi

if [[ -f "$CADDY_FILE" ]] && grep -q '# BEGIN MY T VPS COMPANION' "$CADDY_FILE"; then
  backup="$CADDY_FILE.before-my-t-vps-companion-uninstall.$(date +%Y%m%d-%H%M%S)"
  cp "$CADDY_FILE" "$backup"
  awk '
    /# BEGIN MY T VPS COMPANION/ { skipping = 1; next }
    /# END MY T VPS COMPANION/ { skipping = 0; next }
    !skipping { print }
  ' "$CADDY_FILE" > "$CADDY_FILE.new"
  mv "$CADDY_FILE.new" "$CADDY_FILE"
  chmod 0644 "$CADDY_FILE"
  if command -v caddy >/dev/null 2>&1; then
    if ! caddy validate --config "$CADDY_FILE"; then
      cp "$backup" "$CADDY_FILE"
      chmod 0644 "$CADDY_FILE"
      printf '[My T VPS Companion] ERROR: Caddy validation failed; configuration restored.\n' >&2
      exit 1
    fi
    systemctl reload caddy
  fi
  printf '[My T VPS Companion] Proxy routes removed; backup: %s\n' "$backup"
fi

printf '[My T VPS Companion] Service removed. Configuration remains in %s for recovery.\n' "$INSTALL_DIR"
