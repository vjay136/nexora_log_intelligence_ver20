#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fail() { printf 'nli-aws-controller: %s\n' "$*" >&2; exit 1; }
[[ $# -ge 1 && $# -le 2 ]] || fail 'usage: sudo bash install-controller-aws-ver20.sh /path/to/nli_controller_agent_ver20.rpm [private-ip-or-hostname]'
rpm_path=$1
endpoint=${2:-}
[[ -f $rpm_path ]] || fail "RPM not found: $rpm_path"
"$SCRIPT_DIR/aws-rhel-preflight-ver20.sh"
if [[ -z $endpoint ]]; then
  endpoint=$(ip -o -4 addr show scope global | awk 'NR==1 {sub("/.*", "", $4); print $4}')
fi
[[ -n $endpoint && $endpoint != *://* && $endpoint != *:* && $endpoint != */* ]] || fail 'provide a private IP address or DNS hostname without a scheme, port, or path'
if [[ $endpoint =~ ^[0-9.]+$ ]]; then
  python3 - "$endpoint" <<'PY' || fail 'controller endpoint is not a valid private IPv4 address'
import ipaddress, sys
value = ipaddress.ip_address(sys.argv[1])
raise SystemExit(0 if value.version == 4 and value.is_private else 1)
PY
else
  [[ $endpoint =~ ^[A-Za-z0-9.-]+$ && $endpoint == *.* ]] || fail 'controller hostname must be a private DNS name (FQDN)'
fi
password_file=/root/nli-initial-admin-password.txt
if [[ ! -s /var/lib/nli-controller/users.json && ! -s $password_file ]]; then
  umask 077
  openssl rand -base64 24 | tr -d '\r\n' >"$password_file"
  chmod 0600 "$password_file"
fi
export NLI_SETUP_ENDPOINT="$endpoint" NLI_SETUP_TLS_IDENTITIES="$endpoint"
export NLI_SETUP_STORAGE_PROVIDER=local NLI_SETUP_ADMIN_PASSWORD_FILE="$password_file"
bash "$SCRIPT_DIR/install-controller-ver20.sh" --mode 7 --rpm "$rpm_path"
printf '\nController endpoint: https://%s:12382\n' "$endpoint"
printf 'Private CA bootstrap: /root/nli-client-bootstrap.env (copy securely to each client)\n'
printf 'Use the AWS security group plan in docs/V20_AWS_RED_HAT_INSTALLATION.md; do not expose 12382 publicly.\n'
