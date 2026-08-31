#!/bin/sh
# nebulacb postinstall — enable + start service
set -e

if [ -f /etc/nebulacb/config.json ]; then
  chown root:nebulacb /etc/nebulacb/config.json || true
  chmod 0640 /etc/nebulacb/config.json || true
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  systemctl enable nebulacb.service || true
  if systemctl is-active --quiet nebulacb.service; then
    systemctl restart nebulacb.service || true
  else
    systemctl start nebulacb.service || true
  fi
fi

echo "NebulaCB installed. Edit /etc/nebulacb/config.json then: systemctl restart nebulacb"
exit 0
