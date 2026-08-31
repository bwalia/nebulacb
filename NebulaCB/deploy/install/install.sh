#!/usr/bin/env bash
#
# NebulaCB system installer
# Supports: Ubuntu, Debian, CentOS/RHEL/Rocky/Alma, Fedora, openSUSE/SLES
#
set -euo pipefail

PREFIX_BIN="/usr/local/bin"
PREFIX_SHARE="/usr/local/share/nebulacb"
CONFIG_DIR="/etc/nebulacb"
LIB_DIR="/var/lib/nebulacb"
LOG_DIR="/var/log/nebulacb"
SYSTEMD_UNIT="/etc/systemd/system/nebulacb.service"
SERVICE_USER="nebulacb"
SERVICE_GROUP="nebulacb"

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BINARY="${REPO_ROOT}/bin/nebulacb"
UI_BUILD="${REPO_ROOT}/web/nebulacb-ui/build"
UNIT_FILE="${REPO_ROOT}/deploy/systemd/nebulacb.service"
SAMPLE_CONFIG="${REPO_ROOT}/deploy/install/config.sample.json"
SOURCE_CONFIG="${SOURCE_CONFIG:-}"

log() { echo -e "\033[1;36m[install]\033[0m $*"; }
warn() { echo -e "\033[1;33m[warn]\033[0m $*" >&2; }
err() { echo -e "\033[1;31m[error]\033[0m $*" >&2; exit 1; }

require_root() {
  if [[ $EUID -ne 0 ]]; then
    err "must run as root (use sudo)"
  fi
}

detect_distro() {
  if [[ -f /etc/os-release ]]; then
    . /etc/os-release
    echo "${ID:-unknown}"
  else
    echo "unknown"
  fi
}

create_user() {
  if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    log "user ${SERVICE_USER} already exists"
    return
  fi
  log "creating system user ${SERVICE_USER}"
  case "$(detect_distro)" in
    ubuntu|debian)
      adduser --system --group --no-create-home --home "${LIB_DIR}" --shell /usr/sbin/nologin "${SERVICE_USER}"
      ;;
    centos|rhel|rocky|almalinux|fedora)
      useradd --system --user-group --home-dir "${LIB_DIR}" --shell /sbin/nologin "${SERVICE_USER}"
      ;;
    opensuse*|sles|suse)
      useradd --system --user-group --home-dir "${LIB_DIR}" --shell /sbin/nologin "${SERVICE_USER}"
      ;;
    *)
      useradd --system --user-group --home-dir "${LIB_DIR}" --shell /bin/false "${SERVICE_USER}" || \
        adduser --system --group --no-create-home --home "${LIB_DIR}" --shell /bin/false "${SERVICE_USER}"
      ;;
  esac
}

install_files() {
  [[ -x "${BINARY}" ]] || err "binary not found at ${BINARY} (run 'make build' first)"
  [[ -d "${UI_BUILD}" ]] || warn "UI build not found at ${UI_BUILD} — UI will be unavailable"
  [[ -f "${UNIT_FILE}" ]] || err "systemd unit not found at ${UNIT_FILE}"

  log "installing binary -> ${PREFIX_BIN}/nebulacb"
  install -D -m 0755 "${BINARY}" "${PREFIX_BIN}/nebulacb"

  log "installing UI -> ${PREFIX_SHARE}/web/nebulacb-ui/build"
  if [[ -d "${UI_BUILD}" ]]; then
    mkdir -p "${PREFIX_SHARE}/web/nebulacb-ui"
    rm -rf "${PREFIX_SHARE}/web/nebulacb-ui/build"
    cp -a "${UI_BUILD}" "${PREFIX_SHARE}/web/nebulacb-ui/build"
  fi

  log "installing systemd unit -> ${SYSTEMD_UNIT}"
  install -D -m 0644 "${UNIT_FILE}" "${SYSTEMD_UNIT}"

  log "creating directories ${CONFIG_DIR} ${LIB_DIR} ${LOG_DIR}"
  install -d -m 0755 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${LIB_DIR}" "${LOG_DIR}"
  install -d -m 0755 "${CONFIG_DIR}"

  if [[ ! -f "${CONFIG_DIR}/config.json" ]]; then
    if [[ -n "${SOURCE_CONFIG}" && -f "${SOURCE_CONFIG}" ]]; then
      log "seeding config from ${SOURCE_CONFIG}"
      install -m 0640 -o root -g "${SERVICE_GROUP}" "${SOURCE_CONFIG}" "${CONFIG_DIR}/config.json"
    elif [[ -f "${SAMPLE_CONFIG}" ]]; then
      log "seeding config from sample"
      install -m 0640 -o root -g "${SERVICE_GROUP}" "${SAMPLE_CONFIG}" "${CONFIG_DIR}/config.json"
    else
      warn "no source config found — service may fail to start"
    fi
  else
    log "config ${CONFIG_DIR}/config.json already exists — leaving untouched"
  fi
}

enable_service() {
  log "reloading systemd"
  systemctl daemon-reload

  log "enabling nebulacb.service"
  systemctl enable nebulacb.service

  log "starting nebulacb.service"
  systemctl restart nebulacb.service

  sleep 2
  if systemctl is-active --quiet nebulacb.service; then
    log "nebulacb is running ✓"
    systemctl status nebulacb.service --no-pager -l | head -15 || true
  else
    warn "nebulacb failed to start — see 'journalctl -u nebulacb -n 80'"
    systemctl status nebulacb.service --no-pager -l | head -25 || true
    exit 1
  fi
}

main() {
  require_root
  log "distro: $(detect_distro)"
  create_user
  install_files
  enable_service
  log "done. Try: curl http://localhost:8899/api/v1/health"
}

main "$@"
