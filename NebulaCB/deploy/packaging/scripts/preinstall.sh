#!/bin/sh
# nebulacb preinstall — create system user
set -e

if ! id -u nebulacb >/dev/null 2>&1; then
  if command -v adduser >/dev/null 2>&1 && adduser --help 2>&1 | grep -q -- '--system'; then
    adduser --system --group --no-create-home --home /var/lib/nebulacb --shell /usr/sbin/nologin nebulacb || true
  else
    useradd --system --user-group --home-dir /var/lib/nebulacb --shell /sbin/nologin nebulacb || true
  fi
fi

install -d -m 0755 -o nebulacb -g nebulacb /var/lib/nebulacb /var/log/nebulacb || true
install -d -m 0755 /etc/nebulacb || true

exit 0
