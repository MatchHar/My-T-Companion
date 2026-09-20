#!/usr/bin/env bash
# Sourced by both entry points. No mutations until target/current are understood.
myt_valid_version() { [[ "$1" =~ ^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$ ]]; }
myt_version_is_lower() {
  awk -v proposed="$1" -v installed="$2" 'BEGIN {
    split(proposed,a,"."); split(installed,b,".");
    for(i=1;i<=3;i++){if(a[i]+0 < b[i]+0)exit 0;if(a[i]+0 > b[i]+0)exit 1}
    exit 1
  }'
}
myt_guard_version() {
  local target="$1" current
  myt_valid_version "$target" || fail "Invalid target release version."
  [[ ! -L "$INSTALL_DIR" ]] || fail "Installation symlink requires manual review."
  if [[ -f "$INSTALL_DIR/VERSION" ]]; then
    current="$(tr -d '[:space:]' < "$INSTALL_DIR/VERSION")"
    myt_valid_version "$current" || fail "Installed version is unknown; refusing to overwrite."
    if myt_version_is_lower "$target" "$current" && [[ "${MY_T_ALLOW_DOWNGRADE:-0}" != 1 ]]; then
      fail "Refusing downgrade $current → $target. Explicit rollback requires MY_T_ALLOW_DOWNGRADE=1."
    fi
  elif [[ -e "$INSTALL_DIR/install.sh" || -e "$INSTALL_DIR/docker-compose.yml" || -e "$INSTALL_DIR/.env" ]]; then
    fail "Existing installation has no readable VERSION; refusing to treat it as a new installation."
  fi
}
myt_acquire_operation_lock() {
  command -v flock >/dev/null 2>&1 || fail "Missing flock (util-linux); no installation was changed."
  if [[ "${MY_T_OPERATION_LOCK_HELD:-0}" == 1 && -e /proc/$$/fd/9 ]]; then return; fi
  [[ -d "$(dirname "$INSTALL_DIR")" ]] || fail "Installation parent directory is missing."
  exec 9>"${INSTALL_DIR}.operation.lock"
  flock -n 9 || fail "Another Companion installation/update is running."
  export MY_T_OPERATION_LOCK_HELD=1
}
