#!/usr/bin/env bash
set -euo pipefail

TESLAMATE_DIR="${TESLAMATE_DIR:-/opt/teslamate}"
INSTALL_DIR="${INSTALL_DIR:-/opt/my-t-parking-monitor}"
COMPOSE_PROJECT="${COMPOSE_PROJECT:-my-t-parking-monitor}"
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION_FILE="$SOURCE_DIR/VERSION"
[[ -f "$VERSION_FILE" ]] || {
  printf '[My T Parking Monitor] ERROR: VERSION file is missing.\n' >&2
  exit 1
}
VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
ENV_FILE="$INSTALL_DIR/.env"
COMPOSE_FILE="$INSTALL_DIR/docker-compose.yml"
CADDY_FILE="${CADDY_FILE:-/etc/caddy/Caddyfile}"
MY_T_BASE_URL="${MY_T_BASE_URL:-}"
MY_T_AUTH_HEADER="${MY_T_AUTH_HEADER:-}"
MY_T_AUTH_PROBE_URL="${MY_T_AUTH_PROBE_URL:-}"
PUSH_INSTALLATION_ID="${PUSH_INSTALLATION_ID:-}"
PUSH_RELAY_URL="${PUSH_RELAY_URL:-}"
PUSH_RELAY_SECRET="${PUSH_RELAY_SECRET:-}"

log() {
  printf '[My T Parking Monitor] %s\n' "$*"
}

fail() {
  printf '[My T Parking Monitor] ERROR: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "Missing required command: $1"
}

read_env_value() {
  local key="$1"
  local file="$2"
  local line
  line="$(grep -E "^${key}=" "$file" 2>/dev/null | tail -n 1 || true)"
  [[ -n "$line" ]] || return 1
  printf '%s' "${line#*=}" | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//"
}

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  fail "Run with sudo or as root."
fi

require_command docker
require_command openssl
require_command sed
require_command awk
require_command curl

[[ -d "$TESLAMATE_DIR" ]] || fail "TeslaMate directory not found: $TESLAMATE_DIR"
[[ -f "$TESLAMATE_DIR/docker-compose.yml" ]] || fail "TeslaMate docker-compose.yml not found."
[[ -f "$TESLAMATE_DIR/.env" ]] || fail "TeslaMate .env not found."
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
  || fail "Invalid or missing VERSION file."
[[ -f "$SOURCE_DIR/Dockerfile" && -f "$SOURCE_DIR/main.go" &&
   -f "$SOURCE_DIR/notification.go" && -f "$SOURCE_DIR/charging_notification.go" && -f "$SOURCE_DIR/navigation_notification.go" ]] \
  || fail "Run install.sh from a complete My-T-Parking-Monitor checkout."

log "Checking the existing TeslaMate deployment"
database_container="$(
  cd "$TESLAMATE_DIR"
  docker compose ps -q database 2>/dev/null || true
)"
[[ -n "$database_container" ]] || fail "TeslaMate database service is not running."

database_network="$(
  docker inspect "$database_container" \
    --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{"\n"}}{{end}}' \
    | head -n 1
)"
[[ -n "$database_network" ]] || fail "Unable to detect the TeslaMate Docker network."

database_pass="$(
  read_env_value DATABASE_PASS "$TESLAMATE_DIR/.env" \
    || read_env_value TM_DB_PASS "$TESLAMATE_DIR/.env" \
    || true
)"
[[ -n "$database_pass" ]] || fail "DATABASE_PASS was not found in TeslaMate .env."

api_token="$(
  read_env_value MY_T_API_TOKEN "$TESLAMATE_DIR/.env" \
    || read_env_value TM_API_TOKEN "$TESLAMATE_DIR/.env" \
    || read_env_value API_TOKEN "$TESLAMATE_DIR/.env" \
    || read_env_value MY_T_API_TOKEN "$ENV_FILE" \
    || true
)"
generated_token=false
if [[ -z "$api_token" ]]; then
  api_token="$(openssl rand -hex 32)"
  generated_token=true
fi

timezone="$(
  read_env_value TZ "$TESLAMATE_DIR/.env" \
    || printf 'UTC'
)"

push_installation_id="${PUSH_INSTALLATION_ID:-$(read_env_value PUSH_INSTALLATION_ID "$ENV_FILE" || true)}"
push_relay_url="${PUSH_RELAY_URL:-$(read_env_value PUSH_RELAY_URL "$ENV_FILE" || true)}"
push_relay_secret="${PUSH_RELAY_SECRET:-$(read_env_value PUSH_RELAY_SECRET "$ENV_FILE" || true)}"
if [[ -n "$push_relay_url" && ! "$push_relay_url" =~ ^https:// ]]; then
  fail "PUSH_RELAY_URL must use HTTPS."
fi
push_values=0
[[ -n "$push_installation_id" ]] && push_values=$((push_values + 1))
[[ -n "$push_relay_url" ]] && push_values=$((push_values + 1))
[[ -n "$push_relay_secret" ]] && push_values=$((push_values + 1))
if [[ "$push_values" -ne 0 && "$push_values" -ne 3 ]]; then
  fail "Software push requires PUSH_INSTALLATION_ID, PUSH_RELAY_URL, and PUSH_RELAY_SECRET together."
fi

auth_probe_url="$(
  if [[ -n "$MY_T_AUTH_PROBE_URL" ]]; then
    printf '%s' "$MY_T_AUTH_PROBE_URL"
  else
    read_env_value AUTH_PROBE_URL "$ENV_FILE" || true
  fi
)"
if [[ -f "$CADDY_FILE" ]]; then
  api_hostname="$(
    awk '/^[[:space:]]*@api_host[[:space:]]+host[[:space:]]+/ {gsub(/"/, "", $3); print $3; exit}' "$CADDY_FILE"
  )"
  if [[ -z "$auth_probe_url" && -n "$api_hostname" ]]; then
    auth_probe_url="https://${api_hostname}/api/ping"
  fi
fi

if [[ "$generated_token" == true && -z "$auth_probe_url" ]]; then
  fail "No reusable TeslaMate API authentication was detected. Set MY_T_API_TOKEN in the TeslaMate .env or provide MY_T_AUTH_PROBE_URL for the existing protected /api/ping endpoint."
fi

if [[ -n "$auth_probe_url" ]]; then
  unauthenticated_status="$(
    curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
      "$auth_probe_url" || true
  )"
  if [[ "$unauthenticated_status" =~ ^2 ]]; then
    fail "The existing API authentication probe is publicly accessible. Protect /api/ping before installing the companion."
  fi
fi

log "Installing service files in $INSTALL_DIR"
install -d -m 0755 "$INSTALL_DIR"
if [[ "$SOURCE_DIR" != "$INSTALL_DIR" ]]; then
  install -m 0644 "$SOURCE_DIR/Dockerfile" "$INSTALL_DIR/Dockerfile"
  install -m 0644 "$SOURCE_DIR/go.mod" "$INSTALL_DIR/go.mod"
  if [[ -f "$SOURCE_DIR/go.sum" ]]; then
    install -m 0644 "$SOURCE_DIR/go.sum" "$INSTALL_DIR/go.sum"
  fi
  install -m 0644 "$SOURCE_DIR/main.go" "$INSTALL_DIR/main.go"
  install -m 0644 "$SOURCE_DIR/notification.go" "$INSTALL_DIR/notification.go"
  install -m 0644 "$SOURCE_DIR/charging_notification.go" "$INSTALL_DIR/charging_notification.go"
  install -m 0644 "$SOURCE_DIR/navigation_notification.go" "$INSTALL_DIR/navigation_notification.go"
  install -m 0644 "$SOURCE_DIR/VERSION" "$INSTALL_DIR/VERSION"
  install -m 0755 "$SOURCE_DIR/install.sh" "$INSTALL_DIR/install.sh"
  install -m 0755 "$SOURCE_DIR/update.sh" "$INSTALL_DIR/update.sh"
  if [[ -f "$SOURCE_DIR/uninstall.sh" ]]; then
    install -m 0755 "$SOURCE_DIR/uninstall.sh" "$INSTALL_DIR/uninstall.sh"
  fi
else
  log "Running from the installed directory; service source files are already current"
fi

umask 077
{
  printf 'DATABASE_PASS=%s\n' "$database_pass"
  printf 'MY_T_API_TOKEN=%s\n' "$api_token"
  printf 'AUTH_PROBE_URL=%s\n' "$auth_probe_url"
  printf 'TZ=%s\n' "$timezone"
  printf 'TESLAMATE_NETWORK=%s\n' "$database_network"
  printf 'PUSH_INSTALLATION_ID=%s\n' "$push_installation_id"
  printf 'PUSH_RELAY_URL=%s\n' "$push_relay_url"
  printf 'PUSH_RELAY_SECRET=%s\n' "$push_relay_secret"
} > "$ENV_FILE"
chmod 0600 "$ENV_FILE"

cat > "$COMPOSE_FILE" <<'YAML'
services:
  parking-monitor:
    build: .
    image: myt/parking-monitor:${VERSION}
    restart: unless-stopped
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /tmp:size=16m,mode=1777
    environment:
      DATABASE_USER: teslamate
      DATABASE_PASS: ${DATABASE_PASS}
      DATABASE_NAME: teslamate
      DATABASE_HOST: database
      PGOPTIONS: -c default_transaction_read_only=on
      API_TOKEN: ${MY_T_API_TOKEN}
      AUTH_PROBE_URL: ${AUTH_PROBE_URL:-}
      TZ: ${TZ:-UTC}
      MQTT_BROKER_URL: tcp://mosquitto:1883
      PUSH_INSTALLATION_ID: ${PUSH_INSTALLATION_ID:-}
      PUSH_RELAY_URL: ${PUSH_RELAY_URL:-}
      PUSH_RELAY_SECRET: ${PUSH_RELAY_SECRET:-}
      PUSH_STATE_PATH: /data/software-notifications.json
    ports:
      - "127.0.0.1:8083:8080"
    volumes:
      - notification-state:/data
    healthcheck:
      test: ["CMD", "/app/states-api", "-healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    networks:
      - teslamate

volumes:
  notification-state:

networks:
  teslamate:
    external: true
    name: ${TESLAMATE_NETWORK}
YAML
sed -i.bak "s/\${VERSION}/$VERSION/g" "$COMPOSE_FILE"
rm -f "$COMPOSE_FILE.bak"

log "Building and starting the read-only monitor"
docker compose \
  --project-name "$COMPOSE_PROJECT" \
  --env-file "$ENV_FILE" \
  --file "$COMPOSE_FILE" \
  up -d --build

for _ in $(seq 1 20); do
  if curl --fail --silent --show-error http://127.0.0.1:8083/api/healthz >/dev/null; then
    break
  fi
  sleep 1
done
curl --fail --silent --show-error http://127.0.0.1:8083/api/healthz >/dev/null \
  || fail "The monitor did not become healthy."

proxy_ready=false
if [[ -f "$CADDY_FILE" ]] && command -v caddy >/dev/null 2>&1; then
  missing_states=true
  missing_capabilities=true
  missing_current_drive=true
  missing_notification_status=true
  missing_notification_pair=true
  grep -qE 'cars/.*/states|parking_states|my_t_parking_states' "$CADDY_FILE" && missing_states=false
  grep -qE 'api/v1/capabilities|parking_capabilities|my_t_parking_capabilities' "$CADDY_FILE" && missing_capabilities=false
  grep -qE 'current-drive|my_t_current_drive' "$CADDY_FILE" && missing_current_drive=false
  grep -qE 'notifications/software-update/status|my_t_software_push_status' "$CADDY_FILE" && missing_notification_status=false
  grep -qE 'notifications/software-update/pair|my_t_software_push_pair' "$CADDY_FILE" && missing_notification_pair=false

  if [[ "$missing_states" == true || "$missing_capabilities" == true || "$missing_current_drive" == true || "$missing_notification_status" == true || "$missing_notification_pair" == true ]]; then
    route_anchor="$(grep -nE '^[[:space:]]*handle[[:space:]]+@(teslamate_api|api)' "$CADDY_FILE" | head -n 1 | cut -d: -f1 || true)"
    if [[ -z "$route_anchor" ]]; then
      fail "Service is healthy, but the Caddy API route location could not be detected. Add the routes from Caddyfile.snippet manually."
    fi

    backup="$CADDY_FILE.before-my-t-parking-monitor.$(date +%Y%m%d-%H%M%S)"
    cp "$CADDY_FILE" "$backup"
    route_file="$(mktemp)"
    printf '\t# BEGIN MY T VPS COMPANION\n' > "$route_file"
    if [[ "$missing_states" == true ]]; then
      cat >> "$route_file" <<'CADDY'
	@my_t_parking_states path_regexp my_t_parking_states ^/api/v1/cars/[0-9]+/states$
	handle @my_t_parking_states {
		reverse_proxy 127.0.0.1:8083
	}

CADDY
    fi
    if [[ "$missing_current_drive" == true ]]; then
      cat >> "$route_file" <<'CADDY'
	@my_t_current_drive path_regexp my_t_current_drive ^/api/v1/cars/[0-9]+/navigation/current-drive$
	handle @my_t_current_drive {
		reverse_proxy 127.0.0.1:8083
	}

CADDY
    fi
    if [[ "$missing_capabilities" == true ]]; then
      cat >> "$route_file" <<'CADDY'
	@my_t_parking_capabilities path /api/v1/capabilities
	handle @my_t_parking_capabilities {
		reverse_proxy 127.0.0.1:8083
	}

CADDY
    fi
    if [[ "$missing_notification_status" == true ]]; then
      cat >> "$route_file" <<'CADDY'
	@my_t_software_push_status path /api/v1/notifications/software-update/status
	handle @my_t_software_push_status {
		reverse_proxy 127.0.0.1:8083
	}

CADDY
    fi
    if [[ "$missing_notification_pair" == true ]]; then
      cat >> "$route_file" <<'CADDY'
	@my_t_software_push_pair path /api/v1/notifications/software-update/pair
	handle @my_t_software_push_pair {
		reverse_proxy 127.0.0.1:8083
	}

CADDY
    fi
    printf '\t# END MY T VPS COMPANION\n\n' >> "$route_file"
    awk -v line="$route_anchor" -v insert="$route_file" '
      NR == line {
        while ((getline value < insert) > 0) print value
        close(insert)
      }
      { print }
    ' "$CADDY_FILE" > "$CADDY_FILE.new"
    mv "$CADDY_FILE.new" "$CADDY_FILE"
    chmod 0644 "$CADDY_FILE"
    rm -f "$route_file"

    if ! caddy validate --config "$CADDY_FILE"; then
      cp "$backup" "$CADDY_FILE"
      chmod 0644 "$CADDY_FILE"
      fail "Caddy validation failed; the original configuration was restored."
    fi
    if ! systemctl reload caddy; then
      cp "$backup" "$CADDY_FILE"
      chmod 0644 "$CADDY_FILE"
      systemctl reload caddy || true
      fail "Caddy reload failed; the original configuration was restored."
    fi
    log "Missing VPS Companion Caddy routes installed; backup: $backup"
  else
    log "All VPS Companion Caddy routes are already installed"
  fi
  proxy_ready=true
else
  log "No supported system Caddy installation detected"
fi

capabilities="$(
  curl --fail --silent --show-error \
    -H "Authorization: Bearer $api_token" \
    http://127.0.0.1:8083/api/v1/capabilities
)"
printf '%s' "$capabilities" | grep -q '"parking_state_history"' \
  || fail "Capability verification failed."
printf '%s' "$capabilities" | grep -q '"current_drive_trajectory"' \
  || fail "Current-drive capability verification failed."
printf '%s' "$capabilities" | grep -q '"vehicle_software_update_events"' \
  || fail "Software-update capability verification failed."

if [[ "$proxy_ready" != true ]]; then
  if [[ -z "$MY_T_BASE_URL" ]]; then
    printf '\n'
    log "Service installed locally, but setup is NOT complete."
    log "Add the Caddy, Nginx, or Traefik routes, then rerun with:"
    printf '  sudo MY_T_BASE_URL="https://your-api.example"'
    printf ' MY_T_AUTH_HEADER="Authorization: Bearer REDACTED" "%s/install.sh"\n\n' "$INSTALL_DIR"
    fail "My T cannot reach the Companion until the unified proxy is verified."
  fi

  public_capabilities_url="${MY_T_BASE_URL%/}/api/v1/capabilities"
  curl_args=(--fail --silent --show-error --output /dev/null)
  if [[ -n "$MY_T_AUTH_HEADER" ]]; then
    curl_args+=(-H "$MY_T_AUTH_HEADER")
  fi
  if ! curl "${curl_args[@]}" "$public_capabilities_url"; then
    fail "The unified My T endpoint could not be verified: $public_capabilities_url"
  fi
  proxy_ready=true
  log "Unified My T endpoint verified"
fi

log "Installation complete (version $VERSION)"
if [[ "$generated_token" == true ]]; then
  printf '\nUse this bearer token in the My T TeslaMate API connection:\n%s\n\n' "$api_token"
else
  log "The existing TeslaMate API authentication remains unchanged"
fi
