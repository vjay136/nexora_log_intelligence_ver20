#!/usr/bin/env bash
set -euo pipefail

CONFIG=${NLI_CONTROLLER_CONFIG:-/etc/nli/controller.json}
ENV_FILE=${NLI_CONTROLLER_ENV_FILE:-/etc/nli/controller.env}
STATE_DIR=${NLI_CONTROLLER_STATE_DIR:-/var/lib/nli-controller}
SERVICE=${NLI_CONTROLLER_SERVICE:-nli-controller-agent.service}
BOOTSTRAP_FILE=${NLI_CLIENT_BOOTSTRAP_FILE:-/root/nli-client-bootstrap.env}
SUPABASE_SCHEMA_TEMPLATE=${NLI_SUPABASE_SCHEMA_TEMPLATE:-/usr/share/nli/supabase/nli_schema.sql}
SUPABASE_BOOTSTRAP_FILE=${NLI_SUPABASE_BOOTSTRAP_FILE:-/root/nli-supabase-bootstrap.sql}
PORT=${NLI_CONTROLLER_PORT:-12382}
BOOTSTRAP_PASSWORD_FILE=${NLI_BOOTSTRAP_ADMIN_PASSWORD_FILE:-$STATE_DIR/bootstrap-admin-password}
CREDENTIAL_FILE=${NLI_CONTROLLER_CREDENTIAL_FILE:-/root/nli-controller-credentials.env}
rotate_enrollment=0
if [[ ${1:-} == --rotate-enrollment ]]; then
  rotate_enrollment=1
  shift
fi
[[ $# -eq 0 ]] || { printf 'usage: sudo nli-controller-setup [--rotate-enrollment]\n' >&2; exit 2; }

fail() { printf 'nli-controller-setup: %s\n' "$*" >&2; exit 1; }
info() { printf 'nli-controller-setup: %s\n' "$*"; }
[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run as root: sudo nli-controller-setup'
for command in python3 curl systemctl openssl base64 hostname sha256sum ss; do command -v "$command" >/dev/null || fail "$command is required"; done
[[ -f $CONFIG && -f $ENV_FILE ]] || fail 'install the controller RPM before running setup'
[[ -f $SUPABASE_SCHEMA_TEMPLATE ]] || fail "Supabase schema template is missing: $SUPABASE_SCHEMA_TEMPLATE"

read_env() {
  local key=$1 value
  value=$(sed -n "s/^${key}=//p" "$ENV_FILE" | tail -n 1)
  if [[ -z $value && -s $CREDENTIAL_FILE ]]; then
    value=$(sed -n "s/^${key}=//p" "$CREDENTIAL_FILE" | tail -n 1)
  fi
  printf '%s\n' "$value"
}
valid_env_secret() {
  [[ $1 =~ ^[A-Za-z0-9._~!@#%^+=,:/-]+$ ]]
}
random_secret() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}
existing_or_random_secret() {
  local env_name=$1 state_name=$2 value
  value=$(read_env "$env_name")
  if [[ -z $value && -s $STATE_DIR/$state_name ]]; then
    IFS= read -r value <"$STATE_DIR/$state_name" || true
  fi
  printf '%s\n' "${value:-$(random_secret)}"
}

# Collect identities without making cloud metadata a dependency. IMDSv2 is
# queried only through the link-local AWS endpoint and every request is bounded.
discover_host_identities() {
  local value token=''
  {
    hostname 2>/dev/null || true
    hostname -s 2>/dev/null || true
    hostname -f 2>/dev/null || true
    hostname -I 2>/dev/null | tr ' ' '\n' || true
    if token=$(curl -fsS --connect-timeout 1 --max-time 2 -X PUT \
      -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' \
      http://169.254.169.254/latest/api/token 2>/dev/null); then
      for value in local-ipv4 public-ipv4 local-hostname public-hostname; do
        curl -fsS --connect-timeout 1 --max-time 2 \
          -H "X-aws-ec2-metadata-token: $token" \
          "http://169.254.169.254/latest/meta-data/$value" 2>/dev/null || true
        printf '\n'
      done
    fi
  } | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//' | sed '/^$/d' | awk '!seen[tolower($0)]++'
}

first_discovered_identity() {
  local value fallback=''
  while IFS= read -r value; do
    [[ $value == localhost || $value == localhost.localdomain || $value == 127.* || $value == ::1 ]] && continue
    [[ -n $fallback ]] || fallback=$value
    [[ $value == *.* && ! $value =~ ^[0-9.]+$ ]] && { printf '%s\n' "$value"; return 0; }
  done < <(discover_host_identities)
  printf '%s\n' "$fallback"
}

storage_provider=${NLI_SETUP_STORAGE_PROVIDER:-supabase}
[[ $storage_provider == supabase || $storage_provider == local ]] || fail 'NLI_SETUP_STORAGE_PROVIDER must be supabase or local'
supabase_url=''
supabase_public_key=''
supabase_controller_secret=''
supabase_storage_secret=''
storage_allow_insecure=false
assurance_key=$(existing_or_random_secret NLI_ASSURANCE_ENCRYPTION_KEY assurance.key)
audit_key=$(existing_or_random_secret NLI_AUDIT_HMAC_KEY audit.key)
csrf_key=$(existing_or_random_secret NLI_CSRF_HMAC_KEY csrf.key)
artifact_public_keys=$(read_env NLI_ARTIFACT_ED25519_PUBLIC_KEYS)
for secret_spec in "audit HMAC:$audit_key" "CSRF HMAC:$csrf_key" "Assurance encryption:$assurance_key"; do
  secret_label=${secret_spec%%:*}
  secret_value=${secret_spec#*:}
  valid_env_secret "$secret_value" || fail "$secret_label key is not safe for a systemd EnvironmentFile"
done

printf '\nNLI v20 controller first-run setup\n'
printf '  1) Local administration through SSH tunnel (secure default)\n'
printf '  2) Private-network TLS for separate controller/client nodes\n\n'
printf '  3) Public DNS + Nginx + ACME certificate (automated)\n'
printf '  4) Cloudflare Tunnel token + public HTTPS hostname (automated)\n'
printf '  5) Operator-managed reverse proxy with public HTTPS\n\n'
printf '  6) Corporate PKI certificate + direct HTTPS (restricted networks)\n'
printf '  7) Offline private TLS (generated private CA; no public DNS, ACME, or corporate PKI required)\n\n'
printf '  8) Auto-detect a trusted server certificate/key; fall back to offline private TLS\n\n'
printf '  9) Public DNS + Apache HTTP Server + ACME certificate (automated)\n'
printf ' 10) Temporary Cloudflare Quick Tunnel (testing only; no domain/account)\n\n'
mode=${NLI_SETUP_MODE:-}
if [[ -z $mode ]]; then read -r -p 'Deployment mode [7]: ' mode; fi
mode=${mode:-7}
[[ $mode =~ ^([1-9]|10)$ ]] || fail 'deployment mode must be 1 through 10'
selected_mode=$mode
offline_private_tls=false
auto_server_tls=false
found_server_cert=''
found_server_key=''
if [[ $mode == 7 ]]; then
  # Use the same tested generated-CA implementation while making the offline
  # enterprise path explicit to operators. No public DNS or external CA is used.
  mode=2
  offline_private_tls=true
  if [[ -z ${NLI_SETUP_STORAGE_PROVIDER:-} ]]; then storage_provider=local; fi
elif [[ $mode == 8 ]]; then
  auto_server_tls=true
  if [[ -z ${NLI_SETUP_STORAGE_PROVIDER:-} ]]; then storage_provider=local; fi
fi

admin_password=''
if [[ ! -s $STATE_DIR/users.json ]]; then
  if [[ -n ${NLI_SETUP_ADMIN_PASSWORD_FILE:-} ]]; then
    [[ -f $NLI_SETUP_ADMIN_PASSWORD_FILE ]] || fail "admin password file not found: $NLI_SETUP_ADMIN_PASSWORD_FILE"
    IFS= read -r admin_password < "$NLI_SETUP_ADMIN_PASSWORD_FILE" || true
  elif [[ -n ${NLI_SETUP_ADMIN_PASSWORD:-} ]]; then
    admin_password=$NLI_SETUP_ADMIN_PASSWORD
  else
    [[ -t 0 ]] || fail 'unattended setup requires NLI_SETUP_ADMIN_PASSWORD_FILE'
    read -r -s -p 'Initial admin password (minimum 12 characters): ' admin_password
    printf '\n'
    read -r -s -p 'Confirm initial admin password: ' confirmation
    printf '\n'
    [[ $admin_password == "$confirmation" ]] || fail 'admin passwords do not match'
  fi
  [[ $(LC_ALL=C printf '%s' "$admin_password" | wc -c) -ge 12 ]] || fail 'admin password must contain at least 12 bytes'
  [[ $admin_password != *$'\n'* && $admin_password != *$'\r'* ]] || fail 'admin password cannot contain line breaks'
else
  info 'existing users.json detected; the stored admin password will be preserved'
  info 'run sudo nli-admin-reset separately if that password is unknown'
fi

if [[ $rotate_enrollment -eq 1 ]]; then
  enrollment_token=$(random_secret)
  info 'rotating to a fresh single-use enrollment token'
else
  enrollment_token=$(read_env NLI_ENROLLMENT_TOKEN)
  if [[ $(LC_ALL=C printf '%s' "$enrollment_token" | wc -c) -lt 24 ]]; then
    enrollment_token=$(random_secret)
  fi
fi
valid_env_secret "$enrollment_token" || fail 'existing NLI_ENROLLMENT_TOKEN is not safe for a systemd EnvironmentFile'

listen_address="127.0.0.1:${PORT}"
controller_url="http://127.0.0.1:${PORT}"
tls_cert=''
tls_key=''
force_secure=false
allow_insecure_remote=false
ca_base64=''
endpoint=''
added_firewall_http=0
added_firewall_https=0
added_firewall_http_permanent=0
added_firewall_https_permanent=0
selinux_proxy_managed=false
selinux_proxy_changed_this_run=0
selinux_setup_prior_current=''
selinux_setup_prior_persistent=''
previous_selinux_manifest=$(python3 - /etc/nli/controller-edge.json <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
if not path.exists():
    print("false  ")
else:
    value = json.loads(path.read_text(encoding="utf-8"))
    changed = value.get("selinux_proxy_boolean_changed") is True
    current = value.get("selinux_proxy_boolean_prior_current", "") if changed else ""
    persistent = value.get("selinux_proxy_boolean_prior_persistent", "") if changed else ""
    if current not in ("", "on", "off") or persistent not in ("", "on", "off"):
        raise SystemExit("invalid SELinux state in the existing controller edge manifest")
    if changed and (current not in ("on", "off") or persistent not in ("on", "off")):
        raise SystemExit("existing edge manifest has an incomplete SELinux state")
    print(f"{str(changed).lower()} {current} {persistent}")
PY
) || fail 'could not read the prior controller edge SELinux state'
read -r previous_selinux_changed previous_selinux_prior_current previous_selinux_prior_persistent <<<"$previous_selinux_manifest"
previous_selinux_changed=${previous_selinux_changed:-false}
previous_selinux_prior_current=${previous_selinux_prior_current:-}
previous_selinux_prior_persistent=${previous_selinux_prior_persistent:-}
read_selinux_proxy_boolean() {
  local current semanage_values
  current=$(getsebool httpd_can_network_connect | awk '$1=="httpd_can_network_connect" && $2=="-->" {print $3}')
  [[ $current == on || $current == off ]] || fail 'could not read the current httpd_can_network_connect SELinux boolean'
  semanage_values=$(semanage boolean -l -n | python3 -c 'import re,sys; rows=[re.match(r"^\s*httpd_can_network_connect\s+\((on|off)\s*,\s*(on|off)\)", line) for line in sys.stdin]; row=next((m for m in rows if m), None); print(" ".join(row.groups()) if row else "")')
  read -r semanage_current semanage_persistent <<<"$semanage_values"
  [[ $semanage_current == on || $semanage_current == off ]] || fail 'could not read the persistent httpd_can_network_connect SELinux boolean with semanage'
  [[ $semanage_current == "$current" ]] || fail 'SELinux current boolean state changed while it was being inspected'
  [[ $semanage_persistent == on || $semanage_persistent == off ]] || fail 'could not read the default httpd_can_network_connect SELinux boolean with semanage'
  printf '%s %s\n' "$current" "$semanage_persistent"
}
enable_selinux_proxy_boolean() {
  [[ $(getenforce) != Disabled ]] || return 0
  selinux_proxy_managed=true
  local state
  state=$(read_selinux_proxy_boolean)
  read -r selinux_setup_prior_current selinux_setup_prior_persistent <<<"$state"
  if [[ $selinux_setup_prior_current != on || $selinux_setup_prior_persistent != on ]]; then
    selinux_proxy_changed_this_run=1
    setsebool -P httpd_can_network_connect on || fail 'could not enable SELinux httpd_can_network_connect for the controller reverse proxy'
  fi
}
restore_selinux_proxy_boolean() {
  local state current persistent
  [[ $selinux_proxy_changed_this_run == 1 ]] || return 0
  state=$(read_selinux_proxy_boolean 2>/dev/null) || return 0
  read -r current persistent <<<"$state"
  # Do not overwrite a later administrator change made after NLI enabled it.
  [[ $current == on && $persistent == on ]] || return 0
  setsebool -P httpd_can_network_connect "$selinux_setup_prior_persistent" >/dev/null 2>&1 || return 1
  setsebool httpd_can_network_connect "$selinux_setup_prior_current" >/dev/null 2>&1 || return 1
}
rollback_edge_firewall() {
  if command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    [[ $added_firewall_http == 0 ]] || firewall-cmd --remove-service=http >/dev/null 2>&1 || true
    [[ $added_firewall_https == 0 ]] || firewall-cmd --remove-service=https >/dev/null 2>&1 || true
    [[ $added_firewall_http_permanent == 0 ]] || firewall-cmd --permanent --remove-service=http >/dev/null 2>&1 || true
    [[ $added_firewall_https_permanent == 0 ]] || firewall-cmd --permanent --remove-service=https >/dev/null 2>&1 || true
  fi
}
capture_renewal_state() {
  local path
  renewal_timer_active=0
  renewal_timer_enabled=0
  systemctl is-active --quiet nli-certbot-renew.timer && renewal_timer_active=1 || true
  systemctl is-enabled --quiet nli-certbot-renew.timer && renewal_timer_enabled=1 || true
  renewal_service_backup=$(mktemp)
  renewal_timer_backup=$(mktemp)
  renewal_service_existed=0
  renewal_timer_existed=0
  path=/etc/systemd/system/nli-certbot-renew.service
  if [[ -e $path ]]; then cp -a "$path" "$renewal_service_backup"; renewal_service_existed=1; fi
  path=/etc/systemd/system/nli-certbot-renew.timer
  if [[ -e $path ]]; then cp -a "$path" "$renewal_timer_backup"; renewal_timer_existed=1; fi
}
restore_renewal_state() {
  local service_path=/etc/systemd/system/nli-certbot-renew.service
  local timer_path=/etc/systemd/system/nli-certbot-renew.timer
  if [[ $renewal_service_existed == 1 ]]; then cp -a "$renewal_service_backup" "$service_path" || true; else rm -f "$service_path" || true; fi
  if [[ $renewal_timer_existed == 1 ]]; then cp -a "$renewal_timer_backup" "$timer_path" || true; else rm -f "$timer_path" || true; fi
  systemctl daemon-reload >/dev/null 2>&1 || true
  if [[ $renewal_timer_enabled == 1 ]]; then systemctl enable nli-certbot-renew.timer >/dev/null 2>&1 || true; else systemctl disable nli-certbot-renew.timer >/dev/null 2>&1 || true; fi
  if [[ $renewal_timer_active == 1 ]]; then systemctl start nli-certbot-renew.timer >/dev/null 2>&1 || true; else systemctl stop nli-certbot-renew.timer >/dev/null 2>&1 || true; fi
  rm -f "$renewal_service_backup" "$renewal_timer_backup" || true
}
discard_renewal_state_backup() {
  rm -f "$renewal_service_backup" "$renewal_timer_backup"
}
proxy_service_fail() {
  local service=$1 message=$2
  systemctl status "$service" --no-pager -l >&2 || true
  journalctl -u "$service" --no-pager -n 80 >&2 || true
  fail "$message"
}

find_existing_server_pair() {
  local cert_dir key_dir cert key purpose ca_flag cert_pub key_pub
  local -a cert_dirs=(/etc/pki/tls/certs /etc/nginx /etc/httpd /etc/nli)
  local -a key_dirs=(/etc/pki/tls/private /etc/nginx /etc/httpd /etc/nli)
  local -a cert_candidates=() key_candidates=() existing_dirs=()
  for cert_dir in "${cert_dirs[@]}"; do [[ -d $cert_dir ]] && existing_dirs+=("$cert_dir"); done
  ((${#existing_dirs[@]} > 0)) || return 1
  while IFS= read -r -d '' cert; do cert_candidates+=("$cert"); done < <(
    find "${existing_dirs[@]}" -type f \( -iname '*.crt' -o -iname '*.cer' -o -iname '*.cert' -o -iname '*.pem' \) -print0 2>/dev/null
  )
  existing_dirs=()
  for key_dir in "${key_dirs[@]}"; do [[ -d $key_dir ]] && existing_dirs+=("$key_dir"); done
  ((${#existing_dirs[@]} > 0)) || return 1
  while IFS= read -r -d '' key; do key_candidates+=("$key"); done < <(
    find "${existing_dirs[@]}" -type f \( -iname '*.key' -o -iname '*-key.pem' -o -iname '*.pem' \) -print0 2>/dev/null
  )
  for cert in "${cert_candidates[@]}"; do
    # Excludes Katello anchors and entitlement/client certificates: only a
    # live, non-CA leaf with the requested SAN and SSL-server purpose qualifies.
    openssl x509 -in "$cert" -noout -checkend 86400 >/dev/null 2>&1 || continue
    purpose=$(openssl x509 -in "$cert" -noout -purpose 2>/dev/null) || continue
    grep -Fxq 'SSL server : Yes' <<<"$purpose" || continue
    ca_flag=$(openssl x509 -in "$cert" -noout -text 2>/dev/null | grep -m1 -E 'CA:[[:space:]]*TRUE' || true)
    [[ -z $ca_flag ]] || continue
    if python3 - "$endpoint" <<'PY' >/dev/null 2>&1
import ipaddress, sys
ipaddress.ip_address(sys.argv[1])
PY
    then
      openssl x509 -in "$cert" -noout -checkip "$endpoint" >/dev/null 2>&1 || continue
      openssl verify -purpose sslserver -verify_ip "$endpoint" -untrusted "$cert" "$cert" >/dev/null 2>&1 || continue
    else
      openssl x509 -in "$cert" -noout -checkhost "$endpoint" >/dev/null 2>&1 || continue
      openssl verify -purpose sslserver -verify_hostname "$endpoint" -untrusted "$cert" "$cert" >/dev/null 2>&1 || continue
    fi
    cert_pub=$(openssl x509 -in "$cert" -pubkey -noout 2>/dev/null | openssl pkey -pubin -outform DER 2>/dev/null | sha256sum | awk '{print $1}') || continue
    [[ $cert_pub =~ ^[0-9a-f]{64}$ ]] || continue
    for key in "${key_candidates[@]}"; do
      key_pub=$(openssl pkey -passin pass: -in "$key" -pubout -outform DER 2>/dev/null | sha256sum | awk '{print $1}') || continue
      if [[ $key_pub == "$cert_pub" ]]; then
        found_server_cert=$cert
        found_server_key=$key
        return 0
      fi
    done
  done
  return 1
}

if [[ $mode == 8 ]]; then
  endpoint=${NLI_SETUP_ENDPOINT:-}
  if [[ -z $endpoint ]]; then
    endpoint=$(first_discovered_identity)
    [[ -n $endpoint ]] || read -r -p 'Controller DNS hostname or IP used by clients: ' endpoint
    [[ -z $endpoint ]] || info "automatically selected controller identity $endpoint"
  fi
  endpoint=$(python3 - "$endpoint" <<'PY'
import ipaddress, re, sys
value=sys.argv[1].rstrip('.')
try:
    ipaddress.ip_address(value)
except ValueError:
    if not re.fullmatch(r"(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*", value):
        raise SystemExit(1)
print(value)
PY
  ) || fail 'enter a valid controller DNS hostname or IP address'
  if find_existing_server_pair; then
    info "found a trusted TLS server certificate with a matching key at $found_server_cert"
    mode=6
  else
    info 'no eligible server certificate/key pair matched this identity; using generated offline private TLS'
    mode=2
    offline_private_tls=true
    if [[ -z ${NLI_SETUP_STORAGE_PROVIDER:-} ]]; then storage_provider=local; fi
  fi
fi

if [[ $mode == 2 ]]; then
  default_endpoint=$(first_discovered_identity)
  default_endpoint=${default_endpoint:-$(hostname -f 2>/dev/null || hostname)}
  endpoint=${NLI_SETUP_ENDPOINT:-$endpoint}
  [[ -n $endpoint ]] || read -r -p "Controller DNS name or private IP [$default_endpoint]: " endpoint
  endpoint=${endpoint:-$default_endpoint}
  [[ $endpoint =~ ^[A-Za-z0-9.-]+$ ]] || fail 'controller endpoint may contain only DNS-name or IPv4 characters'
  [[ $endpoint != *.example.com && $endpoint != nli-controller.example.com ]] || fail 'documentation placeholder host is not allowed'

  additional_tls_identities=${NLI_SETUP_TLS_IDENTITIES:-}
  if [[ -z $additional_tls_identities ]]; then
    if ! read -r -p 'Additional controller DNS names or IPs for TLS, comma separated [automatic host identities only]: ' additional_tls_identities; then
      additional_tls_identities=''
      info 'optional TLS identity input ended; using the automatic controller identities'
    fi
  fi

  tls_identities=()
  tls_sans=()
  add_tls_identity() {
    local candidate=${1%.}
    candidate=$(printf '%s' "$candidate" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')
    [[ -n $candidate ]] || return 0
    local san_type
    san_type=$(python3 - "$candidate" <<'PY'
import ipaddress, re, sys
value = sys.argv[1]
try:
    ipaddress.ip_address(value)
    print("IP")
    raise SystemExit(0)
except ValueError:
    pass
if len(value) > 253 or not re.fullmatch(r"(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*", value):
    raise SystemExit(1)
print("DNS")
PY
    ) || fail "invalid TLS DNS name or IP identity: $candidate"
    local existing
    for existing in "${tls_identities[@]:-}"; do
      [[ ${existing,,} == ${candidate,,} ]] && return 0
    done
    tls_identities+=("$candidate")
    tls_sans+=("$san_type:$candidate")
  }
  add_tls_identity "$endpoint"
  add_tls_identity 'localhost'
  add_tls_identity '127.0.0.1'
  add_tls_identity '::1'
  add_tls_identity "$(hostname 2>/dev/null || true)"
  add_tls_identity "$(hostname -s 2>/dev/null || true)"
  add_tls_identity "$(hostname -f 2>/dev/null || true)"
  while read -r detected_identity; do add_tls_identity "$detected_identity"; done < <(hostname -I 2>/dev/null | tr ' ' '\n' | sed '/^$/d' || true)
  while IFS= read -r detected_identity; do add_tls_identity "$detected_identity"; done < <(discover_host_identities)
  IFS=',' read -r -a requested_tls_identities <<<"$additional_tls_identities"
  for requested_identity in "${requested_tls_identities[@]:-}"; do add_tls_identity "$requested_identity"; done
  san=$(IFS=,; printf '%s' "${tls_sans[*]}")

  tls_dir="$STATE_DIR/tls"
  install -d -o root -g nli -m 0750 "$tls_dir"
  ca_key="$tls_dir/ca.key"
  ca_cert="$tls_dir/ca.crt"
  tls_key="$tls_dir/server.key"
  tls_cert="$tls_dir/server.crt"

  generate_tls=false
  if [[ ! -s $ca_key || ! -s $ca_cert || ! -s $tls_key || ! -s $tls_cert ]]; then
    generate_tls=true
  else
    for tls_identity in "${tls_identities[@]}"; do
      if python3 - "$tls_identity" <<'PY' >/dev/null 2>&1
import ipaddress, sys
ipaddress.ip_address(sys.argv[1])
PY
      then
        openssl x509 -in "$tls_cert" -noout -checkip "$tls_identity" >/dev/null 2>&1 || generate_tls=true
      else
        openssl x509 -in "$tls_cert" -noout -checkhost "$tls_identity" >/dev/null 2>&1 || generate_tls=true
      fi
    done
  fi

  if [[ $generate_tls == true ]]; then
    info 'generating a controller TLS certificate for every configured DNS name and IP identity'
    generation_dir=$(mktemp -d "$tls_dir/.generate.XXXXXXXX") || fail 'could not create a temporary TLS generation directory'
    generation_error="$generation_dir/openssl.error"
    generation_ca_key="$generation_dir/ca.key"
    generation_ca_cert="$generation_dir/ca.crt"
    generation_tls_key="$generation_dir/server.key"
    generation_tls_cert="$generation_dir/server.crt"
    generation_csr="$generation_dir/server.csr"
    generation_extensions="$generation_dir/server.ext"

    openssl_or_fail() {
      local description=$1
      shift
      : >"$generation_error"
      if ! "$@" > /dev/null 2>"$generation_error"; then
        local detail
        detail=$(tail -n 8 "$generation_error" | tr '\n' ' ' | sed -E 's/[[:space:]]+/ /g; s/[[:space:]]+$//')
        rm -rf -- "$generation_dir"
        [[ -n $detail ]] || detail='OpenSSL returned a non-zero exit status without diagnostics'
        fail "$description failed: $detail"
      fi
    }

    # Generate keys separately from certificate requests. This avoids provider-
    # dependent `req -newkey` behaviour and lets each failure identify its exact
    # stage on Rocky Linux 9/10 and in CI.
    if [[ -s $ca_key && -s $ca_cert ]]; then
      cp -- "$ca_key" "$generation_ca_key"
      cp -- "$ca_cert" "$generation_ca_cert"
      openssl_or_fail 'existing private CA key validation' openssl pkey -in "$generation_ca_key" -check -noout
      openssl_or_fail 'existing private CA certificate validation' openssl x509 -in "$generation_ca_cert" -noout -checkend 86400
      ca_public_key=$(openssl pkey -in "$generation_ca_key" -pubout 2>/dev/null | sha256sum | awk '{print $1}')
      ca_certificate_public_key=$(openssl x509 -in "$generation_ca_cert" -pubkey -noout 2>/dev/null | sha256sum | awk '{print $1}')
      [[ -n $ca_public_key && $ca_public_key == "$ca_certificate_public_key" ]] || { rm -rf -- "$generation_dir"; fail 'existing private CA key does not match its certificate'; }
    else
      openssl_or_fail 'private CA key generation' openssl genpkey -algorithm RSA \
        -pkeyopt rsa_keygen_bits:3072 -out "$generation_ca_key"
      openssl_or_fail 'private CA certificate generation' openssl req -x509 -new -sha256 -days 3650 \
        -key "$generation_ca_key" -subj '/CN=NLI v20 Private Controller CA' \
        -addext 'basicConstraints=critical,CA:TRUE' \
        -addext 'keyUsage=critical,keyCertSign,cRLSign' \
        -out "$generation_ca_cert"
    fi
    openssl_or_fail 'controller TLS key generation' openssl genpkey -algorithm RSA \
      -pkeyopt rsa_keygen_bits:3072 -out "$generation_tls_key"
    openssl_or_fail 'controller TLS request generation' openssl req -new -sha256 \
      -key "$generation_tls_key" -subj "/CN=$endpoint" -out "$generation_csr"
    cat >"$generation_extensions" <<EOF
subjectAltName=$san
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
EOF
    certificate_serial=$(openssl rand -hex 16) || { rm -rf -- "$generation_dir"; fail 'controller certificate serial generation failed'; }
    openssl_or_fail 'controller TLS certificate signing' openssl x509 -req -sha256 -days 825 \
      -in "$generation_csr" -CA "$generation_ca_cert" -CAkey "$generation_ca_key" \
      -set_serial "0x$certificate_serial" -extfile "$generation_extensions" -out "$generation_tls_cert"
    openssl_or_fail 'private CA key validation' openssl pkey -in "$generation_ca_key" -check -noout
    openssl_or_fail 'controller TLS key validation' openssl pkey -in "$generation_tls_key" -check -noout
    openssl_or_fail 'controller TLS chain validation' openssl verify -CAfile "$generation_ca_cert" "$generation_tls_cert"
    for tls_identity in "${tls_identities[@]}"; do
      if python3 - "$tls_identity" <<'PY' >/dev/null 2>&1
import ipaddress, sys
ipaddress.ip_address(sys.argv[1])
PY
      then
        openssl_or_fail "controller TLS IP identity validation ($tls_identity)" openssl x509 -in "$generation_tls_cert" -noout -checkip "$tls_identity"
      else
        openssl_or_fail "controller TLS DNS identity validation ($tls_identity)" openssl x509 -in "$generation_tls_cert" -noout -checkhost "$tls_identity"
      fi
    done

    install -o root -g root -m 0600 "$generation_ca_key" "$ca_key"
    install -o root -g root -m 0644 "$generation_ca_cert" "$ca_cert"
    install -o root -g nli -m 0640 "$generation_tls_key" "$tls_key"
    install -o root -g nli -m 0644 "$generation_tls_cert" "$tls_cert"
    rm -rf -- "$generation_dir"
    unset -f openssl_or_fail
  fi
  for tls_identity in "${tls_identities[@]}"; do
    if python3 - "$tls_identity" <<'PY' >/dev/null 2>&1
import ipaddress, sys
ipaddress.ip_address(sys.argv[1])
PY
    then
      openssl x509 -in "$tls_cert" -noout -checkip "$tls_identity" >/dev/null || fail "TLS certificate does not match IP identity $tls_identity"
    else
      openssl x509 -in "$tls_cert" -noout -checkhost "$tls_identity" >/dev/null || fail "TLS certificate does not match DNS identity $tls_identity"
    fi
  done
  chown root:root "$ca_key" "$ca_cert"
  chown root:nli "$tls_key" "$tls_cert"
  chmod 0600 "$ca_key"
  chmod 0644 "$ca_cert" "$tls_cert"
  chmod 0640 "$tls_key"

  listen_address="0.0.0.0:${PORT}"
  controller_url="https://${endpoint}:${PORT}"
  force_secure=true
  ca_base64=$(base64 -w0 "$ca_cert")
  info "TLS certificate identities: $(IFS=', '; printf '%s' "${tls_identities[*]}")"
elif [[ $mode == 6 ]]; then
  endpoint=${NLI_SETUP_ENDPOINT:-$endpoint}
  [[ -n $endpoint ]] || read -r -p 'Controller DNS hostname present in the corporate certificate: ' endpoint
  if [[ $auto_server_tls == true && -n $found_server_cert && -n $found_server_key ]]; then
    tls_dir="$STATE_DIR/tls"
    install -d -o root -g nli -m 0750 "$tls_dir"
    tls_cert="$tls_dir/discovered-server.crt"
    tls_key="$tls_dir/discovered-server.key"
    install -o root -g nli -m 0644 "$found_server_cert" "$tls_cert"
    install -o root -g nli -m 0640 "$found_server_key" "$tls_key"
    ca_cert=''
    ca_base64=''
    listen_address="0.0.0.0:${PORT}"
    controller_url="https://${endpoint}:${PORT}"
    force_secure=true
    info "using verified server certificate $found_server_cert with its matching private key"
  else
    [[ $endpoint =~ ^[A-Za-z0-9.-]+$ ]] || fail 'enter the controller DNS hostname or IP present in the certificate SAN'
    supplied_cert=${NLI_SETUP_TLS_CERT_FILE:-}
    supplied_key=${NLI_SETUP_TLS_KEY_FILE:-}
    supplied_ca=${NLI_SETUP_TLS_CA_FILE:-}
    [[ -n $supplied_cert ]] || read -r -p 'Corporate server certificate/full-chain PEM path: ' supplied_cert
    [[ -n $supplied_key ]] || read -r -p 'Corporate unencrypted private-key PEM path: ' supplied_key
    [[ -f $supplied_cert && -f $supplied_key ]] || fail 'corporate certificate and private-key files are required'
    openssl x509 -in "$supplied_cert" -noout -checkend 86400 >/dev/null || fail 'corporate certificate is invalid or expires within 24 hours'
    openssl x509 -in "$supplied_cert" -noout -purpose | grep -Fxq 'SSL server : Yes' || fail 'corporate certificate is not valid for SSL server use'
    ca_flag=$(openssl x509 -in "$supplied_cert" -noout -text | grep -m1 -E 'CA:[[:space:]]*TRUE' || true)
    [[ -z $ca_flag ]] || fail 'corporate certificate is a CA certificate; provide a leaf server certificate'
    openssl pkey -passin pass: -in "$supplied_key" -noout >/dev/null 2>&1 || fail 'corporate private key must be a valid unencrypted PEM key'
    cert_public=$(openssl x509 -in "$supplied_cert" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | awk '{print $1}')
    key_public=$(openssl pkey -passin pass: -in "$supplied_key" -pubout -outform DER | sha256sum | awk '{print $1}')
    [[ $cert_public == "$key_public" ]] || fail 'corporate certificate and private key do not match'
    if python3 - "$endpoint" <<'PY' >/dev/null 2>&1
import ipaddress, sys
ipaddress.ip_address(sys.argv[1])
PY
    then
      openssl x509 -in "$supplied_cert" -noout -checkip "$endpoint" >/dev/null || fail 'corporate certificate SAN does not contain the controller IP'
    else
      openssl x509 -in "$supplied_cert" -noout -checkhost "$endpoint" >/dev/null || fail 'corporate certificate SAN does not contain the controller DNS hostname'
    fi
    tls_dir="$STATE_DIR/tls"
    install -d -o root -g nli -m 0750 "$tls_dir"
    tls_cert="$tls_dir/corporate-server.crt"
    tls_key="$tls_dir/corporate-server.key"
    install -o root -g nli -m 0644 "$supplied_cert" "$tls_cert"
    install -o root -g nli -m 0640 "$supplied_key" "$tls_key"
    if [[ -n $supplied_ca ]]; then
      [[ -f $supplied_ca ]] || fail "corporate CA file not found: $supplied_ca"
      openssl x509 -in "$supplied_ca" -noout >/dev/null 2>&1 || fail 'corporate CA file is not a PEM certificate'
      ca_cert="$tls_dir/corporate-ca.crt"
      install -o root -g nli -m 0644 "$supplied_ca" "$ca_cert"
      ca_base64=$(base64 -w0 "$ca_cert")
    else
      ca_cert=''
      info 'no private CA bundle supplied; clients will use their operating-system trust store'
    fi
    listen_address="0.0.0.0:${PORT}"
    controller_url="https://${endpoint}:${PORT}"
    force_secure=true
  fi
elif [[ $mode == 3 || $mode == 4 || $mode == 5 || $mode == 9 ]]; then
  case $mode in
    3) endpoint=${NLI_SETUP_ENDPOINT:-}; [[ -n $endpoint ]] || read -r -p 'Public DNS hostname for Nginx/ACME: ' endpoint ;;
    4) endpoint=${NLI_SETUP_ENDPOINT:-}; [[ -n $endpoint ]] || read -r -p 'Public DNS hostname configured in the Cloudflare Tunnel dashboard: ' endpoint ;;
    5) endpoint=${NLI_SETUP_ENDPOINT:-}; [[ -n $endpoint ]] || read -r -p 'Public DNS hostname served by your HTTPS reverse proxy: ' endpoint ;;
    9) endpoint=${NLI_SETUP_ENDPOINT:-}; [[ -n $endpoint ]] || read -r -p 'Public DNS hostname for Apache/ACME: ' endpoint ;;
  esac
  [[ $endpoint =~ ^[A-Za-z0-9.-]+$ && $endpoint == *.* && $endpoint != *..* && $endpoint != -* && $endpoint != *- ]] || fail 'enter a valid public DNS hostname, such as controller.example.net'
  [[ $endpoint != *.example.com && $endpoint != nli-controller.example.com ]] || fail 'documentation placeholder host is not allowed'
  listen_address="127.0.0.1:${PORT}"
  controller_url="https://${endpoint}"
  force_secure=true
fi

if [[ $mode == 10 ]]; then
  endpoint=''
  listen_address="127.0.0.1:${PORT}"
  controller_url=''
  force_secure=true
  if [[ -z ${NLI_SETUP_STORAGE_PROVIDER:-} ]]; then storage_provider=local; fi
fi

if [[ $mode == 3 || $mode == 9 ]]; then
  for command in dnf getent; do command -v "$command" >/dev/null || fail "$command is required for Nginx/ACME setup"; done
  dnf -y install bind-utils || fail 'could not install bind-utils for public DNS preflight'
  command -v dig >/dev/null || fail 'dig is unavailable after installing bind-utils; verify Rocky repositories'
  info 'checking public DNS and host reachability before ACME'
  public_ip=${NLI_SETUP_PUBLIC_IP:-}
  if [[ -z $public_ip ]]; then
    public_ip=$(curl --fail --silent --show-error --max-time 10 https://api.ipify.org 2>/dev/null || true)
  fi
  if [[ -z $public_ip ]]; then
    if [[ -t 0 ]]; then
      read -r -p 'Could not discover the public IPv4 address. Enter the globally routable address assigned to this host or NAT/router: ' public_ip
    else
      fail 'could not discover the public IPv4 address; set NLI_SETUP_PUBLIC_IP to this host or NAT/router public address'
    fi
  elif [[ -z ${NLI_SETUP_PUBLIC_IP:-} && -t 0 ]]; then
    read -r -p "Detected public IPv4 $public_ip; enter a different public IPv4 if this host is behind NAT [$public_ip]: " supplied_public_ip
    public_ip=${supplied_public_ip:-$public_ip}
  fi
  [[ -n $public_ip ]] || fail 'a public IPv4 address is required; set NLI_SETUP_PUBLIC_IP and retry'
  python3 - "$public_ip" <<'PY' || fail 'the selected public IP must be a globally routable IPv4 address; set NLI_SETUP_PUBLIC_IP to the NAT/router public address if applicable'
import ipaddress, sys
try:
    value = ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit(1)
raise SystemExit(0 if value.version == 4 and value.is_global else 1)
PY
  resolved_a=$(dig +short A "$endpoint" | awk '/^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ {print}' | sort -u || true)
  [[ -n $resolved_a ]] || fail "$endpoint has no public A record; publish DNS pointing to this host/router before requesting ACME"
  while IFS= read -r address; do
    [[ $address == "$public_ip" ]] || fail "$endpoint resolves to $address, not $public_ip; correct the public DNS A record before ACME"
  done <<<"$resolved_a"
  resolved_aaaa=$(dig +short AAAA "$endpoint" | awk '/:/ {print}' | sort -u || true)
  if [[ -n $resolved_aaaa ]]; then
    public_ipv6=${NLI_SETUP_PUBLIC_IPV6:-}
    if [[ -z $public_ipv6 ]]; then
      [[ -t 0 ]] || fail "$endpoint has an AAAA record; set NLI_SETUP_PUBLIC_IPV6 to the server's globally routable IPv6 address before ACME"
      read -r -p "Detected AAAA record(s): $(tr '\n' ' ' <<<"$resolved_aaaa"). Enter the server's public IPv6 address: " public_ipv6
    fi
    python3 - "$public_ipv6" <<'PY' || fail 'NLI_SETUP_PUBLIC_IPV6 must be a globally routable IPv6 address'
import ipaddress, sys
try:
    value = ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit(1)
raise SystemExit(0 if value.version == 6 and value.is_global else 1)
PY
    while IFS= read -r address; do
      [[ $address == "$public_ipv6" ]] || fail "$endpoint resolves to IPv6 $address, not $public_ipv6; correct the public DNS AAAA record before ACME"
    done <<<"$resolved_aaaa"
  fi
  for port in 80 443; do
    listeners=$(ss -H -ltnp "sport = :$port" || true)
    if [[ -n $listeners ]]; then
      expected_listener=nginx
      [[ $mode != 9 ]] || expected_listener=httpd
      while IFS= read -r listener; do
        [[ $listener == *"$expected_listener"* ]] || fail "TCP $port is held by a service other than $expected_listener; stop the conflicting service before ACME setup"
      done <<<"$listeners"
    fi
  done
  if command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    trap rollback_edge_firewall EXIT
    if ! firewall-cmd --query-service=http >/dev/null 2>&1; then
      firewall-cmd --add-service=http >/dev/null || fail 'firewalld could not allow HTTP; allow inbound TCP 80 before ACME'
      added_firewall_http=1
    fi
    if ! firewall-cmd --query-service=https >/dev/null 2>&1; then
      firewall-cmd --add-service=https >/dev/null || fail 'firewalld could not allow HTTPS; allow inbound TCP 443'
      added_firewall_https=1
    fi
    if ! firewall-cmd --permanent --query-service=http >/dev/null 2>&1; then
      firewall-cmd --permanent --add-service=http >/dev/null || fail 'firewalld could not persist HTTP access; allow inbound TCP 80 before ACME'
      added_firewall_http_permanent=1
    fi
    if ! firewall-cmd --permanent --query-service=https >/dev/null 2>&1; then
      firewall-cmd --permanent --add-service=https >/dev/null || fail 'firewalld could not persist HTTPS access; allow inbound TCP 443'
      added_firewall_https_permanent=1
    fi
  fi
  info 'DNS and local firewall preflight passed; confirm router/cloud firewalls forward TCP 80/443 to this host'
  if [[ $mode == 3 ]]; then
    info 'installing Nginx and Certbot packages'
    dnf -y install epel-release nginx certbot policycoreutils-python-utils
  else
    info 'installing Apache HTTP Server, SSL/proxy modules, and Certbot packages'
    dnf -y install epel-release httpd mod_ssl certbot policycoreutils-python-utils
  fi
  command -v certbot >/dev/null || fail 'Certbot is unavailable from configured Rocky/EPEL repositories; install the official EFF Certbot package and rerun setup'
  if [[ $mode == 9 ]]; then
    command -v httpd >/dev/null || fail 'Apache HTTP Server package did not provide httpd'
    acme_email=${NLI_SETUP_ACME_EMAIL:-}
    [[ -n $acme_email ]] || read -r -p 'ACME certificate contact email: ' acme_email
    [[ $acme_email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || fail 'a valid ACME contact email is required'
    install -d -m 0755 /var/www/nli-acme
    temporary_vhost=/etc/httpd/conf.d/nli-controller.conf
    previous_vhost=$(mktemp)
    [[ -e $temporary_vhost ]] && cp -a "$temporary_vhost" "$previous_vhost" || :
    had_previous=0; [[ -s $previous_vhost ]] && had_previous=1
    capture_renewal_state
    edge_rollback() {
      if [[ $had_previous == 1 ]]; then cp -a "$previous_vhost" "$temporary_vhost" || true; else rm -f "$temporary_vhost" || true; fi
      restore_renewal_state || true
      restore_selinux_proxy_boolean || true
      httpd -t >/dev/null 2>&1 && systemctl reload httpd.service >/dev/null 2>&1 || true
      rm -f "$previous_vhost" || true
    }
  trap 'edge_rollback || true; rollback_edge_firewall' EXIT
    httpd_version=$(httpd -v 2>/dev/null | sed -n 's/^Server version: Apache\/\([0-9.]*\).*/\1/p')
    [[ -n $httpd_version ]] || fail 'could not determine the Apache HTTP Server version'
    apache_websocket_proxy="ProxyPass / http://127.0.0.1:$PORT/ timeout=300"
    apache_websocket_rules=''
    if [[ $(printf '%s\n%s\n' 2.4.47 "$httpd_version" | sort -V | head -n1) == 2.4.47 ]]; then
      apache_websocket_proxy="ProxyPass / http://127.0.0.1:$PORT/ upgrade=websocket timeout=300"
    else
      apache_websocket_rules="  RewriteEngine On
  RewriteCond %{HTTP:Upgrade} =websocket [NC]
  RewriteCond %{HTTP:Connection} upgrade [NC]
  RewriteRule ^/(.*)$ ws://127.0.0.1:$PORT/\$1 [P,L]"
    fi
    cat >"$temporary_vhost" <<EOF
<VirtualHost *:80>
  ServerName $endpoint
  Alias /.well-known/acme-challenge/ /var/www/nli-acme/.well-known/acme-challenge/
  <Directory /var/www/nli-acme>
    Require all granted
  </Directory>
</VirtualHost>
EOF
    httpd -t || fail 'temporary Apache ACME virtual host failed validation'
    if systemctl is-active --quiet httpd.service; then
      systemctl reload httpd.service || proxy_service_fail httpd.service 'Apache could not load the validated temporary ACME virtual host'
    else
      systemctl enable --now httpd.service || proxy_service_fail httpd.service 'Apache failed to start for ACME validation'
    fi
    certbot certonly --webroot --webroot-path /var/www/nli-acme --non-interactive --agree-tos --email "$acme_email" --preferred-challenges http -d "$endpoint" || fail 'ACME issuance failed; verify public DNS and external TCP 80 forwarding, then rerun setup'
    cat >"$temporary_vhost" <<EOF
<VirtualHost *:80>
  ServerName $endpoint
  Alias /.well-known/acme-challenge/ /var/www/nli-acme/.well-known/acme-challenge/
  <Directory /var/www/nli-acme>
    Require all granted
  </Directory>
  RewriteEngine On
  RewriteCond %{REQUEST_URI} !^/\.well-known/acme-challenge/
  RewriteRule ^ https://$endpoint%{REQUEST_URI} [R=301,L,NE]
</VirtualHost>
<VirtualHost *:443>
  ServerName $endpoint
  SSLEngine on
  SSLCertificateFile /etc/letsencrypt/live/$endpoint/fullchain.pem
  SSLCertificateKeyFile /etc/letsencrypt/live/$endpoint/privkey.pem
  SSLProtocol TLSv1.2 TLSv1.3
  ProxyPreserveHost On
  ProxyRequests Off
  ProxyAddHeaders On
  RequestHeader set X-Forwarded-Proto "https"
  RequestHeader set X-Real-IP "expr=%{REMOTE_ADDR}"
  SetEnv no-gzip 1
  ProxyTimeout 300
  LimitRequestBody 67108864
${apache_websocket_rules}
  ${apache_websocket_proxy}
  ProxyPassReverse / http://127.0.0.1:$PORT/
</VirtualHost>
EOF
    httpd -t || fail 'generated Apache reverse-proxy configuration failed validation'
    enable_selinux_proxy_boolean
    if systemctl is-active --quiet httpd.service; then
      systemctl reload httpd.service || proxy_service_fail httpd.service 'Apache could not reload the validated proxy configuration'
    else
      systemctl enable --now httpd.service || proxy_service_fail httpd.service 'Apache failed to start with the validated proxy configuration'
    fi
    cat >/etc/systemd/system/nli-certbot-renew.service <<EOF
[Unit]
Description=Renew the NLI controller ACME certificate
[Service]
Type=oneshot
ExecStart=$(command -v certbot) renew --quiet
ExecStartPost=/usr/bin/systemctl reload httpd.service
EOF
    cat >/etc/systemd/system/nli-certbot-renew.timer <<'EOF'
[Unit]
Description=Twice-daily NLI ACME certificate renewal check
[Timer]
OnCalendar=*-*-* 00,12:00:00
RandomizedDelaySec=1h
Persistent=true
[Install]
WantedBy=timers.target
EOF
    systemctl daemon-reload
    systemctl enable --now nli-certbot-renew.timer
  fi
fi

if [[ $mode == 10 ]]; then
  command -v dnf >/dev/null || fail 'dnf is required for Cloudflare Quick Tunnel setup'
  if ! command -v cloudflared >/dev/null; then
    curl --fail --silent --show-error --location https://pkg.cloudflare.com/cloudflared.repo -o /etc/yum.repos.d/cloudflared.repo || fail 'could not retrieve Cloudflare official RPM repository configuration'
    dnf -y install cloudflared || fail 'cloudflared installation from Cloudflare official repository failed'
  fi
  getent group nli-tunnel >/dev/null || groupadd --system nli-tunnel
  getent passwd nli-tunnel >/dev/null || useradd --system --gid nli-tunnel --home-dir /var/lib/nli-tunnel --shell /sbin/nologin nli-tunnel
  install -d -o nli-tunnel -g nli-tunnel -m 0750 /var/lib/nli-tunnel
  quick_service=/etc/systemd/system/nli-cloudflared-quick.service
  quick_service_backup=$(mktemp)
  quick_service_existed=0
  [[ ! -e $quick_service ]] || { cp -a "$quick_service" "$quick_service_backup"; quick_service_existed=1; }
  quick_service_was_active=0
  systemctl is-active --quiet nli-cloudflared-quick.service && quick_service_was_active=1 || true
  rollback_quick_tunnel() {
    systemctl stop nli-cloudflared-quick.service >/dev/null 2>&1 || true
    if [[ $quick_service_existed == 1 ]]; then cp -a "$quick_service_backup" "$quick_service" || true; else rm -f -- "$quick_service" || true; fi
    systemctl daemon-reload >/dev/null 2>&1 || true
    if [[ $quick_service_was_active == 1 ]]; then systemctl start nli-cloudflared-quick.service >/dev/null 2>&1 || true; fi
    rm -f -- "$quick_service_backup" || true
  }
  trap rollback_quick_tunnel EXIT
  cat >/etc/systemd/system/nli-cloudflared-quick.service <<EOF
[Unit]
Description=NLI temporary Cloudflare Quick Tunnel (testing only)
After=network-online.target nli-controller-agent.service
Wants=network-online.target

[Service]
Type=simple
User=nli-tunnel
Group=nli-tunnel
WorkingDirectory=/var/lib/nli-tunnel
Environment=HOME=/var/lib/nli-tunnel
ExecStart=/usr/bin/cloudflared tunnel --no-autoupdate --url http://127.0.0.1:$PORT
Restart=no
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
fi

if [[ $mode == 3 ]]; then
  acme_email=${NLI_SETUP_ACME_EMAIL:-}
  [[ -n $acme_email ]] || read -r -p 'ACME certificate contact email: ' acme_email
  [[ $acme_email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || fail 'a valid ACME contact email is required'
  certbot_bin=$(command -v certbot)
  install -d -m 0755 /etc/nginx/conf.d /var/www/nli-acme
  nginx_site=/etc/nginx/conf.d/nli-controller.conf
  nginx_backup=$(mktemp)
  [[ -e $nginx_site ]] && cp -a "$nginx_site" "$nginx_backup" || :
  had_nginx_site=0; [[ -s $nginx_backup ]] && had_nginx_site=1
  capture_renewal_state
  nginx_rollback() {
    if [[ $had_nginx_site == 1 ]]; then cp -a "$nginx_backup" "$nginx_site" || true; else rm -f "$nginx_site" || true; fi
    restore_renewal_state || true
    restore_selinux_proxy_boolean || true
    nginx -t >/dev/null 2>&1 && systemctl reload nginx.service >/dev/null 2>&1 || true
    rm -f "$nginx_backup"
  }
  trap 'nginx_rollback || true; rollback_edge_firewall' EXIT
  cat >"$nginx_site" <<EOF
map \$http_upgrade \$connection_upgrade { default upgrade; '' close; }
server {
    listen 80;
    server_name $endpoint;
    location /.well-known/acme-challenge/ { root /var/www/nli-acme; }
    location / { return 404; }
}
EOF
  nginx -t || fail 'temporary Nginx ACME configuration failed validation'
  if systemctl is-active --quiet nginx.service; then
    systemctl reload nginx.service || proxy_service_fail nginx.service 'Nginx could not load the validated temporary ACME site'
  else
    systemctl enable --now nginx.service || proxy_service_fail nginx.service 'Nginx failed to start for ACME validation'
  fi
  certbot certonly --webroot --webroot-path /var/www/nli-acme --non-interactive --agree-tos --email "$acme_email" --preferred-challenges http -d "$endpoint" || fail 'ACME issuance failed; confirm external TCP 80 forwarding and public DNS, then rerun setup'
  cat >"$nginx_site" <<EOF
map \$http_upgrade \$connection_upgrade { default upgrade; '' close; }
server {
    listen 80;
    server_name $endpoint;
    location /.well-known/acme-challenge/ { root /var/www/nli-acme; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl;
    server_name $endpoint;
    ssl_certificate /etc/letsencrypt/live/$endpoint/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$endpoint/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_timeout 1d;
    ssl_session_cache shared:NLI_TLS:10m;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    client_max_body_size 64m;
    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header Authorization \$http_authorization;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        send_timeout 300s;
    }
}
EOF
  nginx -t || fail 'generated Nginx reverse-proxy configuration failed validation'
  enable_selinux_proxy_boolean
  if systemctl is-active --quiet nginx.service; then
    systemctl reload nginx.service || proxy_service_fail nginx.service 'Nginx could not reload the validated proxy configuration'
  else
    systemctl enable --now nginx.service || proxy_service_fail nginx.service 'Nginx failed to start with the validated proxy configuration'
  fi
  cat >/etc/systemd/system/nli-certbot-renew.service <<EOF
[Unit]
Description=Renew the NLI controller ACME certificate
[Service]
Type=oneshot
ExecStart=$certbot_bin renew --quiet
ExecStartPost=/usr/bin/systemctl reload nginx.service
EOF
  cat >/etc/systemd/system/nli-certbot-renew.timer <<'EOF'
[Unit]
Description=Twice-daily NLI ACME certificate renewal check
[Timer]
OnCalendar=*-*-* 00,12:00:00
RandomizedDelaySec=1h
Persistent=true
[Install]
WantedBy=timers.target
EOF
  systemctl daemon-reload
  systemctl enable --now nli-certbot-renew.timer
fi
if [[ $mode == 4 ]]; then
  for command in dnf; do command -v "$command" >/dev/null || fail "$command is required for Cloudflare Tunnel setup"; done
  if ! command -v cloudflared >/dev/null; then
    curl --fail --silent --show-error --location https://pkg.cloudflare.com/cloudflared.repo -o /etc/yum.repos.d/cloudflared.repo || fail 'could not retrieve Cloudflare official RPM repository configuration'
    dnf -y install cloudflared || fail 'cloudflared installation from Cloudflare official repository failed'
  fi
  cloudflared_version=$(cloudflared --version | awk 'NR==1 {print $3}')
  [[ -n $cloudflared_version ]] || fail 'could not determine cloudflared version'
  [[ $(printf '%s\n%s\n' 2025.4.0 "$cloudflared_version" | sort -V | head -n1) == 2025.4.0 ]] || fail 'cloudflared 2025.4.0 or newer is required for secure token-file support'
  tunnel_token=${NLI_CLOUDFLARE_TUNNEL_TOKEN:-}
  if [[ -z $tunnel_token ]]; then read -r -s -p 'Cloudflare remotely-managed Tunnel token (input is hidden): ' tunnel_token; printf '\n'; fi
  [[ $tunnel_token =~ ^[A-Za-z0-9._~+/=-]{40,}$ ]] || fail 'Cloudflare Tunnel token format is invalid or too short'
  getent group nli-tunnel >/dev/null || groupadd --system nli-tunnel
  getent passwd nli-tunnel >/dev/null || useradd --system --gid nli-tunnel --home-dir /var/lib/nli-tunnel --shell /sbin/nologin nli-tunnel
  install -d -o root -g nli-tunnel -m 0750 /etc/nli/cloudflare
  token_tmp=$(mktemp /etc/nli/cloudflare/token.XXXXXX)
  printf '%s' "$tunnel_token" >"$token_tmp"
  install -o root -g nli-tunnel -m 0640 "$token_tmp" /etc/nli/cloudflare/tunnel-token
  rm -f "$token_tmp"
  cat >/etc/systemd/system/nli-cloudflared.service <<EOF
[Unit]
Description=NLI v20 Cloudflare Tunnel connector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nli-tunnel
Group=nli-tunnel
ExecStart=/usr/bin/cloudflared tunnel --no-autoupdate run --token-file /etc/nli/cloudflare/tunnel-token
Restart=always
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadOnlyPaths=/etc/nli/cloudflare/tunnel-token

[Install]
WantedBy=multi-user.target
EOF
  unset tunnel_token
  systemctl daemon-reload
fi

quarantine_retention_enabled=${NLI_SETUP_QUARANTINE_RAW_RETENTION:-}
existing_quarantine_key=$(read_env NLI_QUARANTINE_ENCRYPTION_KEY)
if [[ -z $existing_quarantine_key && -s $STATE_DIR/quarantine-metadata/quarantine.key ]]; then
  IFS= read -r existing_quarantine_key <"$STATE_DIR/quarantine-metadata/quarantine.key" || true
fi
quarantine_key=''

if [[ $storage_provider == supabase ]]; then
current_supabase_url=$(read_env SUPABASE_URL)
current_supabase_key=$(read_env SUPABASE_PUBLISHABLE_KEY)
current_supabase_key=${current_supabase_key:-$(read_env SUPABASE_ANON_KEY)}
if [[ -n ${NLI_SETUP_SUPABASE_URL:-} ]]; then
  supabase_url=$NLI_SETUP_SUPABASE_URL
else
  read -r -p "Supabase project URL [${current_supabase_url:-https://project-ref.supabase.co}]: " supabase_url
  supabase_url=${supabase_url:-${current_supabase_url:-}}
fi
NLI_SETUP_SUPABASE_PUBLISHABLE_KEY=${NLI_SETUP_SUPABASE_PUBLISHABLE_KEY:-${NLI_SETUP_SUPABASE_ANON_KEY:-}}
if [[ -n ${NLI_SETUP_SUPABASE_PUBLISHABLE_KEY:-} ]]; then
  supabase_public_key=$NLI_SETUP_SUPABASE_PUBLISHABLE_KEY
elif [[ -n $current_supabase_key ]]; then
  read -r -s -p 'Supabase publishable/public key [Enter keeps the packaged value]: ' supabase_public_key
  printf '\n'
  supabase_public_key=${supabase_public_key:-$current_supabase_key}
else
  read -r -s -p 'Supabase publishable/public key: ' supabase_public_key
  printf '\n'
fi
if [[ ${NLI_SUPABASE_ALLOW_INSECURE:-0} == 1 ]]; then
  [[ $supabase_url =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?/?$ ]] || fail 'invalid Supabase test URL'
  storage_allow_insecure=true
else
  [[ $supabase_url =~ ^https://[a-z0-9-]+\.supabase\.co/?$ ]] || fail 'Supabase project URL must be an absolute https://*.supabase.co URL'
  storage_allow_insecure=false
fi
supabase_url=${supabase_url%/}
[[ $(LC_ALL=C printf '%s' "$supabase_public_key" | wc -c) -ge 32 ]] || fail 'Supabase publishable/public key is missing or too short'
supabase_controller_secret=$(read_env NLI_SUPABASE_CONTROLLER_SECRET)
if [[ $(LC_ALL=C printf '%s' "$supabase_controller_secret" | wc -c) -lt 32 ]]; then
  supabase_controller_secret=$(random_secret)
fi
valid_env_secret "$supabase_controller_secret" || fail 'Supabase controller secret is not safe for a systemd EnvironmentFile'
supabase_storage_secret=${NLI_SETUP_SUPABASE_SECRET_KEY:-$(read_env SUPABASE_SECRET_KEY)}
if [[ -z $supabase_storage_secret ]]; then
  read -r -s -p 'Supabase server-only secret key (sb_secret_...; stored only on controller): ' supabase_storage_secret
  printf '\n'
fi
[[ $supabase_storage_secret == sb_secret_* ]] || fail 'Supabase Storage requires a server-only sb_secret_ key; publishable/anon keys are not accepted'
[[ $(LC_ALL=C printf '%s' "$supabase_storage_secret" | wc -c) -ge 20 ]] || fail 'Supabase secret key is too short'
valid_env_secret "$supabase_storage_secret" || fail 'Supabase secret key is not safe for a systemd EnvironmentFile'
# assurance_key was resolved before deployment-mode selection from the existing
# environment or state file.  Do not regenerate it merely because Supabase is
# selected: retained Assurance fixtures must remain decryptable after an
# upgrade or reconfiguration.
valid_env_secret "$assurance_key" || fail 'Assurance encryption key is not safe for a systemd EnvironmentFile'
supabase_secret_sha256=$(printf '%s' "$supabase_controller_secret" | sha256sum | awk '{print $1}')
NLI_SETUP_SUPABASE_HASH=$supabase_secret_sha256 python3 - "$SUPABASE_SCHEMA_TEMPLATE" "$SUPABASE_BOOTSTRAP_FILE" <<'PY'
import os, pathlib, sys
source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
placeholder = "__NLI_CONTROLLER_SECRET_SHA256__"
if source.count(placeholder) != 1:
    raise SystemExit("Supabase schema template placeholder is missing or repeated")
rendered = source.replace(placeholder, os.environ["NLI_SETUP_SUPABASE_HASH"])
target = pathlib.Path(sys.argv[2])
target.write_text(rendered, encoding="utf-8")
target.chmod(0o600)
PY
fi

if [[ -z $quarantine_retention_enabled ]]; then
  if [[ -n $existing_quarantine_key ]]; then
    if ! read -r -p 'Keep encrypted controller-side raw quarantine retention enabled? [Y/n]: ' quarantine_answer; then
      quarantine_answer=''
      info 'optional quarantine-retention input ended; preserving the enabled setting'
    fi
    case ${quarantine_answer,,} in n|no) quarantine_retention_enabled=false ;; *) quarantine_retention_enabled=true ;; esac
  else
    if ! read -r -p 'Enable encrypted controller-side raw quarantine retention for operator downloads/replay review? [y/N]: ' quarantine_answer; then
      quarantine_answer=''
      info 'optional quarantine-retention input ended; applying the disabled default'
    fi
    case ${quarantine_answer,,} in y|yes) quarantine_retention_enabled=true ;; *) quarantine_retention_enabled=false ;; esac
  fi
fi
[[ $quarantine_retention_enabled == true || $quarantine_retention_enabled == false ]] || fail 'NLI_SETUP_QUARANTINE_RAW_RETENTION must be true or false'
if [[ $quarantine_retention_enabled == true ]]; then
  quarantine_key=${existing_quarantine_key:-$(random_secret)}
  valid_env_secret "$quarantine_key" || fail 'quarantine encryption key is not safe for a systemd EnvironmentFile'
fi

umask 077
env_tmp=$(mktemp "${ENV_FILE}.XXXXXX")
NLI_SETUP_ENROLLMENT="$enrollment_token" \
NLI_SETUP_SUPABASE_URL="$supabase_url" \
NLI_SETUP_SUPABASE_PUBLISHABLE_KEY="$supabase_public_key" \
NLI_SETUP_SUPABASE_SECRET="$supabase_controller_secret" \
NLI_SETUP_STORAGE_SECRET="$supabase_storage_secret" \
NLI_SETUP_ASSURANCE_KEY="$assurance_key" \
NLI_SETUP_AUDIT_KEY="$audit_key" \
NLI_SETUP_CSRF_KEY="$csrf_key" \
NLI_SETUP_ARTIFACT_PUBLIC_KEYS="$artifact_public_keys" \
NLI_SETUP_QUARANTINE_KEY="$quarantine_key" \
NLI_SETUP_QUARANTINE_ENABLED="$quarantine_retention_enabled" \
python3 - "$ENV_FILE" "$env_tmp" <<'PY'
import os, pathlib, sys
source = pathlib.Path(sys.argv[1])
target = pathlib.Path(sys.argv[2])
lines = source.read_text(encoding="utf-8").splitlines() if source.exists() else []
updates = {
    "NLI_ENROLLMENT_TOKEN": os.environ["NLI_SETUP_ENROLLMENT"],
    "NLI_AUDIT_HMAC_KEY": os.environ["NLI_SETUP_AUDIT_KEY"],
    "NLI_CSRF_HMAC_KEY": os.environ["NLI_SETUP_CSRF_KEY"],
    "NLI_ASSURANCE_ENCRYPTION_KEY": os.environ["NLI_SETUP_ASSURANCE_KEY"],
    "NLI_ARTIFACT_ED25519_PUBLIC_KEYS": os.environ["NLI_SETUP_ARTIFACT_PUBLIC_KEYS"],
    "SUPABASE_URL": os.environ["NLI_SETUP_SUPABASE_URL"],
    "SUPABASE_PUBLISHABLE_KEY": os.environ["NLI_SETUP_SUPABASE_PUBLISHABLE_KEY"],
    "NLI_SUPABASE_CONTROLLER_SECRET": os.environ["NLI_SETUP_SUPABASE_SECRET"],
    "SUPABASE_SECRET_KEY": os.environ["NLI_SETUP_STORAGE_SECRET"],
}
if os.environ["NLI_SETUP_QUARANTINE_ENABLED"] == "true":
    updates["NLI_QUARANTINE_ENCRYPTION_KEY"] = os.environ["NLI_SETUP_QUARANTINE_KEY"]
else:
    updates["NLI_QUARANTINE_ENCRYPTION_KEY"] = ""
updates["NLI_BOOTSTRAP_ADMIN_PASSWORD"] = ""
seen = set()
out = []
for line in lines:
    key = line.split("=", 1)[0].strip() if "=" in line and not line.lstrip().startswith("#") else ""
    if key in updates:
        out.append(f"{key}={updates[key]}")
        seen.add(key)
    else:
        out.append(line)
for key, value in updates.items():
    if key not in seen:
        out.append(f"{key}={value}")
target.write_text("\n".join(out) + "\n", encoding="utf-8")
PY
install -o root -g nli -m 0640 "$env_tmp" "$ENV_FILE"
rm -f "$env_tmp"

# Persist one authoritative root-only handoff record. Reconfiguration and RPM
# upgrades preserve controller.env; this snapshot makes recovery explicit
# without exposing credentials in terminal output or process arguments.
credential_tmp=$(mktemp "${CREDENTIAL_FILE}.XXXXXX")
cat >"$credential_tmp" <<EOF
# NLI V20 controller credentials. Root only. Do not copy to clients.
NLI_ENROLLMENT_TOKEN=$enrollment_token
NLI_AUDIT_HMAC_KEY=$audit_key
NLI_CSRF_HMAC_KEY=$csrf_key
NLI_ASSURANCE_ENCRYPTION_KEY=$assurance_key
NLI_ARTIFACT_ED25519_PUBLIC_KEYS=$artifact_public_keys
NLI_QUARANTINE_ENCRYPTION_KEY=$quarantine_key
SUPABASE_URL=$supabase_url
SUPABASE_PUBLISHABLE_KEY=$supabase_public_key
NLI_SUPABASE_CONTROLLER_SECRET=$supabase_controller_secret
SUPABASE_SECRET_KEY=$supabase_storage_secret
EOF
install -o root -g root -m 0600 "$credential_tmp" "$CREDENTIAL_FILE"
rm -f "$credential_tmp"
info "controller credentials preserved at $CREDENTIAL_FILE (root-only; values are intentionally not printed)"

if [[ -n $admin_password ]]; then
  install -d -o nli -g nli -m 0750 "$STATE_DIR"
  password_tmp=$(mktemp "${BOOTSTRAP_PASSWORD_FILE}.XXXXXX")
  printf '%s' "$admin_password" >"$password_tmp"
  install -o root -g nli -m 0640 "$password_tmp" "$BOOTSTRAP_PASSWORD_FILE"
  rm -f "$password_tmp"
fi

NLI_SETUP_LISTEN=$listen_address NLI_SETUP_CERT=$tls_cert NLI_SETUP_KEY=$tls_key \
NLI_SETUP_FORCE_SECURE=$force_secure NLI_SETUP_ALLOW_INSECURE=$allow_insecure_remote \
NLI_SETUP_STORAGE_PROVIDER=$storage_provider NLI_SETUP_STORAGE_INSECURE=$storage_allow_insecure \
NLI_SETUP_QUARANTINE_ENABLED=$quarantine_retention_enabled \
python3 - "$CONFIG" <<'PY'
import json, os, pathlib, sys
path = pathlib.Path(sys.argv[1])
value = json.loads(path.read_text(encoding="utf-8"))
value["listen_address"] = os.environ["NLI_SETUP_LISTEN"]
value["tls_cert"] = os.environ["NLI_SETUP_CERT"]
value["tls_key"] = os.environ["NLI_SETUP_KEY"]
value["force_secure_cookies"] = os.environ["NLI_SETUP_FORCE_SECURE"] == "true"
value["allow_insecure_remote"] = os.environ["NLI_SETUP_ALLOW_INSECURE"] == "true"
value["storage_provider"] = os.environ["NLI_SETUP_STORAGE_PROVIDER"]
value["storage_allow_insecure"] = os.environ["NLI_SETUP_STORAGE_INSECURE"] == "true"
value["assurance_retention_enabled"] = True
value["assurance_retention_manual"] = True
value["quarantine_raw_retention_enabled"] = os.environ["NLI_SETUP_QUARANTINE_ENABLED"] == "true"
value["quarantine_raw_retention_max_records"] = 1000
value["quarantine_raw_retention_hours"] = 24
value["quarantine_raw_retention_key_env"] = "NLI_QUARANTINE_ENCRYPTION_KEY"
tmp = path.with_suffix(path.suffix + ".tmp")
tmp.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
tmp.chmod(0o640)
tmp.replace(path)
PY
chown root:nli "$CONFIG"
chmod 0640 "$CONFIG"

if [[ $mode == 1 ]]; then
  health_args=(--fail --silent --show-error "${controller_url}/api/v1/health")
elif [[ $mode == 2 ]]; then
  health_args=(--fail --silent --show-error --cacert "$ca_cert" --resolve "${endpoint}:${PORT}:127.0.0.1" "${controller_url}/api/v1/health")
elif [[ $mode == 6 ]]; then
  health_args=(--fail --silent --show-error --resolve "${endpoint}:${PORT}:127.0.0.1")
  [[ -n $ca_cert ]] && health_args+=(--cacert "$ca_cert")
  health_args+=("${controller_url}/api/v1/health")
elif [[ $mode == 3 || $mode == 9 ]]; then
  health_args=(--fail --silent --show-error --max-time 10 --resolve "${endpoint}:443:127.0.0.1" "${controller_url}/api/v1/health")
else
  if [[ $mode == 10 ]]; then
    health_args=(--fail --silent --show-error --max-time 10 "http://127.0.0.1:${PORT}/api/v1/health")
  else
    health_args=(--fail --silent --show-error --max-time 10 "${controller_url}/api/v1/health")
  fi
fi
wait_for_controller() {
  local health_json=''
  for _ in $(seq 1 30); do
    if systemctl is-active --quiet "$SERVICE" && health_json=$(curl "${health_args[@]}" 2>/dev/null); then break; fi
    sleep 1
  done
  systemctl is-active --quiet "$SERVICE" || { journalctl -u "$SERVICE" --no-pager -n 80 >&2 || true; fail 'controller service did not become active; the journal above contains the startup cause'; }
  if ! ss -lnt | awk -v port=":${PORT}" '$4 ~ port "$" {found=1} END {exit !found}'; then
    journalctl -u "$SERVICE" --no-pager -n 80 >&2 || true
    fail "controller is running but TCP ${PORT} is not listening; verify listen_address, certificate/key readability, and SELinux policy"
  fi
  health_json=$(curl "${health_args[@]}") || {
    info "TCP ${PORT} is listening, but the authenticated TLS health probe failed. Check that the URL identity appears in the certificate SAN and that local/upstream firewalls permit the selected port."
    openssl x509 -in "$tls_cert" -noout -subject -ext subjectAltName 2>/dev/null >&2 || true
    fail 'controller health endpoint failed'
  }
  printf '%s' "$health_json" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("service")=="nli-controller-agent" and d.get("api_version")=="v1"' || fail 'health endpoint is not an NLI v20 controller'
}

verify_supabase() {
  local result=''
  result=$(curl --fail --silent --show-error --max-time 15 \
    -H 'Content-Type: application/json' \
    -H "apikey: $supabase_public_key" \
    -H "X-NLI-Controller-Secret: $supabase_controller_secret" \
    -d '{}' "$supabase_url/rest/v1/rpc/nli_health") || return 1
  printf '%s' "$result" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("status")=="ok" and d.get("schema_version")==1'
}

if [[ $storage_provider == supabase ]]; then
if ! verify_supabase; then
  info 'Supabase RPC verification failed; the schema may be missing, credentials may be invalid, or the service may be unreachable.'
  info "Open the Supabase project SQL Editor, run the complete file $SUPABASE_BOOTSTRAP_FILE, then return here."
  info 'The generated SQL contains only a SHA-256 secret digest; it never contains the controller secret or admin password.'
  if [[ -t 0 ]]; then
    read -r -p 'Press Enter after the SQL Editor reports success: ' _
  else
    fail "run $SUPABASE_BOOTSTRAP_FILE in Supabase SQL Editor, then rerun sudo nli-controller-setup"
  fi
  verify_supabase || fail 'Supabase schema verification still failed; review the SQL Editor result and project URL/key'
fi
info 'Supabase protected RPC storage is healthy'
else
  info 'explicit local metadata storage selected'
fi

systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null
if ! systemctl restart "$SERVICE"; then
  journalctl -u "$SERVICE" --no-pager -n 80 >&2 || true
  fail 'controller service failed to start'
fi
if [[ $mode == 4 ]]; then
  systemctl enable --now nli-cloudflared.service || fail 'Cloudflare Tunnel connector failed to start; inspect systemctl status nli-cloudflared'
fi
wait_for_controller

if [[ $mode == 10 ]]; then
  tunnel_started=$(date --iso-8601=seconds)
  systemctl restart nli-cloudflared-quick.service || fail 'Cloudflare Quick Tunnel failed to start; inspect systemctl status and journalctl -u nli-cloudflared-quick'
  quick_url=''
  for _ in $(seq 1 30); do
    quick_url=$(journalctl -u nli-cloudflared-quick.service --since "$tunnel_started" --no-pager -o cat 2>/dev/null | grep -Eo 'https://[a-zA-Z0-9-]+\.trycloudflare\.com' | tail -n 1 || true)
    [[ -n $quick_url ]] && break
    systemctl is-active --quiet nli-cloudflared-quick.service || break
    sleep 1
  done
  [[ -n $quick_url ]] || { journalctl -u nli-cloudflared-quick.service --no-pager -n 80 >&2 || true; fail 'Quick Tunnel URL was not announced; check outbound network policy and rerun setup'; }
  endpoint=${quick_url#https://}
  controller_url=$quick_url
  curl --fail --silent --show-error --max-time 20 "${controller_url}/api/v1/health" >/dev/null || fail 'Quick Tunnel did not reach the controller health endpoint; inspect cloudflared and controller journals'
fi

cat >"$BOOTSTRAP_FILE" <<EOF
NLI_CONTROLLER_URL=$controller_url
NLI_CONTROLLER_CA_BASE64=$ca_base64
NLI_ENROLLMENT_TOKEN=$enrollment_token
EOF
chmod 0600 "$BOOTSTRAP_FILE"

# The bootstrap password is needed only while users.json is first created.
# Remove the protected one-time file and prove the controller restarts without it.
if [[ -f $BOOTSTRAP_PASSWORD_FILE ]]; then
  rm -f "$BOOTSTRAP_PASSWORD_FILE"
  if ! systemctl restart "$SERVICE"; then
    journalctl -u "$SERVICE" --no-pager -n 80 >&2 || true
    fail 'controller failed after clearing the one-time bootstrap password'
  fi
  wait_for_controller
  info 'removed the one-time bootstrap admin password file'
fi

if [[ $mode == 2 || $mode == 6 ]]; then
  if command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="${PORT}/tcp" >/dev/null || fail "could not open TCP ${PORT} in firewalld"
    firewall-cmd --reload >/dev/null || fail 'firewalld reload failed'
    firewall-cmd --query-port="${PORT}/tcp" >/dev/null || fail "firewalld did not confirm TCP ${PORT}"
    info "firewalld allows TCP ${PORT}; restrict the source network to managed clients in production"
  else
    info "firewalld is not active; allow TCP ${PORT} from client subnets in the host, VMware, and upstream firewalls"
  fi
  info "connectivity check from a client: nc -vz ${endpoint} ${PORT}"
  if curl -fsS --connect-timeout 1 --max-time 2 -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token >/dev/null 2>&1; then
    info "AWS detected: the EC2 security group must allow inbound TCP ${PORT} from the client security group or private CIDR; host firewalld cannot open the AWS security group"
  fi
fi

# Remove prior NLI-managed exposure only after the selected mode passes health.
remove_prior_proxy_site() {
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
      fail "could not safely remove the previous NLI proxy site $site; its previous configuration was restored"
    fi
  fi
  rm -f -- "$backup"
}
if [[ $mode != 3 && -f /etc/nginx/conf.d/nli-controller.conf ]]; then
  remove_prior_proxy_site /etc/nginx/conf.d/nli-controller.conf nginx nginx.service
fi
if [[ $mode != 3 && $mode != 9 && -f /etc/systemd/system/nli-certbot-renew.timer ]]; then
  systemctl disable --now nli-certbot-renew.timer >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/nli-certbot-renew.timer /etc/systemd/system/nli-certbot-renew.service
  systemctl daemon-reload
fi
if [[ $mode != 9 && -f /etc/httpd/conf.d/nli-controller.conf ]]; then
  remove_prior_proxy_site /etc/httpd/conf.d/nli-controller.conf httpd httpd.service
fi
if [[ $mode != 4 && -f /etc/systemd/system/nli-cloudflared.service ]]; then
  systemctl disable --now nli-cloudflared.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/nli-cloudflared.service /etc/nli/cloudflare/tunnel-token
  systemctl daemon-reload
fi
if [[ $mode != 10 && -f /etc/systemd/system/nli-cloudflared-quick.service ]]; then
  systemctl disable --now nli-cloudflared-quick.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/nli-cloudflared-quick.service
  systemctl daemon-reload
fi

admin_password=''
confirmation=''
enrollment_token=''
supabase_public_key=''
supabase_controller_secret=''
supabase_storage_secret=''
assurance_key=''
info "PASS: controller is healthy at $controller_url"
proxy_service=none
public_origin=''
edge_listeners=()
selinux_manifest_managed=$selinux_proxy_managed
selinux_manifest_changed=$previous_selinux_changed
selinux_manifest_prior_current=$previous_selinux_prior_current
selinux_manifest_prior_persistent=$previous_selinux_prior_persistent
if [[ $selinux_proxy_changed_this_run == 1 ]]; then
  selinux_manifest_managed=true
  selinux_manifest_changed=true
  if [[ $previous_selinux_changed != true ]]; then
    selinux_manifest_prior_current=$selinux_setup_prior_current
    selinux_manifest_prior_persistent=$selinux_setup_prior_persistent
  fi
elif [[ $previous_selinux_changed == true ]]; then
  selinux_manifest_managed=true
fi
case $selected_mode in
  2|6|7|8)
    edge_listeners=("${PORT}/tcp")
    ;;
  3)
    proxy_service=nginx
    public_origin=$controller_url
    tls_cert="/etc/letsencrypt/live/$endpoint/fullchain.pem"
    edge_listeners=(80/tcp 443/tcp)
    ;;
  4)
    proxy_service=cloudflared
    public_origin=$controller_url
    ;;
  5)
    proxy_service=external
    public_origin=$controller_url
    ;;
  9)
    proxy_service=httpd
    public_origin=$controller_url
    tls_cert="/etc/letsencrypt/live/$endpoint/fullchain.pem"
    edge_listeners=(80/tcp 443/tcp)
    ;;
  10)
    proxy_service=cloudflared
    public_origin=$controller_url
    ;;
esac
NLI_EDGE_MODE=$selected_mode \
NLI_EDGE_PROXY_SERVICE=$proxy_service \
NLI_EDGE_PUBLIC_ORIGIN=$public_origin \
NLI_EDGE_CONTROLLER_LISTENER=$listen_address \
NLI_EDGE_LISTENERS="${edge_listeners[*]}" \
NLI_EDGE_TLS_CERT_PATH=${tls_cert:-} \
NLI_EDGE_TLS_CA_PATH=${ca_cert:-} \
NLI_EDGE_SELINUX_MANAGED=$selinux_manifest_managed \
NLI_EDGE_SELINUX_CHANGED=$selinux_manifest_changed \
NLI_EDGE_SELINUX_PRIOR_CURRENT=$selinux_manifest_prior_current \
NLI_EDGE_SELINUX_PRIOR_PERSISTENT=$selinux_manifest_prior_persistent \
NLI_EDGE_CONFIGURED_AT=$(date --utc --iso-8601=seconds) \
python3 - /etc/nli/controller-edge.json <<'PY'
import json, os, pathlib, tempfile

target = pathlib.Path(os.sys.argv[1])
target.parent.mkdir(parents=True, exist_ok=True)
value = {
    "mode": int(os.environ["NLI_EDGE_MODE"]),
    "proxy_service": os.environ["NLI_EDGE_PROXY_SERVICE"],
    "public_origin": os.environ["NLI_EDGE_PUBLIC_ORIGIN"],
    "controller_listener": os.environ["NLI_EDGE_CONTROLLER_LISTENER"],
    "edge_listeners": os.environ["NLI_EDGE_LISTENERS"].split(),
    "tls_cert_path": os.environ["NLI_EDGE_TLS_CERT_PATH"],
    "tls_ca_path": os.environ["NLI_EDGE_TLS_CA_PATH"],
    "selinux_proxy_boolean_managed": os.environ["NLI_EDGE_SELINUX_MANAGED"] == "true",
    "selinux_proxy_boolean_changed": os.environ["NLI_EDGE_SELINUX_CHANGED"] == "true",
    "selinux_proxy_boolean_prior_current": os.environ["NLI_EDGE_SELINUX_PRIOR_CURRENT"],
    "selinux_proxy_boolean_prior_persistent": os.environ["NLI_EDGE_SELINUX_PRIOR_PERSISTENT"],
    "configured_at": os.environ["NLI_EDGE_CONFIGURED_AT"],
}
fd, temporary = tempfile.mkstemp(prefix=".controller-edge.", dir=target.parent)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.chmod(temporary, 0o644)
    os.replace(temporary, target)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
chown root:root /etc/nli/controller-edge.json
chmod 0644 /etc/nli/controller-edge.json
info 'wrote non-secret controller edge manifest to /etc/nli/controller-edge.json'
if [[ $mode == 3 ]]; then
  trap - EXIT
  rm -f "$nginx_backup"
  discard_renewal_state_backup
elif [[ $mode == 9 ]]; then
  trap - EXIT
  rm -f "$previous_vhost"
  discard_renewal_state_backup
elif [[ $mode == 10 ]]; then
  trap - EXIT
  rm -f "$quick_service_backup"
fi
added_firewall_http=0; added_firewall_https=0
added_firewall_http_permanent=0; added_firewall_https_permanent=0
if [[ $mode == 3 || $mode == 4 || $mode == 5 || $mode == 9 || $mode == 10 ]]; then
  info "NLI_CONTROLLER_ORIGIN=$controller_url"
  info "Vercel production environment line: NLI_CONTROLLER_ORIGIN=$controller_url"
fi
info "PASS: System Health storage mode is $storage_provider and Assurance retention is encrypted/manual"
info "$BOOTSTRAP_FILE is mode 0600 and valid for exactly one successful client registration"
if [[ -n ${NLI_CONTROLLER_PROFILE_FILE:-} && ${NLI_KEEP_BOOTSTRAP:-0} != 1 ]]; then
  rm -f -- "$NLI_CONTROLLER_PROFILE_FILE"
  info 'removed the local controller bootstrap profile after successful setup'
fi
info 'for each additional client, rerun: sudo nli-controller-setup --rotate-enrollment'
if [[ $mode == 1 ]]; then
  info 'from Windows run Open-NLI-WebView.ps1 to create the SSH tunnel and open the verified controller UI'
elif [[ $mode == 2 || $mode == 6 ]]; then
  info 'install the private CA on authorized administrator workstations before opening the HTTPS WebView'
else
  if [[ $mode == 10 ]]; then
    info 'temporary test only: the Quick Tunnel URL is random and stops working when cloudflared stops; restarting creates a different URL and requires a fresh client bootstrap'
    info 'the controller remains bound to loopback; Quick Tunnel requires no domain, account, or inbound firewall ports'
  else
    info "controller remains bound to 127.0.0.1:${PORT}; configure the selected HTTPS proxy to forward to http://127.0.0.1:${PORT} and publish DNS ${endpoint}"
    info 'the public URL and enrollment bootstrap use HTTPS; keep proxy-to-controller traffic on loopback and do not enable proxy TLS bypasses or public port 12382'
  fi
fi
