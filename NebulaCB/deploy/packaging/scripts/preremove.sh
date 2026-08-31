#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop nebulacb.service || true
  systemctl disable nebulacb.service || true
fi
exit 0
