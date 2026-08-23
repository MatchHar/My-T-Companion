#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
catalog="$repo_root/hostbox/myt-stack.json"
signature="$catalog.sig"
public_key="$repo_root/hostbox/hostbox-catalog-signing-public.pem"

jq -e '
  .schema_version == 1 and
  .template_id == "myt-stack" and
  .channel == "stable" and
  .upstream.teslamate.follow_latest_release == false and
  .upstream.teslamateapi.follow_latest_release == false and
  .upstream.companion.follow_latest_release == false and
  (.revision | test("^[0-9]{4}\\.[0-9]{2}\\.[0-9]{2}\\.[0-9]+$")) and
  (.images.teslamate | test("^teslamate/teslamate:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
  (.images.grafana | test("^teslamate/grafana:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
  (.images.teslamateapi | test("^tobiasehlert/teslamateapi:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
  (.images.postgres | test("^postgres:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
  (.images.mosquitto | test("^eclipse-mosquitto:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
  (.images.caddy | test("^caddy:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
  (.images.companion | test("^myt/companion:[0-9]+\\.[0-9]+\\.[0-9]+$")) and
  (.artifacts.companion_archive_sha256 | test("^[0-9a-f]{64}$")) and
  .paths.install_dir == "/opt/teslamate" and
  .paths.env_file == "/opt/teslamate/.env" and
  .paths.compose_file == "/opt/teslamate/docker-compose.yml" and
  (.notes_zh | length > 0) and
  (.notes_zh_hant | length > 0) and
  (.notes_en | length > 0) and
  (.hints_zh | type == "object") and
  (.hints_zh_hant | type == "object") and
  (.hints_en | type == "object")
' "$catalog" >/dev/null

signature_binary=$(mktemp)
trap 'rm -f "$signature_binary"' EXIT
openssl base64 -d -A -in "$signature" -out "$signature_binary"
openssl pkeyutl -verify -pubin -inkey "$public_key" -rawin \
  -in "$catalog" -sigfile "$signature_binary" >/dev/null
echo "HostBox catalog signature and policy verified"
