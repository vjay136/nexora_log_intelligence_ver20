#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fail() { printf 'nli-aws-cloudflare-controller: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
Usage:
  sudo bash install-controller-aws-cloudflare-ver20.sh \
    --rpm /path/to/nli_controller_agent_ver20.rpm \
    --private-endpoint 10.20.1.10 \
    --public-hostname nli-controller.example.com \
    --tunnel-token-file /root/cloudflare-tunnel-token.txt

The Cloudflare dashboard published-application route must already map the
public hostname to https://127.0.0.1:12382. For the generated NLI private
certificate, enable No TLS Verify only on that loopback origin route.
EOF
}

rpm_path= private_endpoint= public_hostname= tunnel_token_file=
while (($#)); do
  case "$1" in
    --rpm) (($# >= 2)) || fail 'missing value for --rpm'; rpm_path=$2; shift 2 ;;
    --private-endpoint) (($# >= 2)) || fail 'missing value for --private-endpoint'; private_endpoint=$2; shift 2 ;;
    --public-hostname) (($# >= 2)) || fail 'missing value for --public-hostname'; public_hostname=$2; shift 2 ;;
    --tunnel-token-file) (($# >= 2)) || fail 'missing value for --tunnel-token-file'; tunnel_token_file=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
done

[[ -n $rpm_path && -n $private_endpoint && -n $public_hostname && -n $tunnel_token_file ]] || { usage >&2; exit 1; }
[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run with sudo/root'

bash "$SCRIPT_DIR/install-controller-aws-ver20.sh" "$rpm_path" "$private_endpoint"
bash "$SCRIPT_DIR/setup-cloudflare-tunnel-v20.sh" --hostname "$public_hostname" --token-file "$tunnel_token_file"
bash "$SCRIPT_DIR/verify-cloudflare-tunnel-v20.sh"

printf '\nAWS private controller and Cloudflare public route are healthy.\n'
printf 'Set this server-only Vercel variable, then redeploy:\n'
printf 'NLI_CONTROLLER_ORIGIN=https://%s\n' "$public_hostname"
