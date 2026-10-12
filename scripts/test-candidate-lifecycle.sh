#!/usr/bin/env bash
# Real Docker/installers and synthetic data ONLY. Never run on an operator host.
set -euo pipefail

fail() { printf 'CANDIDATE LIFECYCLE FAIL: %s\n' "$*" >&2; exit 1; }
[[ "${GITHUB_ACTIONS:-}" == true && "${MYT_RUNNER_ENVIRONMENT:-}" == github-hosted &&
   "${RUNNER_OS:-}" == Linux && "${MYT_DISPOSABLE_CANDIDATE:-}" == 1 && "$EUID" == 0 ]] \
  || fail 'Requires explicit opt-in on a disposable GitHub-hosted Linux runner as root.'
for key in INSTALL_DIR TESLAMATE_DIR COMPOSE_PROJECT CADDY_FILE DATABASE_PASS DATABASE_USER DATABASE_HOST \
  DATABASE_URL PGPASSWORD API_TOKEN MY_T_API_TOKEN MY_T_BASE_URL MY_T_AUTH_HEADER MY_T_AUTH_PROBE_URL \
  MY_T_UPDATE_SOURCE_DIR MY_T_RELEASE_BASE_URL MY_T_GITHUB_REPOSITORY PUSH_INSTALLATION_ID PUSH_RELAY_SECRET \
  PUSH_RELAY_URL MQTT_NAMESPACE MQTT_BROKER_URL MQTT_USERNAME MQTT_PASSWORD DOCKER_HOST DOCKER_CONTEXT \
  DOCKER_TLS_VERIFY DOCKER_CERT_PATH FRIEND_TOGETHER_ENABLED FRIEND_TOGETHER_GUEST_ORIGIN; do
  [[ -z "${!key:-}" ]] || fail "Refusing inherited operator configuration: $key"
done
for tool in docker curl jq gh git sha256sum tar openssl ss ip iptables python3; do
  command -v "$tool" >/dev/null || fail "Missing $tool"
done
[[ "$(docker context inspect --format '{{.Endpoints.docker.Host}}')" == unix:///var/run/docker.sock ]] \
  || fail 'Docker must use this disposable runner, never a remote daemon.'
[[ -z "$(docker ps -aq)" ]] || fail 'Runner Docker daemon is not empty.'
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# sudo changes the Git config owner. Trust only this exact checkout for these
# read-only commands, without modifying the runner's global Git configuration.
candidate_git() { git -c safe.directory="$repo_dir" -C "$repo_dir" "$@"; }
[[ -z "$(candidate_git status --porcelain)" ]] || fail 'Candidate checkout is not clean.'
previous=1.10.53
target=1.10.54
repository=MatchHar/My-T-Companion
previous_digest=3796860cb8b85faa50baa70a2da1e01b033261d699cf446b07f6e3af15c10831
mode="${MYT_CANDIDATE_MODE:-gitarchive}"
[[ "$mode" == gitarchive || "$mode" == published ]] || fail 'Mode must be gitarchive or published.'
[[ "$(candidate_git show HEAD:VERSION | tr -d '[:space:]')" == "$target" ]] || fail 'Exact HEAD must have VERSION 1.10.54.'
bridge=myt-candidate0
! ip link show "$bridge" >/dev/null 2>&1 || fail 'Fixture bridge already exists.'
if ss -H -lnt '( sport = :80 or sport = :8083 )' | grep -q .; then fail 'Fixture ports are occupied.'; fi
work_dir="$(mktemp -d /tmp/myt-candidate.XXXXXXXX)"
log_dir="${RUNNER_TEMP:?}/myt-candidate-lifecycle-logs"
mkdir -p "$log_dir"
prefix="myt-candidate-${work_dir##*.}"
prefix="${prefix,,}"
stack_project="${prefix}-stack"
clean_project="${prefix}-clean"
baseline_project="${prefix}-baseline"
real_docker="$(command -v docker)"
firewall_ready=0
http_pid=''
export TESLAMATE_DIR="$work_dir/teslamate" CADDY_FILE="$work_dir/no-system-caddy"
export INSTALL_DIR="$work_dir/clean-install" COMPOSE_PROJECT="$clean_project"
export DATABASE_USER=fixture_reader DATABASE_PASS=synthetic-candidate-reader-only
export MY_T_API_TOKEN=synthetic-candidate-owner-token-not-a-secret MY_T_BASE_URL=http://127.0.0.1
export TESLAMATE_VERSION=4.3.0 TESLAMATE_WEB_URL=http://teslamateapi:8080
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
  [[ -z "$http_pid" ]] || kill "$http_pid" 2>/dev/null
  printf 'exit_status=%s\n' "$status" >> "$log_dir/gates.txt"
  chmod -R a+rX "$log_dir"
  # Scratch directories remain on this ephemeral runner; no broad host deletion.
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
  [[ "$version" != "$previous" || "$digest" == "$previous_digest" ]] || fail 'Previous immutable archive changed.'
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
mkdir -p "$work_dir/http/candidate" "$work_dir/http/fault" "$work_dir/source/$target"
archive="my-t-companion-$target.tar.gz"
candidate_git archive --format=tar.gz --prefix="my-t-companion-$target/" \
  --output="$work_dir/http/candidate/$archive" HEAD
(cd "$work_dir/http/candidate"; sha256sum "$archive" > "$archive.sha256")
candidate_digest="$(sha256sum "$work_dir/http/candidate/$archive" | cut -d' ' -f1)"
tar -xzf "$work_dir/http/candidate/$archive" -C "$work_dir/source/$target" --strip-components=1
if [[ "$mode" == published ]]; then
  fetch_release "$target"
  [[ "$(<"$work_dir/releases/$target/digest")" == "$candidate_digest" ]] || fail 'Published target is not the exact HEAD archive.'
fi
jq -n --arg mode "$mode" --arg commit "$(candidate_git rev-parse HEAD)" \
  --arg candidate_sha256 "$candidate_digest" --arg previous_sha256 "$previous_digest" \
  '{mode:$mode,head_commit:$commit,candidate_sha256:$candidate_sha256,previous_sha256:$previous_sha256,
    candidate_attestation_verified:($mode=="published"),runtime_data:"synthetic only"}' > "$log_dir/source-evidence.json"
gate 'Immutable 1.10.53 digest/checksum/provenance verified; exact clean HEAD candidate archive recorded'

# The negative archive is intentionally NOT the publication candidate. Only its
# Dockerfile differs, forcing a real Docker build failure after installer copy.
mkdir -p "$work_dir/fault-source/my-t-companion-$target"
cp -a "$work_dir/source/$target/." "$work_dir/fault-source/my-t-companion-$target/"
printf '\nRUN echo SYNTHETIC_CANDIDATE_BUILD_FAILURE >&2; exit 86\n' >> "$work_dir/fault-source/my-t-companion-$target/Dockerfile"
tar -czf "$work_dir/http/fault/$archive" -C "$work_dir/fault-source" "my-t-companion-$target"
(cd "$work_dir/http/fault"; sha256sum "$archive" > "$archive.sha256")
fault_digest="$(sha256sum "$work_dir/http/fault/$archive" | cut -d' ' -f1)"
printf '%s\n' "$fault_digest" > "$log_dir/negative-fixture-only.sha256"
# Real loopback HTTP transport, not a curl shim. No fixture container can reach
# runner-host services because the INPUT rules below deny new bridge traffic.
python3 -u - "$work_dir/http" "$work_dir/http-port" > "$log_dir/archive-http.log" 2>&1 <<'PY' &
import functools, http.server, pathlib, sys
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(http.server.SimpleHTTPRequestHandler, directory=sys.argv[1]))
pathlib.Path(sys.argv[2]).write_text(str(server.server_port))
server.serve_forever()
PY
http_pid=$!
for _ in $(seq 1 50); do [[ ! -s "$work_dir/http-port" ]] || break; sleep 0.1; done
[[ -s "$work_dir/http-port" ]] || fail 'Loopback archive server did not start.'
transport="http://127.0.0.1:$(<"$work_dir/http-port")"
target_base="$transport/candidate"
[[ "$mode" != published ]] || target_base="https://github.com/$repository/releases/download/v$target"

mkdir -p "$TESLAMATE_DIR/api" "$TESLAMATE_DIR/mosquitto"
cat > "$TESLAMATE_DIR/docker-compose.yml" <<EOF
name: $stack_project
services:
  database:
    image: postgres:18
    environment:
      POSTGRES_USER: fixture_admin
      POSTGRES_PASSWORD: synthetic-candidate-bootstrap-only
      POSTGRES_DB: teslamate
    volumes: [database:/var/lib/postgresql]
    healthcheck:
      test: [CMD-SHELL, "pg_isready -U fixture_admin -d teslamate"]
      interval: 2s
      timeout: 2s
      retries: 30
  mosquitto:
    image: eclipse-mosquitto:2
    volumes: [./mosquitto:/mosquitto/config:ro]
  teslamateapi:
    image: caddy:2.8-alpine
    volumes: [./api:/etc/caddy:ro]
  caddy:
    image: caddy:2.8-alpine
    ports: ["127.0.0.1:80:80"]
    volumes: [.:/etc/caddy:ro]
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
persistence true
persistence_location /mosquitto/data/
autosave_interval 1
EOF
cat > "$TESLAMATE_DIR/api/Caddyfile" <<'EOF'
:8080 {
  route {
    @unauthorized not header Authorization "Bearer synthetic-candidate-owner-token-not-a-secret"
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
cp "$TESLAMATE_DIR/Caddyfile" "$work_dir/initial-Caddyfile"
# Literal internal:true prevents the released installer's host-loopback health
# probes on this fixture. Instead use the previously accepted dedicated bridge
# with kernel-enforced runtime egress/host-service denial. Pull/build downloads
# are outside this runtime bridge; never claim this is fully air-gapped CI.
stack create
iptables -I DOCKER-USER 1 -i "$bridge" ! -o "$bridge" -j DROP
iptables -I DOCKER-USER 1 -i "$bridge" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
iptables -I INPUT 1 -i "$bridge" -j DROP
iptables -I INPUT 1 -i "$bridge" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
firewall_ready=1
iptables -C DOCKER-USER -i "$bridge" ! -o "$bridge" -j DROP
iptables -C INPUT -i "$bridge" -j DROP
stack up -d --wait --wait-timeout 90
if stack exec -T teslamateapi wget -q -T 2 -O /dev/null http://192.0.2.1; then fail 'Fixture runtime egress is open.'; fi
docker image inspect postgres:18 eclipse-mosquitto:2 caddy:2.8-alpine \
  --format '{{.Id}} {{json .RepoDigests}}' > "$log_dir/fixture-images.txt"
gate 'Dedicated runtime bridge denies external/runner-host connections; ingress is loopback only'
stack exec -T database psql -U fixture_admin -d teslamate -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE car_settings (id integer PRIMARY KEY, enabled boolean NOT NULL);
CREATE TABLE cars (id integer PRIMARY KEY, name text, settings_id integer REFERENCES car_settings(id));
CREATE TABLE states (id bigint PRIMARY KEY, car_id integer REFERENCES cars(id), state text NOT NULL, start_date timestamp(6) NOT NULL, end_date timestamp(6));
CREATE TABLE drives (id bigint PRIMARY KEY, car_id integer REFERENCES cars(id), start_date timestamp(6), end_date timestamp(6), start_geofence_id integer, start_address_id integer, end_geofence_id integer, end_address_id integer);
CREATE TABLE positions (
  id bigint PRIMARY KEY, car_id integer REFERENCES cars(id), date timestamp(6) NOT NULL,
  tpms_pressure_fl numeric, tpms_pressure_fr numeric, tpms_pressure_rl numeric, tpms_pressure_rr numeric,
  outside_temp numeric, speed integer, drive_id bigint REFERENCES drives(id), battery_level integer,
  rated_battery_range_km numeric, latitude numeric, longitude numeric, odometer numeric, address_id integer
);
CREATE TABLE charging_processes (id bigint PRIMARY KEY, car_id integer REFERENCES cars(id), position_id bigint REFERENCES positions(id), start_date timestamp(6), end_date timestamp(6));
CREATE TABLE geofences (id integer PRIMARY KEY, name text);
CREATE TABLE addresses (id integer PRIMARY KEY, name text, display_name text);
CREATE INDEX ON positions(car_id);
CREATE INDEX ON positions USING brin(date);
INSERT INTO car_settings VALUES (1,true),(2,true);
INSERT INTO cars VALUES (1,'Synthetic car',1),(2,'Other synthetic car',2);
INSERT INTO drives (id,car_id,start_date,end_date) VALUES (101,1,'2026-10-11 12:00:00.123456',NULL);
INSERT INTO positions (id,car_id,date,tpms_pressure_fl,tpms_pressure_fr,tpms_pressure_rl,tpms_pressure_rr,outside_temp,speed,battery_level,rated_battery_range_km,latitude,longitude,odometer) VALUES
 (1,1,'2026-10-11 12:00:00.123456',2.875,NULL,0,2.9,-12.75,0,50,300,43,-79,1000),
 (2,1,'2026-10-11 12:00:00.123456',2.8,2.9,2.8,2.9,15,25,49,294,43.001,-79,1000.1),
 (3,1,'2026-10-11 12:01:00',2.7,2.8,2.7,2.8,16,30,48,288,43.002,-79,1000.2),
 (4,2,'2026-10-11 12:01:00',3,3,3,3,17,0,60,360,44,-78,2000);
UPDATE positions SET drive_id=101 WHERE car_id=1;
INSERT INTO states VALUES (1,1,'online','2026-10-11 11:59:00','2026-10-11 12:02:00');
CREATE ROLE fixture_reader LOGIN PASSWORD 'synthetic-candidate-reader-only';
GRANT CONNECT ON DATABASE teslamate TO fixture_reader;
GRANT USAGE ON SCHEMA public TO fixture_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO fixture_reader;
ALTER ROLE fixture_reader SET default_transaction_read_only = on;
SQL
stack ps -q database mosquitto teslamateapi | sort > "$work_dir/stack-identities"
database_manifest() {
  stack exec -T database pg_dump -U fixture_admin -d teslamate --data-only --no-owner --no-privileges \
    | sed '/^--/d; /^\\restrict /d; /^\\unrestrict /d' | sha256sum | cut -d' ' -f1
}
database_manifest > "$work_dir/database-baseline.sha256"
[[ "$(stack exec -T database psql -U fixture_admin -d teslamate -Atc "SELECT NOT rolsuper AND 'default_transaction_read_only=on'=ANY(rolconfig) FROM pg_roles WHERE rolname='fixture_reader'")" == t ]] || fail 'Application DB role is not restricted/read-only.'
request() { curl --fail --silent --show-error --max-time 15 -H "Authorization: Bearer $MY_T_API_TOKEN" "$@"; }
verify_health() {
  local version="$1" cid status=''
  cid="$(companion ps -q companion)"
  [[ -n "$cid" ]] || fail 'No Companion container.'
  for _ in $(seq 1 40); do
    status="$("$real_docker" inspect --format '{{.State.Health.Status}}' "$cid")"
    [[ "$status" != healthy ]] || break
    sleep 1
  done
  [[ "$status" == healthy ]] || fail 'Container did not become healthy.'
  [[ "$("$real_docker" inspect --format '{{.RestartCount}}' "$cid")" == 0 ]] || fail 'Unexpected container restart.'
  "$real_docker" inspect "$cid" | jq -e '.[0] | .HostConfig.ReadonlyRootfs and (.Config.User == "10001:10001") and (.HostConfig.PortBindings["8080/tcp"] == [{"HostIp":"127.0.0.1","HostPort":"8083"}])' >/dev/null
  request http://127.0.0.1/api/v1/capabilities > "$work_dir/capabilities.json"
  jq -e --arg v "$version" '.version == $v and (.capabilities | index("friend_together_v1") != null)' "$work_dir/capabilities.json" >/dev/null
  if [[ "$version" == "$target" ]]; then
    jq -e '.sql_features | .states.supported == true and .current_drive.supported == true and .friend_source.supported == true and .mqtt_fleet.supported == true' "$work_dir/capabilities.json" >/dev/null
  fi
  [[ "$(curl --silent --max-time 10 -o /dev/null -w '%{http_code}' http://127.0.0.1/api/v1/capabilities)" == 401 ]] || fail 'Unauthenticated capabilities accepted.'
  stack ps -q database mosquitto teslamateapi | sort | cmp -s "$work_dir/stack-identities" - || fail 'Unrelated stack containers changed.'
}
verify_sql_reads() {
  request --get --data-urlencode from=2026-10-11T00:00:00Z --data-urlencode to=2026-10-12T00:00:00Z \
    --data-urlencode limit=10 http://127.0.0.1/api/v1/cars/1/tire-pressure-history > "$work_dir/tires.json"
  jq -e '.data.returned_count == 3 and ([.data.points[].position_id] == [3,2,1]) and (.data.points[-1] | .sensor_measured_at == null and .tpms_pressure_fl == 2.875 and .tpms_pressure_fr == null and .tpms_pressure_rl == 0 and .outside_temp == -12.75 and .recorded_at == "2026-10-11T12:00:00.123456Z")' "$work_dir/tires.json" >/dev/null
  request 'http://127.0.0.1/api/v1/cars/1/states?startDate=2026-10-11&endDate=2026-10-12' > "$work_dir/states.json"
  jq -e '.data.car.car_id == 1 and (.data.states | length == 1) and (.data.states[0] | .state == "online" and .start_date == "2026-10-11T11:59:00Z" and .end_date == "2026-10-11T12:02:00Z")' "$work_dir/states.json" >/dev/null
  request http://127.0.0.1/api/v1/cars/1/navigation/current-drive > "$work_dir/current-drive.json"
  jq -e '.data.car.car_id == 1 and (.data.drive | .drive_id == 101 and .is_ongoing == true and .data_status == "available" and .returned_count == 3 and ([.points[].id] == [1,2,3]) and .first_point.id == 1 and .points[-1].speed == 30)' "$work_dir/current-drive.json" >/dev/null
  [[ "$(curl --silent --max-time 10 -o /dev/null -w '%{http_code}' http://127.0.0.1/api/v1/cars/1/tire-pressure-history)" == 401 ]] || fail 'Unauthenticated SQL history accepted.'
}
mqtt_pub() { stack exec -T mosquitto mosquitto_pub -h localhost "$@"; }
verify_mqtt_candidate() {
  local status_url=http://127.0.0.1/api/v1/cars/1/companion-status cid epoch
  for pair in 'locked false' 'battery_level 50' 'rated_battery_range_km 300'; do
    mqtt_pub -r -t "teslamate/fixture-430/cars/1/${pair%% *}" -m "${pair#* }"
  done
  for _ in $(seq 1 30); do
    request "$status_url" > "$work_dir/mqtt-status.json"
    jq -e '.data.locked == false and .data.mqtt_diagnostics.core_current_connection_complete == true' "$work_dir/mqtt-status.json" >/dev/null && break
    sleep 1
  done
  jq -e '.data.locked == false and .data.mqtt_diagnostics.core_current_connection_complete == true' "$work_dir/mqtt-status.json" >/dev/null
  mqtt_pub -r -t teslamate/other-source/cars/1/locked -m true
  sleep 1
  request "$status_url" | jq -e '.data.locked == false' >/dev/null
  cid="$(companion ps -q companion)"
  epoch="$(jq -er '.data.mqtt_diagnostics.connections.parking.epoch' "$work_dir/mqtt-status.json")"
  stack restart mosquitto >/dev/null
  for _ in $(seq 1 45); do
    request "$status_url" > "$work_dir/mqtt-status.json"
    jq -e --argjson old "$epoch" '.data.locked == false and .data.mqtt_diagnostics.core_current_connection_complete and .data.mqtt_diagnostics.connections.parking.epoch > $old' "$work_dir/mqtt-status.json" >/dev/null && break
    sleep 1
  done
  jq -e --argjson old "$epoch" '.data.locked == false and .data.mqtt_diagnostics.core_current_connection_complete and .data.mqtt_diagnostics.connections.parking.epoch > $old' "$work_dir/mqtt-status.json" >/dev/null
  stack ps -q database mosquitto teslamateapi | sort > "$work_dir/stack-identities"
  stack exec -T database psql -U fixture_admin -d teslamate -v ON_ERROR_STOP=1 -c \
    "INSERT INTO car_settings VALUES (73,true); INSERT INTO cars VALUES (73,'New synthetic car',73);" >/dev/null
  mqtt_pub -r -t teslamate/fixture-430/cars/73/locked -m false
  for _ in $(seq 1 45); do
    request http://127.0.0.1/api/v1/cars/73/companion-status > "$work_dir/new-car.json"
    jq -e '.data.locked == false and .data.mqtt_diagnostics.logging_enabled == true' "$work_dir/new-car.json" >/dev/null && break
    sleep 1
  done
  jq -e '.data.locked == false and .data.mqtt_diagnostics.logging_enabled == true' "$work_dir/new-car.json" >/dev/null
  [[ "$(companion ps -q companion)" == "$cid" && "$(docker inspect --format '{{.RestartCount}}' "$cid")" == 0 ]] || fail 'New vehicle required a Companion restart.'
  request 'http://127.0.0.1/api/v1/cars/73/parking-events?startDate=2026-01-01&endDate=2027-01-01' > "$work_dir/new-car-events.json"
  jq -e '.data.events | length == 0' "$work_dir/new-car-events.json" >/dev/null
  for field in locked battery_level rated_battery_range_km; do mqtt_pub -r -n -t "teslamate/fixture-430/cars/1/$field"; done
  mqtt_pub -r -n -t teslamate/fixture-430/cars/73/locked
  mqtt_pub -r -n -t teslamate/other-source/cars/1/locked
  stack exec -T database psql -U fixture_admin -d teslamate -v ON_ERROR_STOP=1 -c 'DELETE FROM cars WHERE id=73; DELETE FROM car_settings WHERE id=73;' >/dev/null
  database_manifest > "$work_dir/database-baseline.sha256"
  gate 'Real broker retained baseline, namespace isolation, reconnect and newly-added enabled car without Companion restart'
}

export MQTT_NAMESPACE=fixture-430
"$work_dir/source/$target/install.sh" > "$log_dir/clean-candidate.log" 2>&1
verify_health "$target"
verify_sql_reads
verify_mqtt_candidate
"$INSTALL_DIR/uninstall.sh" > "$log_dir/clean-uninstall.log" 2>&1
[[ -z "$(companion ps -q companion)" && -f "$INSTALL_DIR/.env" ]] || fail 'Clean uninstall lost configuration or kept service.'
docker volume inspect "${COMPOSE_PROJECT}_notification-state" >/dev/null
gate 'Clean candidate actual Docker install/API/SQL/broker smoke and uninstall retaining configuration/volume'
cp "$work_dir/initial-Caddyfile" "$TESLAMATE_DIR/Caddyfile"
stack restart caddy >/dev/null
unset MQTT_NAMESPACE
export INSTALL_DIR="$work_dir/baseline-install" COMPOSE_PROJECT="$baseline_project"
MY_T_VERSION="$previous" MY_T_EXPECTED_SHA256="$previous_digest" \
  "$work_dir/source/$previous/update.sh" > "$log_dir/clean-previous.log" 2>&1
verify_health "$previous"
verify_sql_reads
companion stop companion >/dev/null
state_dir="$(docker volume inspect --format '{{.Mountpoint}}' "${COMPOSE_PROJECT}_notification-state")"
[[ "$state_dir" == "/var/lib/docker/volumes/${COMPOSE_PROJECT}_notification-state/_data" ]] || fail 'Unexpected fixture volume path.'
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n --arg now "$now" '{cars:{"1":{values:{locked:"true"}}},events:[{id:"synthetic-parking",car_id:1,type:"vehicle_locked",field:"locked",value:"true",observed_at:$now,observation_mode:"teslamate_mqtt_first_observed"}]}' > "$state_dir/parking-events.json"
jq -n --arg now "$now" '{vehicle_namespace:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",subscribers:[{installation_id:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",source_id:"synthetic-source",relay_url:"https://push.my-tesla.app/v1/events",relay_secret:"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",status:"paused",software_update:true,lock_secure:false,charging_live_activity:false,navigation_live_activity:false,navigation_trip_alerts:false,low_battery:false,vehicle_preferences:[{car_id:1,software_update:true}],updated_at:$now,last_seen_at:$now}],outbox:[]}' > "$state_dir/push-subscribers.json"
jq -n --arg now "$now" '{sessions:[{session_id:"synthetic-completed",car_id:1,destination:"Synthetic destination",started_at:$now,ended_at:$now,updated_at:$now,last_event_type:"navigation_ended"}]}' > "$state_dir/navigation-push-history.json"
for file in software-notifications.json charging-live-activities.json navigation-live-activities.json; do
  jq -n --arg now "$now" '{cars:{},delivered:{"synthetic-marker":$now}}' > "$state_dir/$file"
done
printf 'synthetic operator durable state\n' > "$state_dir/operator-sentinel.txt"
chown -R 10001:10001 "$state_dir"
printf 'synthetic operator source note\n' > "$INSTALL_DIR/operator-note.txt"
unset FRIEND_TOGETHER_ENABLED FRIEND_TOGETHER_GUEST_ORIGIN FRIEND_TOGETHER_STATE_PATH
companion start companion >/dev/null
verify_health "$previous"
source_manifest() { (cd "$INSTALL_DIR"; find . -type f -print0 | sort -z | xargs -0 sha256sum); }
state_manifest() {
  local file relative
  while IFS= read -r -d '' file; do
    relative="${file#"$state_dir/"}"
    printf '%s ' "$relative"
    if [[ "$relative" == friend-together/state.json ]]; then jq -Sc 'del(.last_time_ms)' "$file" | sha256sum | cut -d' ' -f1
    elif [[ "$relative" == *.json ]]; then jq -Sc . "$file" | sha256sum | cut -d' ' -f1
    else sha256sum "$file" | cut -d' ' -f1; fi
  done < <(find "$state_dir" -type f -print0 | sort -z)
}
source_manifest > "$work_dir/source-baseline.sha256"
state_manifest > "$work_dir/state-baseline.sha256"
cp -a "$INSTALL_DIR" "$work_dir/prior-install"
cp "$TESLAMATE_DIR/Caddyfile" "$work_dir/prior-Caddyfile"
cp "$INSTALL_DIR/.env" "$work_dir/prior.env"
verify_preservation() {
  local line key value actual
  # Candidate may add new safe configuration keys; every prior value must remain.
  while IFS= read -r line; do
    [[ "$line" == *=* ]] || continue
    key="${line%%=*}"; value="${line#*=}"
    actual="$(sed -n "s/^${key}=//p" "$INSTALL_DIR/.env")"
    [[ "$actual" == "$value" ]] || fail "Prior configuration value changed: $key"
  done < "$work_dir/prior.env"
  [[ "$(stat -c '%a' "$INSTALL_DIR/.env")" == 600 ]] || fail 'Configuration permissions changed.'
  state_manifest > "$work_dir/state-current.sha256"
  cmp -s "$work_dir/state-baseline.sha256" "$work_dir/state-current.sha256" || fail 'Durable inventory/canonical contents changed.'
  database_manifest | cmp -s "$work_dir/database-baseline.sha256" - || fail 'Source database content changed.'
}
verify_preservation
gate 'Prior immutable53 actual install and synthetic configuration/pairing/history/volume baselines captured'

if MY_T_VERSION="$target" MY_T_EXPECTED_SHA256="$fault_digest" MY_T_RELEASE_BASE_URL="$transport/fault" \
  "$INSTALL_DIR/update.sh" > "$log_dir/failed-candidate-recovery.log" 2>&1; then
  fail 'Negative candidate unexpectedly built successfully.'
fi
grep -q SYNTHETIC_CANDIDATE_BUILD_FAILURE "$log_dir/failed-candidate-recovery.log" || fail 'Real negative Docker build not exercised.'
grep -q 'Previous version restored' "$log_dir/failed-candidate-recovery.log" || fail 'Actual previous installer recovery not exercised.'
verify_health "$previous"
source_manifest | cmp -s "$work_dir/source-baseline.sha256" - || fail 'Failed upgrade did not restore exact source/configuration.'
cmp -s "$work_dir/prior-Caddyfile" "$TESLAMATE_DIR/Caddyfile" || fail 'Failed build changed proxy configuration.'
verify_preservation
gate 'Real candidate Docker build failure; released53 updater invokes real previous installer; healthy exact-source/data recovery'
upgrade_candidate() {
  MY_T_VERSION="$target" MY_T_EXPECTED_SHA256="$candidate_digest" MY_T_RELEASE_BASE_URL="$target_base" \
    "$INSTALL_DIR/update.sh" > "$log_dir/$1.log" 2>&1
}
upgrade_candidate successful-upgrade
verify_health "$target"
verify_sql_reads
verify_preservation
[[ "$(<"$INSTALL_DIR/RELEASE_SHA256")" == "$candidate_digest" ]] || fail 'Installed target digest mismatch.'
gate 'Real digest-pinned53 to exact54 candidate upgrade; prior values/pairing/history/durable data retained'
upgrade_candidate same-version-repeat
verify_health "$target"
verify_preservation
gate 'Same-version digest-verified repeat update is non-destructive'

# Old installers overwrite files; healthy downgrade needs the complete prior
# source/config snapshot. Never restore a durable-data snapshot or rewind data.
companion stop companion >/dev/null
mv "$INSTALL_DIR" "$work_dir/healthy-candidate-context"
cp -a "$work_dir/prior-install" "$INSTALL_DIR"
cp "$work_dir/prior-Caddyfile" "$TESLAMATE_DIR/Caddyfile"
stack restart caddy >/dev/null
MY_T_ALLOW_DOWNGRADE=1 "$INSTALL_DIR/install.sh" > "$log_dir/healthy-backup-assisted-rollback.log" 2>&1
verify_health "$previous"
verify_sql_reads
source_manifest | cmp -s "$work_dir/source-baseline.sha256" - || fail 'Healthy rollback did not restore exact prior source/config.'
verify_preservation
gate 'Backup-assisted healthy rollback runs real immutable53 installer; no new source leftovers or durable-data rewind'
upgrade_candidate final-upgrade
verify_health "$target"
verify_sql_reads
verify_preservation
"$INSTALL_DIR/uninstall.sh" > "$log_dir/final-uninstall.log" 2>&1
[[ -z "$(companion ps -q companion)" && -f "$INSTALL_DIR/.env" ]] || fail 'Final uninstall retained service or lost config.'
docker volume inspect "${COMPOSE_PROJECT}_notification-state" >/dev/null
verify_preservation
gate 'Final re-upgrade and actual uninstall retain all configuration/pairing/history/durable data; Docker-Caddy routes need separate teardown'
