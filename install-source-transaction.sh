#!/usr/bin/env bash
# Sourced by install.sh. Keep rollback compatible with already-installed updaters:
# their old installer only overwrites sources, and cannot remove newly added files.
myt_begin_source_transaction() {
  case "$INSTALL_DIR" in
    /|/opt|/usr|/usr/local|/etc|/var|/home|/Users|"${HOME:-/}"|*/.|*/..|*/../*|*/./*|*/)
      printf 'Refusing broad or unnormalized installation directory\n' >&2; return 1 ;;
  esac
  [[ "$INSTALL_DIR" == /* && "$INSTALL_DIR" != / && ! -L "$INSTALL_DIR" ]] || {
    printf 'Unsafe Companion installation directory\n' >&2
    return 1
  }
  local parent
  parent="$(dirname "$INSTALL_DIR")"
  [[ -d "$parent" ]] || return 1
  MYT_SOURCE_BACKUP="$(umask 077; mktemp -d "$parent/.my-t-install-source.XXXXXXXX")"
  MYT_SOURCE_EXISTED=false
  if [[ -d "$INSTALL_DIR" ]]; then
    cp -a "$INSTALL_DIR" "$MYT_SOURCE_BACKUP/before" || return 1
    MYT_SOURCE_EXISTED=true
  fi
  trap 'myt_finish_source_transaction "$?"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
}

myt_finish_source_transaction() {
  local status="$1"
  trap - EXIT INT TERM
  if [[ "$status" == 0 ]]; then
    # Only remove our own successful transaction snapshot, never INSTALL_DIR.
    if [[ -d "$MYT_SOURCE_BACKUP" && "$(basename "$MYT_SOURCE_BACKUP")" == .my-t-install-source.* ]]; then
      rm -rf -- "$MYT_SOURCE_BACKUP"
    fi
    return 0
  fi
  # Move aside instead of deleting failed sources. This also preserves any
  # operator-owned files for recovery; durable Docker volumes are not touched.
  if [[ -d "$INSTALL_DIR" ]]; then
    mv -- "$INSTALL_DIR" "$MYT_SOURCE_BACKUP/failed" || exit "$status"
  fi
  if [[ "$MYT_SOURCE_EXISTED" == true ]]; then
    if cp -a "$MYT_SOURCE_BACKUP/before" "$INSTALL_DIR"; then
      printf '[My T Companion] Previous source/configuration restored; updater can restart the previous version.\n' >&2
    else
      printf '[My T Companion] Source restore failed; recover from %s/before\n' "$MYT_SOURCE_BACKUP" >&2
    fi
  fi
  printf '[My T Companion] Failed installation retained at %s\n' "$MYT_SOURCE_BACKUP" >&2
  exit "$status"
}
