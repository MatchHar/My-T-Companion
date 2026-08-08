#!/usr/bin/env bash
# My T Companion / Server quick diagnostics (read-only).
# Usage: sudo bash scripts/myt-doctor.sh
#        sudo INSTALL_DIR=/opt/my-t-companion TESLAMATE_DIR=/opt/teslamate bash scripts/myt-doctor.sh
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/my-t-companion}"
TESLAMATE_DIR="${TESLAMATE_DIR:-/opt/teslamate}"
COMPOSE_PROJECT="${COMPOSE_PROJECT:-my-t-companion}"

ok=0
warn=0
fail=0

pass() { printf '  [OK]  %s\n' "$*"; ok=$((ok + 1)); }
note() { printf '  [..]  %s\n' "$*"; warn=$((warn + 1)); }
bad()  { printf '  [!!]  %s\n' "$*"; fail=$((fail + 1)); }

printf '=== My T doctor ===\n'
printf 'INSTALL_DIR=%s  TESLAMATE_DIR=%s\n\n' "$INSTALL_DIR" "$TESLAMATE_DIR"

printf '1) Companion process\n'
if curl -fsS -m 3 http://127.0.0.1:8083/api/healthz >/dev/null 2>&1; then
  pass "healthz on 127.0.0.1:8083"
else
  bad "healthz failed — is companion up? (cd $INSTALL_DIR && docker compose ps)"
fi

token=""
if [[ -f "$INSTALL_DIR/.env" ]]; then
  token="$(grep -E '^MY_T_API_TOKEN=' "$INSTALL_DIR/.env" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"' || true)"
fi
if [[ -z "$token" && -f "$TESLAMATE_DIR/.env" ]]; then
  token="$(grep -E '^API_TOKEN=|^MY_T_API_TOKEN=' "$TESLAMATE_DIR/.env" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"' || true)"
fi

printf '\n2) Capabilities (same auth as My T)\n'
if [[ -n "$token" ]]; then
  body="$(curl -fsS -m 8 -H "Authorization: Bearer ${token}" http://127.0.0.1:8083/api/v1/capabilities 2>/dev/null || true)"
  if echo "$body" | grep -q 'my-t-companion'; then
    ver="$(echo "$body" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    pass "capabilities OK version=${ver:-?}"
    if echo "$body" | grep -q 'teslamate_version'; then
      tm="$(echo "$body" | sed -n 's/.*"teslamate_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
      pass "teslamate_version=${tm}"
    else
      note "no teslamate_version in capabilities (optional; HostBox/install may set TESLAMATE_VERSION)"
    fi
  else
    bad "capabilities not companion JSON (auth or wrong process on :8083)"
  fi
else
  note "no API token found in $INSTALL_DIR/.env or $TESLAMATE_DIR/.env — skip capabilities auth check"
fi

printf '\n3) Compose / MQTT wiring\n'
if [[ -f "$INSTALL_DIR/docker-compose.yml" ]]; then
  if grep -q 'host.docker.internal:host-gateway' "$INSTALL_DIR/docker-compose.yml"; then
    pass "extra_hosts host.docker.internal present"
  else
    note "no host.docker.internal extra_hosts (OK if MQTT is a compose service on same network)"
  fi
  if grep -qE 'MQTT_BROKER_URL:.*host.docker.internal|MQTT_BROKER_URL=.*host.docker.internal' "$INSTALL_DIR/docker-compose.yml" "$INSTALL_DIR/.env" 2>/dev/null; then
    pass "MQTT points at host.docker.internal (typical HostBox / system mosquitto)"
  elif grep -qE 'MQTT_BROKER_URL:.*mosquitto|MQTT_BROKER_URL=.*mosquitto' "$INSTALL_DIR/docker-compose.yml" "$INSTALL_DIR/.env" 2>/dev/null; then
    pass "MQTT points at docker service mosquitto"
  else
    note "could not classify MQTT_BROKER_URL"
  fi
else
  bad "missing $INSTALL_DIR/docker-compose.yml"
fi

cid="$(docker ps -qf name=companion 2>/dev/null | head -1 || true)"
if [[ -n "$cid" ]]; then
  if docker exec "$cid" cat /etc/hosts 2>/dev/null | grep -q host.docker.internal; then
    pass "running container sees host.docker.internal"
  else
    note "container has no host.docker.internal in /etc/hosts (OK if using docker DNS name mosquitto)"
  fi
  if docker logs "$cid" 2>&1 | tail -100 | grep -qiE 'parking-event|subscribed to TeslaMate'; then
    pass "logs mention MQTT / parking-event subscribe"
  else
    note "no recent parking-event MQTT lines in logs (open a door after install to generate events)"
  fi
else
  bad "no running companion container"
fi

printf '\n4) Unified entry (edge / public API port)\n'
if [[ -d "$INSTALL_DIR/edge" ]]; then
  pass "edge directory present ($INSTALL_DIR/edge)"
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -qiE 'edge|my-t-api-edge'; then
    pass "edge-related container running"
  else
    note "edge dir exists but no edge container name matched"
  fi
else
  note "no local edge dir (OK for Tunnel/domain path-split without temporary-IP edge)"
fi

api_port=8081
if [[ -f "$TESLAMATE_DIR/docker-compose.yml" ]]; then
  if grep -qE '8081:8080' "$TESLAMATE_DIR/docker-compose.yml"; then api_port=8081
  elif grep -qE '[" ]8080:8080' "$TESLAMATE_DIR/docker-compose.yml"; then api_port=8080
  fi
fi
if [[ -n "$token" ]]; then
  edge_body="$(curl -fsS -m 5 -H "Authorization: Bearer ${token}" "http://127.0.0.1:${api_port}/api/v1/capabilities" 2>/dev/null || true)"
  if echo "$edge_body" | grep -q 'my-t-companion'; then
    pass "unified entry :${api_port} serves companion capabilities (My T base_url can use this port)"
  else
    note ":${api_port}/capabilities is not companion (bare API or Tunnel mode) — App needs gateway/edge/Tunnel path split"
  fi
fi

printf '\n5) TeslaMate dir\n'
if [[ -d "$TESLAMATE_DIR" ]]; then
  pass "TESLAMATE_DIR exists"
  if [[ -f "$TESLAMATE_DIR/.env" ]]; then
    pass ".env present"
  else
    note "no .env (OK if secrets only in compose — installer supports that)"
  fi
else
  bad "TESLAMATE_DIR missing: $TESLAMATE_DIR"
fi

printf '\n=== summary: ok=%s warn=%s fail=%s ===\n' "$ok" "$warn" "$fail"
if [[ "$fail" -gt 0 ]]; then
  printf 'Fix [!!] items, then: sudo %s/install.sh  or HostBox → 安装/修复增强\n' "$INSTALL_DIR"
  exit 1
fi
printf 'No hard failures. Warnings are informational.\n'
exit 0
