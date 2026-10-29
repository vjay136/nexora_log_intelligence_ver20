#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Uninstall the NLI v20 controller.

Usage:
  sudo bash uninstall-controller-ver20.sh [--yes] [--purge]

The default removal preserves controller configuration, users, TLS material,
and state for recovery or reinstall. --purge permanently removes those local
files after a second explicit confirmation. Supabase cloud data is never
deleted by this script.
EOF
}

fail() { printf 'uninstall-controller-ver20: %s\n' "$*" >&2; exit 1; }
info() { printf 'uninstall-controller-ver20: %s\n' "$*"; }

assume_yes=0
purge=0
purge_confirmed=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes) assume_yes=1 ;;
    --purge) purge=1 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unexpected argument: $1" ;;
  esac
  shift
done

[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run as root with sudo'
for command in dnf rpm systemctl; do command -v "$command" >/dev/null || fail "$command is required"; done

if [[ $assume_yes -ne 1 ]]; then
  [[ -t 0 ]] || fail 'interactive confirmation is unavailable; rerun with --yes'
  read -r -p 'Stop services and uninstall nli-controller-agent? [y/N]: ' answer
  [[ ${answer,,} == y || ${answer,,} == yes ]] || { info 'cancelled'; exit 0; }
fi

if [[ $purge -eq 1 ]]; then
  if [[ $assume_yes -eq 1 ]]; then
    purge_confirmed=1
  else
    read -r -p 'Type PURGE-CONTROLLER to delete local users, TLS, secrets, and state: ' confirmation
    [[ $confirmation == PURGE-CONTROLLER ]] || fail 'purge confirmation did not match; nothing was uninstalled'
    purge_confirmed=1
  fi
fi

restore_nli_selinux_boolean_on_purge() {
  local values changed prior_current prior_persistent current state persistent
  [[ $purge_confirmed == 1 && -f /etc/nli/controller-edge.json ]] || return 0
  values=$(python3 - /etc/nli/controller-edge.json <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
changed = value.get("selinux_proxy_boolean_changed") is True
current = value.get("selinux_proxy_boolean_prior_current", "")
persistent = value.get("selinux_proxy_boolean_prior_persistent", "")
if changed and (current not in ("on", "off") or persistent not in ("on", "off")):
    raise SystemExit("controller edge manifest has an invalid original SELinux boolean state")
print(f"{str(changed).lower()} {current} {persistent}")
PY
) || fail 'could not read the NLI SELinux state before purge'
  read -r changed prior_current prior_persistent <<<"$values"
  [[ $changed == true ]] || return 0
  command -v getenforce >/dev/null && command -v getsebool >/dev/null &&
    command -v semanage >/dev/null && command -v setsebool >/dev/null ||
    fail 'cannot safely restore the NLI-managed SELinux boolean; install policycoreutils-python-utils and retry purge'
  [[ $(getenforce) != Disabled ]] || { info 'SELinux is disabled; no boolean restore is needed'; return 0; }
  current=$(getsebool httpd_can_network_connect | awk '$1=="httpd_can_network_connect" && $2=="-->" {print $3}')
  state=$(semanage boolean -l -n | python3 -c 'import re,sys; rows=[re.match(r"^\s*httpd_can_network_connect\s+\((on|off)\s*,\s*(on|off)\)", line) for line in sys.stdin]; row=next((m for m in rows if m), None); print(" ".join(row.groups()) if row else "")')
  read -r _ persistent <<<"$state"
  if [[ $current != on || $persistent != on ]]; then
    info 'preserving httpd_can_network_connect because its state changed after NLI setup'
    return 0
  fi
  setsebool -P httpd_can_network_connect "$prior_persistent" || fail 'could not restore the persistent SELinux boolean during purge; the manifest is preserved for retry'
  setsebool httpd_can_network_connect "$prior_current" || fail 'could not restore the runtime SELinux boolean during purge; the manifest is preserved for retry'
  info "restored NLI's original SELinux httpd_can_network_connect state ($prior_current runtime, $prior_persistent persistent)"
}

systemctl disable --now nli-controller-bootstrap.service nli-controller-agent.service >/dev/null 2>&1 || true
systemctl disable --now nli-cloudflared.service nli-cloudflared-quick.service nli-certbot-renew.timer >/dev/null 2>&1 || true

remove_managed_site() {
  local site=$1 validator=$2 service=$3 backup
  [[ -f $site ]] || return 0
  backup=$(mktemp)
  cp -a "$site" "$backup"
  rm -f -- "$site"
  if systemctl is-active --quiet "$service"; then
    if ! "$validator" -t >/dev/null 2>&1 || ! systemctl reload "$service"; then
      cp -a "$backup" "$site"
      "$validator" -t >/dev/null 2>&1 && systemctl reload "$service" >/dev/null 2>&1 || true
      rm -f -- "$backup"
      fail "could not safely remove the NLI-owned proxy site $site; the previous site was restored"
    fi
  fi
  rm -f -- "$backup"
}

remove_managed_site /etc/nginx/conf.d/nli-controller.conf nginx nginx.service
remove_managed_site /etc/httpd/conf.d/nli-controller.conf httpd httpd.service
rm -f -- /etc/systemd/system/nli-cloudflared.service /etc/systemd/system/nli-cloudflared-quick.service
rm -f -- /etc/systemd/system/nli-certbot-renew.service /etc/systemd/system/nli-certbot-renew.timer
rm -f -- /etc/nli/cloudflare/tunnel-token
systemctl daemon-reload
restore_nli_selinux_boolean_on_purge

if rpm -q nli-controller-agent >/dev/null 2>&1; then
  dnf -y remove nli-controller-agent
else
  info 'nli-controller-agent is not installed'
fi
systemctl daemon-reload
systemctl reset-failed nli-controller-bootstrap.service nli-controller-agent.service >/dev/null 2>&1 || true

if [[ $purge -eq 1 ]]; then
  rm -f -- /etc/nli/controller.json /etc/nli/controller.json.rpmsave /etc/nli/controller.json.rpmnew
  rm -f -- /etc/nli/controller.env /etc/nli/controller.env.rpmsave /etc/nli/controller.env.rpmnew
  rm -f -- /etc/nli/controller-edge.json
  rm -f -- /etc/nli/controller-bootstrap.env
  rm -f -- /root/nli-client-bootstrap.env /root/nli-controller-credentials.env \
    /root/nli-initial-admin-password.txt /root/nli-supabase-bootstrap.sql
  rm -rf -- /var/lib/nli-controller
  info 'local controller configuration and state were purged; Supabase cloud data was not changed'
else
  info 'controller configuration and state were preserved for reinstall/recovery'
fi
info 'PASS: controller package is not installed'
