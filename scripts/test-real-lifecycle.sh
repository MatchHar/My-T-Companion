#!/usr/bin/env bash
# Real released installers; synthetic data; NEVER run on an operator/production host.
set -euo pipefail

fail() { printf 'LIFECYCLE FAIL: %s\n' "$*" >&2; exit 1; }
[[ "${GITHUB_ACTIONS:-}" == true && "${MYT_RUNNER_ENVIRONMENT:-}" == github-hosted &&
   "${RUNNER_OS:-}" == Linux && "${MYT_DISPOSABLE_LIFECYCLE:-}" == 1 && "$EUID" == 0 ]] \
  || fail 'Requires explicit opt-in on a disposable GitHub-hosted Linux runner as root.'
for key in INSTALL_DIR TESLAMATE_DIR COMPOSE_PROJECT CADDY_FILE DATABASE_PASS DATABASE_USER \
  MY_T_API_TOKEN MY_T_UPDATE_SOURCE_DIR MY_T_RELEASE_BASE_URL PUSH_RELAY_SECRET DOCKER_HOST DOCKER_CONTEXT; do
  [[ -z "${!key:-}" ]] || fail "Refusing inherited operator configuration: $key"
done
for tool in docker curl jq gh sha256sum tar openssl ss ip iptables; do
  command -v "$tool" >/dev/null || fail "Missing $tool"
done
[[ "$(docker context inspect --format '{{.Endpoints.docker.Host}}')" == unix:///var/run/docker.sock ]] \
  || fail 'Docker must use this disposable runner, never a remote daemon.'
[[ -z "$(docker ps -aq)" ]] || fail 'Runner Docker daemon is not empty.'
bridge=myt-fixture0
! ip link show "$bridge" >/dev/null 2>&1 || fail 'Fixture bridge name already exists.'
firewall_ready=0
if ss -H -lnt '( sport = :80 or sport = :8083 )' | grep -q .; then
  fail 'Fixture ports are already occupied.'
fi

previous=1.10.50
target=1.10.51
repository=MatchHar/My-T-Companion
previous_digest=1bf67eb31683242c87fb428a96d148e1664072a45643f8fe7c99f61bfc391bf0
target_digest=ed4783af791bb17d188fae3978d50be02eb33d9b620d2c0e9d2d5c2e944d1e3c
work_dir="$(mktemp -d /tmp/myt-lifecycle.XXXXXXXX)"
log_dir="${RUNNER_TEMP:?}/myt-lifecycle-logs"
mkdir -p "$log_dir"
chmod 0755 "$log_dir"
prefix="myt-life-${work_dir##*.}"
prefix="${prefix,,}"
stack_project="${prefix}-stack"
clean_project="${prefix}-clean"
baseline_project="${prefix}-baseline"
real_docker="$(command -v docker)"
export TESLAMATE_DIR="$work_dir/teslamate"
export CADDY_FILE="$work_dir/no-system-caddy"
export INSTALL_DIR="$work_dir/clean-install" COMPOSE_PROJECT="$clean_project"
export DATABASE_USER=fixture_reader DATABASE_PASS=synthetic-lifecycle-reader-only
export MY_T_API_TOKEN=synthetic-lifecycle-owner-token-not-a-secret
export TESLAMATE_VERSION=4.2.0 TESLAMATE_WEB_URL=http://teslamateapi:8080
export FRIEND_TOGETHER_ENABLED=true FRIEND_TOGETHER_GUEST_ORIGIN=https://friend.example.com
export FRIEND_TOGETHER_STATE_PATH=/data/friend-together/state.json

stack() { "$real_docker" compose --project-name "$stack_project" --file "$TESLAMATE_DIR/docker-compose.yml" "$@"; }
companion() { "$real_docker" compose --project-name "$COMPOSE_PROJECT" --env-file "$INSTALL_DIR/.env" --file "$INSTALL_DIR/docker-compose.yml" "$@"; }
gate() { printf 'PASS: %s\n' "$*" | tee -a "$log_dir/gates.txt"; }
cleanup() {
  local status=$?
  trap - EXIT
  set +e
  if [[ -f "$INSTALL_DIR/docker-compose.yml" && -f "$INSTALL_DIR/.env" ]]; then
    companion logs --no-color --tail 150 > "$log_dir/companion-final.log" 2>&1
    companion down --volumes --remove-orphans > "$log_dir/companion-cleanup.log" 2>&1
  fi
  if [[ -f "$work_dir/clean-install/docker-compose.yml" ]]; then
    "$real_docker" compose --project-name "$clean_project" --env-file "$work_dir/clean-install/.env" \
      --file "$work_dir/clean-install/docker-compose.yml" down --volumes --remove-orphans >> "$log_dir/companion-cleanup.log" 2>&1
  fi
  if [[ -f "$TESLAMATE_DIR/docker-compose.yml" ]]; then
    stack logs --no-color --tail 100 > "$log_dir/stack-final.log" 2>&1
    stack down --volumes --remove-orphans > "$log_dir/stack-cleanup.log" 2>&1
  fi
  if [[ "$firewall_ready" == 1 ]]; then
    iptables -D DOCKER-USER -i "$bridge" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
    iptables -D DOCKER-USER -i "$bridge" ! -o "$bridge" -j DROP
    iptables -D INPUT -i "$bridge" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    iptables -D INPUT -i "$bridge" -j DROP
  fi
  printf 'exit_status=%s\n' "$status" >> "$log_dir/gates.txt"
  chmod -R a+rX "$log_dir"
  # All remaining scratch files belong to this ephemeral runner. No broad deletion.
  exit "$status"
}
trap cleanup EXIT

fetch_release() {
  local version="$1" archive="my-t-companion-$1.tar.gz" digest
  mkdir -p "$work_dir/releases/$version" "$work_dir/source/$version"
  gh api "repos/$repository/releases/tags/v$version" > "$work_dir/releases/$version/metadata.json"
  jq -e --arg tag "v$version" '.immutable == true and .draft == false and .prerelease == false and .tag_name == $tag' \
    "$work_dir/releases/$version/metadata.json" >/dev/null
  digest="$(jq -er --arg name "$archive" '.assets[] | select(.name == $name) | .digest | select(startswith("sha256:")) | ltrimstr("sha256:")' "$work_dir/releases/$version/metadata.json")"
  [[ "$digest" =~ ^[a-f0-9]{64}$ ]] || fail 'Missing published archive digest.'
  if [[ "$version" == "$previous" ]]; then
    [[ "$digest" == "$previous_digest" ]] || fail 'Previous immutable archive changed.'
  else
    [[ "$digest" == "$target_digest" ]] || fail 'Target immutable archive changed.'
  fi
  curl --fail --silent --show-error --location --retry 3 --max-time 120 \
    "https://github.com/$repository/releases/download/v$version/$archive" -o "$work_dir/releases/$version/$archive"
  curl --fail --silent --show-error --location --retry 3 --max-time 120 \
    "https://github.com/$repository/releases/download/v$version/$archive.sha256" -o "$work_dir/releases/$version/$archive.sha256"
  (cd "$work_dir/releases/$version"; sha256sum --check "$archive.sha256")
  [[ "$(sha256sum "$work_dir/releases/$version/$archive" | cut -d' ' -f1)" == "$digest" ]] || fail 'Published digest mismatch.'
  gh attestation verify "$work_dir/releases/$version/$archive" --repo "$repository" \
    --signer-workflow "$repository/.github/workflows/release.yml" > "$log_dir/attestation-$version.log" 2>&1
  tar -xzf "$work_dir/releases/$version/$archive" -C "$work_dir/source/$version" --strip-components=1
  [[ "$(tr -d '[:space:]' < "$work_dir/source/$version/VERSION")" == "$version" ]] || fail 'Archive VERSION mismatch.'
  printf '%s' "$digest" > "$work_dir/releases/$version/digest"
}
fetch_release "$previous"
fetch_release "$target"
latest_url="$(curl --fail --silent --show-error --location --max-time 60 -o /dev/null -w '%{url_effective}' "https://github.com/$repository/releases/latest")"
[[ "$latest_url" == "https://github.com/$repository/releases/tag/v$target" ]] || fail 'Latest stable changed; explicitly update this version-pinned fixture.'
gate 'Published immutable archives, checksums and release-workflow attestations verified'

mkdir -p "$TESLAMATE_DIR/api" "$TESLAMATE_DIR/mosquitto"
cat > "$TESLAMATE_DIR/docker-compose.yml" <<EOF
name: $stack_project
services:
  database:
    image: postgres:18
    environment:
      POSTGRES_USER: fixture_admin
      POSTGRES_PASSWORD: synthetic-lifecycle-bootstrap-only
      POSTGRES_DB: teslamate
    volumes:
      - database:/var/lib/postgresql
    healthcheck:
      test: [CMD-SHELL, "pg_isready -U fixture_admin -d teslamate"]
      interval: 2s
      timeout: 2s
      retries: 30
  mosquitto:
    image: eclipse-mosquitto:2
    volumes:
      - ./mosquitto:/mosquitto/config:ro
  teslamateapi:
    image: caddy:2.8-alpine
    volumes:
      - ./api:/etc/caddy:ro
  caddy:
    image: caddy:2.8-alpine
    ports: ["127.0.0.1:80:80"]
    volumes:
      - .:/etc/caddy:ro
networks:
  default:
    driver: bridge
    enable_ipv6: false
    driver_opts:
      com.docker.network.bridge.name: $bridge
volumes:
  database:
EOF
cat > "$TESLAMATE_DIR/mosquitto/mosquitto.conf" <<'EOF'
listener 1883
allow_anonymous true
persistence false
EOF
cat > "$TESLAMATE_DIR/api/Caddyfile" <<'EOF'
:8080 {
  route {
    @unauthorized not header Authorization "Bearer synthetic-lifecycle-owner-token-not-a-secret"
    respond @unauthorized 401
    @ping path /api/ping
    respond @ping "synthetic API pong" 200
    respond 404
  }
}
EOF
cat > "$TESLAMATE_DIR/Caddyfile" <<'EOF'
:80 {
  reverse_proxy /api/* teslamateapi:8080
}
EOF
# Installer patches TESLAMATE_DIR/Caddyfile; the directory bind sees atomic replacements.
cp "$TESLAMATE_DIR/Caddyfile" "$work_dir/initial-Caddyfile"
# Internal-only Docker networks do not publish the host port needed by the
# unmodified installer. Use a normal dedicated bridge, but deny all new runtime
# egress outside that bridge and all new access to runner-host services. Replies
# to our loopback-published acceptance probes remain allowed. Image pulls and
# build dependency downloads run outside this runtime network.
stack create
iptables -I DOCKER-USER 1 -i "$bridge" ! -o "$bridge" -j DROP
iptables -I DOCKER-USER 1 -i "$bridge" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
iptables -I INPUT 1 -i "$bridge" -j DROP
iptables -I INPUT 1 -i "$bridge" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
firewall_ready=1
iptables -C DOCKER-USER -i "$bridge" ! -o "$bridge" -j DROP
iptables -C INPUT -i "$bridge" -j DROP
stack up -d --wait --wait-timeout 90
# TEST-NET-1 is documentation-only; this bounded probe must not escape the
# fixture bridge. No production service is used as a connectivity target.
if stack exec -T teslamateapi wget -q -T 2 -O /dev/null http://192.0.2.1; then
  fail 'Runtime fixture unexpectedly reached an external network.'
fi
gate 'Dedicated runtime bridge blocks new external/runner-host connections; only loopback ports published'
stack exec -T database psql -U fixture_admin -d teslamate -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE cars (id integer PRIMARY KEY, name text);
CREATE TABLE positions (
  id bigint PRIMARY KEY, car_id integer REFERENCES cars(id), date timestamp(6) NOT NULL,
  tpms_pressure_fl numeric, tpms_pressure_fr numeric, tpms_pressure_rl numeric, tpms_pressure_rr numeric,
  outside_temp numeric, speed integer, drive_id integer
);
CREATE INDEX ON positions(car_id);
CREATE INDEX ON positions USING brin(date);
INSERT INTO cars VALUES (1,'Synthetic car'),(2,'Other synthetic car');
INSERT INTO positions VALUES
  (1,1,'2026-09-20 12:00:00.123456',2.875,NULL,0,2.9,-12.75,0,NULL),
  (2,1,'2026-09-20 12:00:00.123456',2.8,2.9,2.8,2.9,15,25,NULL),
  (3,1,'2026-09-20 12:01:00',2.7,2.8,2.7,2.8,16,30,NULL),
  (4,2,'2026-09-20 12:01:00',3,3,3,3,17,0,NULL);
CREATE ROLE fixture_reader LOGIN PASSWORD 'synthetic-lifecycle-reader-only';
GRANT CONNECT ON DATABASE teslamate TO fixture_reader;
GRANT USAGE ON SCHEMA public TO fixture_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO fixture_reader;
ALTER ROLE fixture_reader SET default_transaction_read_only = on;
SQL
stack ps -q database mosquitto teslamateapi | sort > "$work_dir/stack-identities"
database_manifest() {
  stack exec -T database psql -U fixture_admin -d teslamate -Atc \
    "SELECT json_build_object('cars',(SELECT json_agg(c ORDER BY id) FROM cars c),'positions',(SELECT json_agg(p ORDER BY id) FROM positions p))" | sha256sum | cut -d' ' -f1
}
database_manifest > "$work_dir/database-baseline.sha256"
[[ "$(stack exec -T database psql -U fixture_admin -d teslamate -Atc "SELECT NOT rolsuper AND 'default_transaction_read_only=on'=ANY(rolconfig) FROM pg_roles WHERE rolname='fixture_reader'")" == t ]] || fail 'Fixture application role is not restricted/read-only.'

request() { curl --fail --silent --show-error --max-time 15 -H "Authorization: Bearer $MY_T_API_TOKEN" "$@"; }
verify_health() {
  local version="$1" cid status
  cid="$(companion ps -q companion)"
  [[ -n "$cid" ]] || fail 'No Companion container.'
  for _ in $(seq 1 35); do
    status="$("$real_docker" inspect --format '{{.State.Health.Status}}' "$cid")"
    [[ "$status" == healthy ]] && break
    sleep 1
  done
  [[ "$status" == healthy ]] || fail 'Container did not become healthy.'
  [[ "$("$real_docker" inspect --format '{{.RestartCount}}' "$cid")" == 0 ]] || fail 'Container restarted unexpectedly.'
  "$real_docker" inspect "$cid" | jq -e '.[0] | .HostConfig.ReadonlyRootfs and (.Config.User == "10001:10001") and (.HostConfig.PortBindings["8080/tcp"] == [{"HostIp":"127.0.0.1","HostPort":"8083"}])' >/dev/null
  request http://127.0.0.1/api/v1/capabilities > "$work_dir/capabilities.json"
  jq -e --arg v "$version" '.version == $v and (.capabilities | index("friend_together_v1") != null)' "$work_dir/capabilities.json" >/dev/null
  [[ "$(curl --silent --max-time 10 -o /dev/null -w '%{http_code}' http://127.0.0.1/api/v1/capabilities)" == 401 ]] || fail 'Unauthenticated capabilities accepted.'
  stack ps -q database mosquitto teslamateapi | sort | cmp -s "$work_dir/stack-identities" - || fail 'Unrelated stack containers changed.'
}
verify_tires() {
  local cursor='' page=0 id
  : > "$work_dir/point-ids"
  while :; do
    local args=(--get --data-urlencode from=2026-09-20T00:00:00Z --data-urlencode to=2026-09-21T00:00:00Z --data-urlencode limit=1)
    [[ -z "$cursor" ]] || args+=(--data-urlencode "cursor=$cursor")
    request "${args[@]}" http://127.0.0.1/api/v1/cars/1/tire-pressure-history > "$work_dir/page.json"
    jq -e '.data.returned_count == 1 and .data.points[0].car_id == 1 and .data.points[0].sensor_measured_at == null and .meta.pressure_unit == "bar" and .meta.temperature_unit == "C"' "$work_dir/page.json" >/dev/null
    id="$(jq -r '.data.points[0].position_id' "$work_dir/page.json")"
    printf '%s\n' "$id" >> "$work_dir/point-ids"
    if [[ "$id" == 1 ]]; then
      jq -e '.data.points[0] | .tpms_pressure_fl == 2.875 and .tpms_pressure_fr == null and .tpms_pressure_rl == 0 and .outside_temp == -12.75 and .recorded_at == "2026-09-20T12:00:00.123456Z"' "$work_dir/page.json" >/dev/null
    fi
    page=$((page + 1))
    [[ "$page" -le 3 ]] || fail 'Pagination did not terminate.'
    if [[ "$(jq -r '.data.has_more' "$work_dir/page.json")" == false ]]; then
      jq -e '.data.next_cursor == null' "$work_dir/page.json" >/dev/null
      break
    fi
    cursor="$(jq -er '.data.next_cursor' "$work_dir/page.json")"
  done
  [[ "$(paste -sd, "$work_dir/point-ids")" == 3,2,1 ]] || fail 'Missing, duplicate, reordered or cross-car SQL records.'
  [[ "$(curl --silent --max-time 10 -o /dev/null -w '%{http_code}' http://127.0.0.1/api/v1/cars/1/tire-pressure-history)" == 401 ]] || fail 'Unauthenticated history accepted.'
}
run_update() {
  local version="$1" label="$2"
  MY_T_VERSION="$version" MY_T_EXPECTED_SHA256="$(cat "$work_dir/releases/$version/digest")" \
    "$INSTALL_DIR/update.sh" > "$log_dir/$label.log" 2>&1
}

"$work_dir/source/$target/install.sh" > "$log_dir/clean-$target.log" 2>&1
verify_health "$target"
verify_tires
gate 'Clean 1.10.51 actual install and authenticated Docker-Caddy/Go/Postgres pagination'
"$INSTALL_DIR/uninstall.sh" > "$log_dir/uninstall.log" 2>&1
[[ -z "$(companion ps -q companion)" && -f "$INSTALL_DIR/.env" ]] || fail 'Uninstall did not stop Companion or removed configuration.'
"$real_docker" volume inspect "${COMPOSE_PROJECT}_notification-state" >/dev/null
gate 'Uninstall stops Companion and retains configuration/durable volume (Docker-Caddy routes require separate teardown)'
cp "$work_dir/initial-Caddyfile" "$TESLAMATE_DIR/Caddyfile"
stack restart caddy >/dev/null

export INSTALL_DIR="$work_dir/baseline-install" COMPOSE_PROJECT="$baseline_project"
"$work_dir/source/$previous/install.sh" > "$log_dir/clean-$previous.log" 2>&1
verify_health "$previous"
companion stop companion >/dev/null
state_dir="$("$real_docker" volume inspect --format '{{.Mountpoint}}' "${COMPOSE_PROJECT}_notification-state")"
[[ "$state_dir" == "/var/lib/docker/volumes/${COMPOSE_PROJECT}_notification-state/_data" ]] || fail 'Unexpected fixture volume path.'
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n --arg now "$now" '{cars:{"1":{values:{locked:"true"}}},events:[{id:"synthetic-parking-1",car_id:1,type:"security",field:"locked",value:"true",observed_at:$now,observation_mode:"mqtt_observed"}]}' > "$state_dir/parking-events.json"
jq -n --arg now "$now" '{vehicle_namespace:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",subscribers:[{installation_id:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",source_id:"synthetic-source",relay_url:"https://push.my-tesla.app/v1/events",relay_secret:"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",status:"paused",software_update:true,lock_secure:false,charging_live_activity:false,navigation_live_activity:false,navigation_trip_alerts:false,low_battery:false,vehicle_preferences:[{car_id:1,software_update:true}],updated_at:$now,last_seen_at:$now}],outbox:[]}' > "$state_dir/push-subscribers.json"
jq -n --arg now "$now" '{sessions:[{session_id:"synthetic-completed-1",car_id:1,destination:"Synthetic destination",started_at:$now,ended_at:$now,updated_at:$now,last_event_type:"ended"}]}' > "$state_dir/navigation-push-history.json"
for file in software-notifications.json charging-live-activities.json navigation-live-activities.json; do
  jq -n --arg now "$now" '{cars:{},delivered:{"synthetic-marker":$now}}' > "$state_dir/$file"
done
printf 'synthetic operator state must survive\n' > "$state_dir/operator-sentinel.txt"
chown -R 10001:10001 "$state_dir"
printf 'synthetic operator source note\n' > "$INSTALL_DIR/operator-note.txt"
# Unset these after initial installation: subsequent installers must retain them.
unset FRIEND_TOGETHER_ENABLED FRIEND_TOGETHER_GUEST_ORIGIN FRIEND_TOGETHER_STATE_PATH
companion start companion >/dev/null
verify_health "$previous"

source_manifest() { (cd "$INSTALL_DIR"; find . -type f -print0 | sort -z | xargs -0 sha256sum); }
state_manifest() {
  local file relative
  while IFS= read -r -d '' file; do
    relative="${file#"$state_dir/"}"
    printf '%s ' "$relative"
    if [[ "$relative" == friend-together/state.json ]]; then
      jq -Sc 'del(.last_time_ms)' "$file" | sha256sum | cut -d' ' -f1
    elif [[ "$relative" == *.json ]]; then
      jq -Sc . "$file" | sha256sum | cut -d' ' -f1
    else
      sha256sum "$file" | cut -d' ' -f1
    fi
  done < <(find "$state_dir" -type f -print0 | sort -z)
}
source_manifest > "$work_dir/source-baseline.sha256"
state_manifest > "$work_dir/state-baseline.sha256"
friend_clock="$(jq -er '.last_time_ms' "$state_dir/friend-together/state.json")"
cp -a "$INSTALL_DIR" "$work_dir/prior-install"
cp "$TESLAMATE_DIR/Caddyfile" "$work_dir/prior-Caddyfile"
cp "$INSTALL_DIR/.env" "$work_dir/prior.env"
verify_preservation() {
  cmp -s "$work_dir/prior.env" "$INSTALL_DIR/.env" || fail 'Configuration changed.'
  [[ "$(stat -c '%a' "$INSTALL_DIR/.env")" == 600 ]] || fail 'Private configuration mode changed.'
  state_manifest > "$work_dir/state-current.sha256"
  cmp -s "$work_dir/state-baseline.sha256" "$work_dir/state-current.sha256" || fail 'Durable volume inventory/content changed.'
  local next_clock
  next_clock="$(jq -er '.last_time_ms' "$state_dir/friend-together/state.json")"
  [[ "$next_clock" -ge "$friend_clock" ]] || fail 'Friend Together clock regressed.'
  friend_clock="$next_clock"
  database_manifest | cmp -s "$work_dir/database-baseline.sha256" - || fail 'Source database content changed.'
}
verify_preservation
gate 'Synthetic prior state seeded; entire volume inventory/canonical content and configuration baseline captured'

mkdir "$work_dir/fault-bin"
cat > "$work_dir/fault-bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -f "$MYT_FAULT_PENDING" && "${1:-}" == compose && " $* " == *" build "* ]]; then
  [[ "$(tr -d '[:space:]' < "$INSTALL_DIR/VERSION")" == 1.10.51 ]]
  [[ -f "$INSTALL_DIR/tire_pressure_history.go" && -f "$INSTALL_DIR/tire_pressure_history_test.go" ]]
  mv "$MYT_FAULT_PENDING" "$MYT_FAULT_FIRED"
  printf 'Synthetic one-shot failure after new files copied, before candidate Docker build\n' >&2
  exit 86
fi
exec "$MYT_REAL_DOCKER" "$@"
EOF
chmod 0755 "$work_dir/fault-bin/docker"
touch "$work_dir/fault-pending"
if PATH="$work_dir/fault-bin:$PATH" MYT_REAL_DOCKER="$real_docker" \
  MYT_FAULT_PENDING="$work_dir/fault-pending" MYT_FAULT_FIRED="$work_dir/fault-fired" \
  MY_T_VERSION="$target" MY_T_EXPECTED_SHA256="$(cat "$work_dir/releases/$target/digest")" \
  "$INSTALL_DIR/update.sh" > "$log_dir/failed-update-rollback.log" 2>&1; then
  fail 'Deliberately failed update unexpectedly succeeded.'
fi
[[ -f "$work_dir/fault-fired" && ! -e "$work_dir/fault-pending" ]] || fail 'Failure injection was not exercised.'
verify_health "$previous"
source_manifest | cmp -s "$work_dir/source-baseline.sha256" - || fail 'Failed update did not restore exact source/configuration.'
[[ ! -e "$INSTALL_DIR/tire_pressure_history.go" && ! -e "$INSTALL_DIR/tire_pressure_history_test.go" ]] || fail 'Failed-update source leftovers.'
cmp -s "$work_dir/prior-Caddyfile" "$TESLAMATE_DIR/Caddyfile" || fail 'Pre-build failure changed proxy configuration.'
verify_preservation
gate 'One-shot candidate build failure; actual old installer recovery, exact source/configuration and full durable data retained'

run_update "$target" pinned-upgrade
verify_health "$target"
verify_tires
verify_preservation
gate 'Real digest-pinned 1.10.50 to 1.10.51 upgrade'
MY_T_EXPECTED_SHA256="$(cat "$work_dir/releases/$target/digest")" \
  "$INSTALL_DIR/update.sh" > "$log_dir/latest-repeat-update.log" 2>&1
verify_health "$target"
verify_tires
verify_preservation
[[ "$(grep -c '^[[:space:]]*@my_t_tire_history ' "$TESLAMATE_DIR/Caddyfile")" == 1 ]] || fail 'Repeated update duplicated route.'
gate 'Actual latest-stable updater/repeat install without duplicate routes or state loss'

# Healthy rollback needs exact source restoration: old installers only overwrite.
# Never restore the durable data snapshot; new records would otherwise be lost.
companion stop companion >/dev/null
mv "$INSTALL_DIR" "$work_dir/healthy-target-context"
cp -a "$work_dir/prior-install" "$INSTALL_DIR"
cp "$work_dir/prior-Caddyfile" "$TESLAMATE_DIR/Caddyfile"
stack restart caddy >/dev/null
run_update "$previous" healthy-backup-assisted-rollback
verify_health "$previous"
source_manifest | cmp -s "$work_dir/source-baseline.sha256" - || fail 'Healthy rollback is not exact prior source/configuration.'
[[ ! -e "$INSTALL_DIR/tire_pressure_history.go" && ! -e "$INSTALL_DIR/tire_pressure_history_test.go" ]] || fail 'Healthy rollback retained new tire sources.'
jq -e '.capabilities | index("tire_pressure_history_v1") == null' "$work_dir/capabilities.json" >/dev/null
verify_preservation
gate 'Backup-assisted healthy rollback using real pinned 1.10.50 updater, no source leftovers, no durable-data rewind'
run_update "$target" final-pinned-upgrade
verify_health "$target"
verify_tires
verify_preservation
gate 'Final pinned 1.10.51 re-upgrade and retained source/data/authentication checks'
