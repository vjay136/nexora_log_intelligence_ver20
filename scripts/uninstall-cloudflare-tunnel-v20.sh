#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo 'uninstall-cloudflare-tunnel-v20: run with sudo/root' >&2; exit 1; }
systemctl disable --now cloudflared.service 2>/dev/null || true
rm -f /etc/systemd/system/cloudflared.service
systemctl daemon-reload
systemctl reset-failed cloudflared.service 2>/dev/null || true
rm -rf /etc/cloudflared /var/lib/cloudflared
echo 'uninstall-cloudflare-tunnel-v20: local cloudflared service/configuration removed; cloudflared RPM and Cloudflare dashboard tunnel were left in place'
