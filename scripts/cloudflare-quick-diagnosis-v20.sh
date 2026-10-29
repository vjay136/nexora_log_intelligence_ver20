#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo 'cloudflare-quick-diagnosis-v20: run with sudo/root' >&2; exit 1; }
if ! command -v cloudflared >/dev/null; then
  [[ -r /etc/redhat-release ]] || { echo 'cloudflare-quick-diagnosis-v20: this helper supports RHEL-family systems only' >&2; exit 1; }
  command -v dnf >/dev/null || command -v yum >/dev/null || { echo 'dnf or yum is required' >&2; exit 1; }
  install -d -m 0755 /etc/yum.repos.d
  curl --fail --silent --show-error --location https://pkg.cloudflare.com/cloudflared.repo -o /etc/yum.repos.d/cloudflared.repo
  if command -v dnf >/dev/null; then dnf -y install cloudflared; else yum -y install cloudflared; fi
fi
command -v cloudflared >/dev/null || { echo 'cloudflare-quick-diagnosis-v20: Cloudflare package installation failed' >&2; exit 1; }
curl --fail --silent --show-error --connect-timeout 5 --max-time 10 --insecure https://127.0.0.1:12382/api/v1/health -o /dev/null || { echo 'cloudflare-quick-diagnosis-v20: controller loopback health failed' >&2; exit 1; }
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
# Isolate HOME so a named tunnel config cannot accidentally affect a Quick Tunnel.
echo 'Starting a temporary public diagnosis URL. Anyone holding that random URL may reach the controller; stop with Ctrl+C.'
HOME="$scratch" cloudflared tunnel --url https://127.0.0.1:12382 --no-tls-verify
