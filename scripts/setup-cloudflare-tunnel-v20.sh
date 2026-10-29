#!/usr/bin/env bash
set -euo pipefail

# Configures a remotely-managed named tunnel. The dashboard owns the public
# hostname/origin route; this host stores only its connector token in a root
# protected file and passes it to cloudflared via --token-file.
PREFIX=setup-cloudflare-tunnel-v20
fail() { printf '%s: %s\n' "$PREFIX" "$*" >&2; exit 1; }
info() { printf '%s: %s\n' "$PREFIX" "$*"; }
usage() {
  cat <<'EOF'
Usage: sudo bash setup-cloudflare-tunnel-v20.sh --hostname FQDN --token-file PATH

Install Cloudflare's official cloudflared RPM, protect the remotely-managed
tunnel connector token, and enable cloudflared.service. Configure the published
hostname/origin in the Cloudflare dashboard before running this script.
EOF
}
hostname= token_file=
while (($#)); do
  case "$1" in
    --hostname) (($# >= 2)) || fail 'missing value for --hostname'; hostname=$2; shift 2 ;;
    --token-file) (($# >= 2)) || fail 'missing value for --token-file'; token_file=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option $1" ;;
  esac
done
[[ $hostname =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ && $hostname != *..* ]] || fail 'hostname must be a public DNS name'
[[ -n $token_file && -f $token_file && -s $token_file ]] || fail 'Cloudflare tunnel token file is required'
[[ $EUID -eq 0 ]] || fail 'run with sudo/root'
command -v systemctl >/dev/null || fail 'systemd is required'
command -v dnf >/dev/null || command -v yum >/dev/null || fail 'dnf or yum is required'
[[ -r /etc/redhat-release ]] || fail 'this installer supports RHEL, Rocky Linux, and compatible Red Hat systems only'
grep -Eiq 'Red Hat|Rocky|AlmaLinux|CentOS' /etc/redhat-release || fail 'unsupported Red Hat family distribution'
command -v curl >/dev/null || fail 'curl is required to configure Cloudflare official RPM repository'

install -d -o root -g root -m 0755 /etc/yum.repos.d
repo_tmp=$(mktemp)
trap 'rm -f "$repo_tmp"' EXIT
curl --fail --silent --show-error --location https://pkg.cloudflare.com/cloudflared.repo -o "$repo_tmp" || fail 'could not download Cloudflare official RPM repository definition'
grep -q 'pkg.cloudflare.com' "$repo_tmp" || fail 'downloaded Cloudflare repository definition did not validate'
install -o root -g root -m 0644 "$repo_tmp" /etc/yum.repos.d/cloudflared.repo
if command -v dnf >/dev/null; then dnf -y install cloudflared; else yum -y install cloudflared; fi
command -v cloudflared >/dev/null || fail 'cloudflared package installation failed'

# --token-file requires cloudflared 2025.4.0+. Validate before installing a
# service that would otherwise repeatedly fail and avoid logging token content.
version=$(cloudflared --version 2>/dev/null | awk '{print $3}' | sed 's/^v//')
[[ $version =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] || fail 'could not determine cloudflared version'
(( ${BASH_REMATCH[1]} > 2025 || (${BASH_REMATCH[1]} == 2025 && ${BASH_REMATCH[2]} > 4) || (${BASH_REMATCH[1]} == 2025 && ${BASH_REMATCH[2]} == 4 && ${BASH_REMATCH[3]} >= 0) )) || fail 'cloudflared 2025.4.0 or newer is required for secure --token-file operation'

getent group cloudflared >/dev/null || groupadd --system cloudflared
id cloudflared >/dev/null 2>&1 || useradd --system --gid cloudflared --home-dir /var/lib/cloudflared --shell /sbin/nologin cloudflared
install -d -o root -g cloudflared -m 0750 /etc/cloudflared
install -d -o cloudflared -g cloudflared -m 0750 /var/lib/cloudflared
install -o root -g cloudflared -m 0640 "$token_file" /etc/cloudflared/tunnel-token
printf 'hostname=%s\norigin=https://127.0.0.1:12382\n' "$hostname" >/etc/cloudflared/deployment.conf
chown root:cloudflared /etc/cloudflared/deployment.conf
chmod 0640 /etc/cloudflared/deployment.conf

cat >/etc/systemd/system/cloudflared.service <<'EOF'
[Unit]
Description=Cloudflare Tunnel for NLI controller
Wants=network-online.target
After=network-online.target nli-controller-agent.service

[Service]
Type=simple
User=cloudflared
Group=cloudflared
ExecStart=/usr/bin/cloudflared tunnel --no-autoupdate run --token-file /etc/cloudflared/tunnel-token
Restart=on-failure
RestartSec=5s
UMask=0027
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/var/lib/cloudflared

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 /etc/systemd/system/cloudflared.service
systemctl daemon-reload
systemctl enable cloudflared.service
if systemctl is-active --quiet cloudflared.service; then
  systemctl restart cloudflared.service
else
  systemctl start cloudflared.service
fi
info "named tunnel connector configured for $hostname; token was not printed or placed in the service unit"
info 'run sudo bash scripts/verify-cloudflare-tunnel-v20.sh'
