#!/usr/bin/env bash
#
# NebulaCB uninstaller
#
set -euo pipefail

PREFIX_BIN="/usr/local/bin"
PREFIX_SHARE="/usr/local/share/nebulacb"
CONFIG_DIR="/etc/nebulacb"
LIB_DIR="/var/lib/nebulacb"
LOG_DIR="/var/log/nebulacb"
SYSTEMD_UNIT="/etc/systemd/system/nebulacb.service"
SERVICE_USER="nebulacb"

log() { echo -e "\033[1;36m[uninstall]\033[0m $*"; }

if [[ $EUID -ne 0 ]]; then
  echo "must run as root (use sudo)" >&2
  exit 1
fi

PURGE="${1:-}"

if systemctl list-unit-files | grep -q '^nebulacb.service'; then
  log "stopping nebulacb.service"
  systemctl stop nebulacb.service || true
  log "disabling nebulacb.service"
  systemctl disable nebulacb.service || true
fi

[[ -f "${SYSTEMD_UNIT}" ]] && { log "removing ${SYSTEMD_UNIT}"; rm -f "${SYSTEMD_UNIT}"; }
systemctl daemon-reload || true

[[ -x "${PREFIX_BIN}/nebulacb" ]] && { log "removing ${PREFIX_BIN}/nebulacb"; rm -f "${PREFIX_BIN}/nebulacb"; }
[[ -d "${PREFIX_SHARE}" ]] && { log "removing ${PREFIX_SHARE}"; rm -rf "${PREFIX_SHARE}"; }

if [[ "${PURGE}" == "--purge" ]]; then
  log "purging ${CONFIG_DIR} ${LIB_DIR} ${LOG_DIR}"
  rm -rf "${CONFIG_DIR}" "${LIB_DIR}" "${LOG_DIR}"
  if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    log "removing user ${SERVICE_USER}"
    userdel "${SERVICE_USER}" 2>/dev/null || true
  fi
else
  log "preserving ${CONFIG_DIR} ${LIB_DIR} ${LOG_DIR} (use --purge to remove)"
fi

log "done"
