#!/usr/bin/env bash
set -euo pipefail

fail() { printf 'nli-aws-preflight: %s\n' "$*" >&2; exit 1; }
[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run as root'
[[ -r /etc/os-release ]] || fail 'cannot identify Linux distribution'
# shellcheck disable=SC1091
source /etc/os-release
[[ ${ID:-} == rocky || ${ID:-} == rhel || ${ID_LIKE:-} == *rhel* ]] || fail "unsupported distribution: ${ID:-unknown}; use RHEL or Rocky Linux"
major=${VERSION_ID%%.*}
[[ $major =~ ^(8|9|10)$ ]] || fail "supported releases are RHEL/Rocky 8, 9, and 10 (found ${VERSION_ID:-unknown})"
for command in rpm dnf systemctl python3 curl openssl; do command -v "$command" >/dev/null || fail "$command is required"; done
[[ $(uname -m) == x86_64 ]] || fail 'only x86_64 RPMs are currently published'
if systemctl is-enabled --quiet nli-controller-agent.service 2>/dev/null; then
  systemctl is-active --quiet nli-controller-agent.service || fail 'controller unit is enabled but inactive; inspect journalctl -u nli-controller-agent'
fi
if command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
  printf 'firewalld active; AWS security groups remain the source restriction for TCP 12382.\n'
else
  printf 'firewalld inactive or absent; use the provided AWS security group policy.\n'
fi
printf 'PASS: %s %s (%s), required tools and RPM architecture are ready\n' "${ID}" "${VERSION_ID}" "$(uname -m)"
