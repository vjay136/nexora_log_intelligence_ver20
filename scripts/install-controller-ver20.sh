#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Install and configure the NLI v20 controller on Rocky Linux 9 or 10.

Usage:
  sudo bash install-controller-ver20.sh [--rpm /path/to/nli_controller_agent_ver20.rpm]
  sudo bash install-controller-ver20.sh --quickstart [--rpm /path/to/nli_controller_agent_ver20.rpm]
  sudo bash install-controller-ver20.sh --automatic [--rpm /path/to/nli_controller_agent_ver20.rpm]
  sudo bash install-controller-ver20.sh --mode 3|9 [--rpm /path/to/nli_controller_agent_ver20.rpm]
  sudo bash install-controller-ver20.sh --mode 10 [--rpm /path/to/nli_controller_agent_ver20.rpm]
  sudo bash install-controller-ver20.sh --rotate-enrollment [--rpm /path/to/rpm]
  sudo bash install-controller-ver20.sh --verify-only [--rpm /path/to/rpm]

With no --rpm option, the script finds nli_controller_agent_ver20.rpm beside
this script or in the current directory. Configuration is collected through
the packaged interactive nli-controller-setup utility.

--quickstart performs a prompt-free restricted-network setup. It prefers a
usable host FQDN and otherwise selects the first non-loopback IPv4 address. It
first checks for a matching trusted server certificate/key, safely falls back
to a unique private CA and TLS certificate, uses local controller storage,
writes a random initial admin password to /root/nli-initial-admin-password.txt,
and writes the one-use client bootstrap to /root/nli-client-bootstrap.env.
--automatic is an alias for --quickstart for MNC, cloud, VMware, and local
installations. Unique secrets are generated once and preserved across upgrades;
no universal product secret is embedded in the RPM.

--mode selects a guided setup deployment mode. Modes 3 and 9 require an
already-routable public IPv4, matching DNS, and inbound TCP 80/443 for Nginx
or Apache ACME. Mode 10 starts a temporary Cloudflare Quick Tunnel for testing;
it needs no domain and its URL changes whenever the tunnel process restarts.
EOF
}

fail() { printf 'install-controller-ver20: %s\n' "$*" >&2; exit 1; }
info() { printf 'install-controller-ver20: %s\n' "$*"; }

validate_rpm_release() {
  local value=$1 label=$2 expected=$3 numeric
  numeric=${value%%.*}
  [[ $numeric == "$expected" ]] ||
    fail "the selected $label RPM release $value does not match this bundle release $expected"
}

load_release_identity() {
  local marker=$1
  [[ -f $marker ]] || fail 'RELEASE_ID is missing; extract the complete versioned release archive into a fresh directory'
  expected_version=$(awk -F= '$1=="NLI_VERSION" {print $2; exit}' "$marker")
  expected_release=$(awk -F= '$1=="NLI_RELEASE" {print $2; exit}' "$marker")
  expected_arch=$(awk -F= '$1=="NLI_ARCH" {print $2; exit}' "$marker")
  expected_revision=$(awk -F= '$1=="SOURCE_REVISION" {print $2; exit}' "$marker")
  [[ $expected_version == 20.0.0 ]] || fail "invalid bundle version in RELEASE_ID: $expected_version"
  [[ $expected_release =~ ^[1-9][0-9]*$ ]] || fail "invalid bundle release in RELEASE_ID: $expected_release"
  [[ $expected_arch == x86_64 ]] || fail "invalid bundle architecture in RELEASE_ID: $expected_arch"
  [[ $expected_revision =~ ^[0-9a-f]{40}$ ]] || fail 'invalid source revision in RELEASE_ID'
}

verify_release_bundle() {
  local checksum_file=$1 required
  [[ -f $checksum_file ]] || fail 'SHA256SUMS is missing; extract the complete versioned release archive into a fresh directory'
  for required in RELEASE_ID nli_client_ver20.rpm nli_clinet_ver20.rpm nli_controller_agent_ver20.rpm \
    webview_ver20.zip webapp_ver20.zip install-controller-ver20.sh install-client-ver20.sh \
    uninstall-controller-ver20.sh uninstall-client-ver20.sh INSTALLATION_AND_CONFIGURATION.md \
    WEBAPP_OPERATOR_AND_PARSER_GUIDE.md V20_OPERATOR_DEMO_GUIDE.md V20_PRODUCTION_ACCEPTANCE_TESTS.md V20_SUBSYSTEM_ORCHESTRATION.md \
    BENCHMARKS.txt SAMPLE_FIELD_ACCOUNTING.json; do
    awk -v name="$required" '$2==name && $1 ~ /^[0-9a-f]{64}$/ {found=1} END {exit !found}' "$checksum_file" ||
      fail "SHA256SUMS is incomplete: missing $required"
  done
  (cd "$script_dir" && sha256sum -c SHA256SUMS) || fail 'release bundle checksum verification failed; use a fresh extraction of one release archive'
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
rpm_path=''
rotate_enrollment=0
verify_only=0
quickstart=0
deployment_mode=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --rpm)
      [[ $# -ge 2 ]] || fail '--rpm requires a file path'
      rpm_path=$2
      shift 2
      ;;
    --rotate-enrollment)
      rotate_enrollment=1
      shift
      ;;
    --quickstart|--automatic)
      quickstart=1
      shift
      ;;
    --mode)
      [[ $# -ge 2 ]] || fail '--mode requires a deployment mode from 1 through 10'
      [[ $2 =~ ^([1-9]|10)$ ]] || fail '--mode must be between 1 and 10'
      deployment_mode=$2
      shift 2
      ;;
    --verify-only)
      verify_only=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unexpected argument: $1"
      ;;
  esac
done

[[ $quickstart -eq 0 || -z $deployment_mode ]] || fail '--quickstart and --mode cannot be combined'

[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run as root with sudo'
required_commands=(awk rpm sha256sum readlink)
[[ $verify_only -eq 1 ]] || required_commands+=(dnf systemctl)
for command in "${required_commands[@]}"; do command -v "$command" >/dev/null || fail "$command is required"; done
load_release_identity "$script_dir/RELEASE_ID"
verify_release_bundle "$script_dir/SHA256SUMS"

if [[ -z $rpm_path ]]; then
  for candidate in "$script_dir/nli_controller_agent_ver20.rpm" "$PWD/nli_controller_agent_ver20.rpm"; do
    if [[ -f $candidate ]]; then rpm_path=$candidate; break; fi
  done
fi
if [[ -z $rpm_path && -t 0 ]]; then
  read -r -p 'Path to nli_controller_agent_ver20.rpm: ' rpm_path
fi
[[ -f $rpm_path ]] || fail 'controller RPM was not found; place it beside this script or pass --rpm PATH'
rpm_path=$(readlink -f -- "$rpm_path")

[[ $(rpm -qp --qf '%{NAME}' "$rpm_path") == nli-controller-agent ]] || fail 'the selected file is not the NLI controller RPM'
[[ $(rpm -qp --qf '%{VERSION}' "$rpm_path") == "$expected_version" ]] || fail "the selected controller RPM is not version $expected_version"
rpm_release=$(rpm -qp --qf '%{RELEASE}' "$rpm_path")
validate_rpm_release "$rpm_release" controller "$expected_release"
[[ $(rpm -qp --qf '%{ARCH}' "$rpm_path") == "$expected_arch" ]] || fail "the selected controller RPM is not $expected_arch"

checksum_file="$script_dir/SHA256SUMS"
expected=$(awk '$2=="nli_controller_agent_ver20.rpm" {print $1; exit}' "$checksum_file")
actual=$(sha256sum "$rpm_path" | awk '{print $1}')
[[ $actual == "$expected" ]] || fail 'controller RPM SHA-256 does not match this release bundle'
info 'controller RPM SHA-256 verified'

if [[ $verify_only -eq 1 ]]; then
  info "PASS: controller RPM identity and complete bundle verified ($(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' "$rpm_path"), source $expected_revision)"
  exit 0
fi

info "installing $(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' "$rpm_path")"
dnf -y install "$rpm_path"

setup_args=()
[[ $rotate_enrollment -eq 1 ]] && setup_args+=(--rotate-enrollment)
if [[ $quickstart -eq 1 ]]; then
  quick_fqdn=$(hostname -f 2>/dev/null || true)
  if [[ $quick_fqdn == *.* && $quick_fqdn != localhost.localdomain && $quick_fqdn != localhost ]]; then
    quick_endpoint=$quick_fqdn
  else
    quick_endpoint=$(hostname -I 2>/dev/null | tr ' ' '\n' | awk '$1 ~ /^[0-9]+[.]/ && $1 !~ /^127[.]/ {print; exit}')
  fi
  quick_endpoint=${quick_endpoint:-$(hostname 2>/dev/null || true)}
  [[ -n $quick_endpoint ]] || fail 'quickstart could not determine a controller IP or hostname; use guided setup'
  quick_password_file=/root/nli-initial-admin-password.txt
  if [[ ! -s /var/lib/nli-controller/users.json ]]; then
    umask 077
    openssl rand -base64 24 | tr -d '\r\n' >"$quick_password_file"
    chmod 0600 "$quick_password_file"
  fi
  info "quickstart selected certificate auto-discovery with offline private-TLS fallback for controller identity $quick_endpoint"
  NLI_SETUP_MODE=8 \
  NLI_SETUP_ENDPOINT="$quick_endpoint" \
  NLI_SETUP_TLS_IDENTITIES="$quick_endpoint" \
  NLI_SETUP_STORAGE_PROVIDER=local \
  NLI_SETUP_QUARANTINE_RAW_RETENTION=false \
  NLI_SETUP_ADMIN_PASSWORD_FILE="$quick_password_file" \
    /usr/sbin/nli-controller-setup "${setup_args[@]}"
  if [[ -s $quick_password_file ]]; then
    info "initial admin password file: $quick_password_file (root-only; delete after first login)"
  fi
else
  if [[ -n $deployment_mode ]]; then
    NLI_SETUP_MODE=$deployment_mode /usr/sbin/nli-controller-setup "${setup_args[@]}"
  else
    /usr/sbin/nli-controller-setup "${setup_args[@]}"
  fi
fi

systemctl is-active --quiet nli-controller-agent.service || fail 'controller service is not active after setup'
info 'PASS: controller RPM is installed, configured, and healthy'
info 'one-use client bundle: /root/nli-client-bootstrap.env'
info 'copy that file securely to exactly one client, then run install-client-ver20.sh there'
info 'for another client, rerun this script with --rotate-enrollment or run sudo nli-controller-setup --rotate-enrollment'
