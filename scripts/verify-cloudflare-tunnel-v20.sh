#!/usr/bin/env bash
set -euo pipefail
PREFIX=verify-cloudflare-tunnel-v20
fail() { printf '%s: FAIL: %s\n' "$PREFIX" "$*" >&2; exit 1; }
pass() { printf '%s: PASS: %s\n' "$PREFIX" "$*"; }
[[ $EUID -eq 0 ]] || { echo "$PREFIX: run with sudo/root" >&2; exit 1; }
command -v cloudflared >/dev/null || fail 'cloudflared is not installed'
systemctl is-active --quiet cloudflared.service || fail 'cloudflared.service is not active'
pass 'systemd cloudflared.service is active'
systemctl is-active --quiet nli-controller-agent.service || fail 'nli-controller-agent.service is not active'
pass 'nli-controller-agent.service is active'
curl --fail --silent --show-error --connect-timeout 5 --max-time 10 --insecure https://127.0.0.1:12382/api/v1/health -o /dev/null || fail 'loopback controller HTTPS health endpoint failed'
pass 'controller /api/v1/health responds over loopback HTTPS'
if [[ -r /etc/cloudflared/deployment.conf ]]; then
  hostname=$(sed -n 's/^hostname=//p' /etc/cloudflared/deployment.conf)
  [[ $hostname =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || fail 'saved public hostname is missing or invalid'
  curl --fail --silent --show-error --connect-timeout 10 --max-time 20 "https://${hostname}/api/v1/health" -o /dev/null || fail 'public hostname /api/v1/health failed; check dashboard route, DNS, and tunnel connection'
  pass 'public hostname /api/v1/health responds'
else
  fail 'cloudflared deployment metadata is missing; run setup first'
fi
if ! journalctl -u cloudflared.service -n 100 --no-pager 2>/dev/null | grep -Eqi 'Registered tunnel connection|Connection .*registered'; then
  fail 'no registered Cloudflare edge connection found in recent service journal'
fi
pass 'outbound tunnel connection is registered with Cloudflare'
