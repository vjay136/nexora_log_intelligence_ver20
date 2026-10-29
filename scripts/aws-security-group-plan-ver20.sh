#!/usr/bin/env bash
set -euo pipefail
fail() { printf 'aws-security-group-plan: %s\n' "$*" >&2; exit 1; }
[[ $# -eq 3 ]] || fail 'usage: bash aws-security-group-plan-ver20.sh <controller-sg-id> <client-sg-id> <admin-source-cidr>'
controller_sg=$1 client_sg=$2 admin_cidr=$3
[[ $controller_sg =~ ^sg-[0-9a-fA-F]{8,17}$ && $client_sg =~ ^sg-[0-9a-fA-F]{8,17}$ ]] || fail 'security group IDs must be valid sg- identifiers'
[[ $controller_sg != "$client_sg" ]] || fail 'controller and client security groups must be distinct'
python3 - "$admin_cidr" <<'PY' || fail 'admin source must be a valid IPv4 CIDR; avoid 0.0.0.0/0'
import ipaddress, sys
value = ipaddress.ip_network(sys.argv[1], strict=False)
raise SystemExit(0 if value.version == 4 and str(value) != "0.0.0.0/0" else 1)
PY
cat <<EOF
# Review, then execute with AWS CLI credentials scoped to these security groups.
aws ec2 authorize-security-group-ingress --group-id $controller_sg --ip-permissions '[{"IpProtocol":"tcp","FromPort":22,"ToPort":22,"IpRanges":[{"CidrIp":"$admin_cidr","Description":"SSH from approved admin source"}]}]'
aws ec2 authorize-security-group-ingress --group-id $controller_sg --ip-permissions '[{"IpProtocol":"tcp","FromPort":12382,"ToPort":12382,"UserIdGroupPairs":[{"GroupId":"$client_sg","Description":"NLI clients to private controller TLS"}]}]'
aws ec2 authorize-security-group-ingress --group-id $client_sg --ip-permissions '[{"IpProtocol":"tcp","FromPort":22,"ToPort":22,"IpRanges":[{"CidrIp":"$admin_cidr","Description":"SSH from approved admin source"}]}]'
# This policy intentionally creates no 12382 rule with a public CIDR.
# For an outbound-only Cloudflare Tunnel, allow controller egress TCP 443 and 7844
# to Cloudflare's current published IP ranges using managed prefix lists or egress
# proxy; never add public inbound 12382 for the tunnel path.
EOF
