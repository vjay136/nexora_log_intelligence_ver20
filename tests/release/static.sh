#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
MODE=${1:-full}
# shellcheck disable=SC1091
source "$ROOT/packaging/release.env"
# shellcheck disable=SC1091
source "$ROOT/packaging/docker/images.env"

for forbidden in packaging/build-simple.ps1 packaging/build-local.ps1 packaging/build-local.sh packaging/rpm/nli-agent.spec packaging/systemd/nli-control.service packaging/systemd/nli-manager.service; do
  [[ ! -e "$ROOT/$forbidden" ]] || { echo "forbidden legacy path: $forbidden" >&2; exit 1; }
done
bad_archive_pattern='Compress-Archive.*[.]rpm|Rename-Item.*[.]rpm|placeholder[[:space:]]+RPM'
if grep -RInE "$bad_archive_pattern" "$ROOT/packaging" "$ROOT/scripts/release" "$ROOT/tests"; then
  echo 'forbidden archive masquerading as an RPM remains reachable' >&2
  exit 1
fi
while IFS= read -r -d '' script; do bash -n "$script"; done < <(find "$ROOT/packaging" "$ROOT/scripts/release" "$ROOT/tests" -type f -name '*.sh' -print0)
bash -n "$ROOT/scripts/setup-controller-rocky.sh" "$ROOT/scripts/setup-client-rocky.sh" "$ROOT/scripts/setup-supabase-rocky.sh" "$ROOT/scripts/reset-admin-rocky.sh" \
  "$ROOT/scripts/install-controller-ver20.sh" "$ROOT/scripts/install-client-ver20.sh" \
  "$ROOT/scripts/install-controller-aws-ver20.sh" "$ROOT/scripts/install-client-aws-ver20.sh" \
  "$ROOT/scripts/install-controller-aws-cloudflare-ver20.sh" "$ROOT/scripts/aws-rhel-preflight-ver20.sh" \
  "$ROOT/scripts/aws-security-group-plan-ver20.sh" "$ROOT/scripts/setup-cloudflare-tunnel-v20.sh" \
  "$ROOT/scripts/verify-cloudflare-tunnel-v20.sh" "$ROOT/scripts/uninstall-cloudflare-tunnel-v20.sh" \
  "$ROOT/scripts/cloudflare-quick-diagnosis-v20.sh" \
  "$ROOT/scripts/uninstall-controller-ver20.sh" "$ROOT/scripts/uninstall-client-ver20.sh"
for exposure in '3) Public DNS + Nginx + ACME certificate' '4) Cloudflare Tunnel token' '5) Operator-managed reverse proxy' '6) Corporate PKI certificate + direct HTTPS' '7) Offline private TLS (generated private CA' '8) Auto-detect a trusted server certificate/key' '9) Public DNS + Apache HTTP Server + ACME certificate' '10) Temporary Cloudflare Quick Tunnel'; do
  grep -Fq "$exposure" "$ROOT/scripts/setup-controller-rocky.sh" || { echo "controller setup exposure option missing: $exposure" >&2; exit 1; }
done
for tls_guard in '/etc/pki/tls/certs' '/etc/pki/tls/private' '/etc/nginx' '/etc/httpd' '/etc/nli' 'SSL server : Yes' 'CA:[[:space:]]*TRUE' '-checkend 86400' '-checkhost' '-checkip' '-purpose sslserver' 'found_server_key' 'using generated offline private TLS'; do
  grep -Fq -- "$tls_guard" "$ROOT/scripts/setup-controller-rocky.sh" || { echo "controller TLS discovery guard missing: $tls_guard" >&2; exit 1; }
done
! grep -Eq '/etc/(rhsm|pki/consumer|pki/entitlement)' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'controller TLS discovery must not scan entitlement/client certificate stores' >&2; exit 1; }
grep -Fq 'NLI_SETUP_ADMIN_PASSWORD_FILE' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'controller setup must support a protected bootstrap password file' >&2; exit 1; }
! grep -Fq 'updates["NLI_BOOTSTRAP_ADMIN_PASSWORD"] = os.environ' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'controller setup still persists the bootstrap password in its environment file' >&2; exit 1; }
grep -Fq 'listen_address="127.0.0.1:${PORT}"' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'public proxy modes must retain a loopback controller listener' >&2; exit 1; }
grep -Fq -- '--token-file /etc/nli/cloudflare/tunnel-token' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'Cloudflare service must consume its protected token file' >&2; exit 1; }
! grep -Fq -- '--token "$tunnel_token"' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'Cloudflare tunnel token must not appear in process arguments' >&2; exit 1; }
grep -Fq 'nli-certbot-renew.timer' "$ROOT/scripts/setup-controller-rocky.sh" || { echo 'Nginx ACME mode must install persistent certificate renewal' >&2; exit 1; }
python3 "$ROOT/tests/release/controller_edge_contract_test.py" "$ROOT"
python3 "$ROOT/tests/release/controller_edge_selinux_test.py" "$ROOT"
python3 "$ROOT/tests/release/aws_install_contract_test.py" "$ROOT"
node --test "$ROOT/tests/cloudflare-tunnel-v20.test.js" "$ROOT/tests/aws-cloudflare-orchestrator-v20.test.js" "$ROOT/tests/vercel-preflight.test.js"
for operator_script in install-controller-ver20.sh install-client-ver20.sh uninstall-controller-ver20.sh uninstall-client-ver20.sh; do
  bash "$ROOT/scripts/$operator_script" --help | grep -Fq 'Usage:' || { echo "operator help failed: $operator_script" >&2; exit 1; }
done
grep -Fq "Log file path(s)" "$ROOT/scripts/install-client-ver20.sh" || { echo 'client installer does not collect real log paths' >&2; exit 1; }
grep -Fq 'Select one or more destinations using commas' "$ROOT/scripts/install-client-ver20.sh" || { echo 'client installer does not support simultaneous destinations' >&2; exit 1; }
for destination in file loki clickhouse; do
  grep -Fq '"type": "'"$destination"'"' "$ROOT/scripts/install-client-ver20.sh" || { echo "client installer destination missing: $destination" >&2; exit 1; }
done
grep -Fq '"type": "telemetry"' "$ROOT/scripts/install-client-ver20.sh" || { echo 'client installer does not configure protected controller telemetry' >&2; exit 1; }
! grep -Fq '"type": "controller"' "$ROOT/scripts/install-client-ver20.sh" || { echo 'client installer still exposes the legacy raw-event controller destination' >&2; exit 1; }
grep -Fq 'NLI_ADMIN_PASSWORD is required' "$ROOT/web/e2e.spec.js" || { echo 'WebView E2E must require an explicit password' >&2; exit 1; }
! grep -Fq 'Browser-admin-password-12345!' "$ROOT/web/e2e.spec.js" || { echo 'WebView E2E contains a fallback password' >&2; exit 1; }
python3 - "$ROOT/scripts/setup-client-rocky.sh" <<'PY'
import pathlib, sys
source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
health = source.index('if ! health_json=$(curl')
verified = source.index('value["controller_url"] = os.environ["NLI_SETUP_URL"]')
ca_commit = source.index('mv -f -- "$verified_ca" "$CA_FILE"')
if verified < health:
    raise SystemExit("client setup mutates controller trust before the TLS health check")
if ca_commit < health:
    raise SystemExit("client setup replaces the active private CA before verification")
for guard in (
    'preserving verified controller CA from the current configuration',
    'failed setup attempt must never erase the last-known working CA or endpoint',
    'os.fsync(stream.fileno())',
    '${CA_FILE}.candidate.',
):
    if guard not in source:
        raise SystemExit(f"client setup transactional trust guard missing: {guard}")
PY

docker run --rm --mount "type=bind,src=$ROOT,dst=/src,readonly" \
  "$EVIDENCE_IMAGE" sh -lc 'PYTHONPYCACHEPREFIX=/tmp/pycache python -m py_compile /src/scripts/release/generate-evidence.py /src/tests/release/evidence_test.py /src/tests/release/client_installer_config_test.py /src/tests/release/source_parser_provenance_test.py && python /src/tests/release/evidence_test.py && python /src/tests/release/client_installer_config_test.py /src && python /src/tests/release/source_parser_provenance_test.py /src'

rpm_check='bash /src/packaging/docker/install-rpm-tools.sh >/dev/null; dnf -y --disablerepo="*" --enablerepo="nli-vault-*" --setopt=install_weak_deps=False install systemd-252-51.el9_6.3.rocky.0.1.x86_64 >/dev/null; rpmspec -P --define "nli_version ${NLI_VERSION:?}" --define "nli_release ${NLI_RELEASE:?}" /src/packaging/rpm/nli-client.spec >/dev/null; rpmspec -P --define "nli_version ${NLI_VERSION:?}" --define "nli_release ${NLI_RELEASE:?}" /src/packaging/rpm/nli-controller-agent.spec >/dev/null; install -m 0755 /usr/bin/true /usr/bin/nli-agent; install -m 0755 /usr/bin/true /usr/bin/nli-client-control; install -m 0755 /usr/bin/true /usr/bin/nli-controller-agent; install -m 0755 /usr/bin/true /usr/sbin/nli-client-setup; install -m 0755 /usr/bin/true /usr/sbin/nli-controller-setup; install -d /tmp/units; install -m 0644 /src/packaging/systemd/*.service /tmp/units/; systemd-analyze verify /tmp/units/nli-agent.service /tmp/units/nli-client-control.service /tmp/units/nli-client-bootstrap.service /tmp/units/nli-controller-agent.service /tmp/units/nli-controller-bootstrap.service'
docker run --rm --mount "type=bind,src=$ROOT,dst=/src,readonly" -e NLI_VERSION="$NLI_VERSION" -e NLI_RELEASE="$NLI_RELEASE" "$RPM_BUILD_IMAGE" bash -lc "$rpm_check"
edge_config_check='dnf -y install python3 openssl nginx httpd mod_ssl >/dev/null; python3 /src/tests/release/controller_edge_config_integration_test.py /src'
docker run --rm --mount "type=bind,src=$ROOT,dst=/src,readonly" "$RPM_BUILD_IMAGE" bash -lc "$edge_config_check"
bash "$ROOT/tests/supabase/schema-contract.sh"

[[ $MODE == infra-only ]] || bash "$ROOT/scripts/release/preflight.sh"

while IFS='|' read -r unit description; do
  grep -Fxq "Description=$description" "$ROOT/packaging/systemd/$unit" || {
    echo "service metadata has the wrong product name: $unit" >&2
    exit 1
  }
  ! grep -q '^Documentation=' "$ROOT/packaging/systemd/$unit" || {
    echo "service metadata must not show a repository link: $unit" >&2
    exit 1
  }
done <<'EOF'
nli-agent.service|Nexora Log Intelligence - Client Agent
nli-client-control.service|Nexora Log Intelligence - Client
nli-client-bootstrap.service|Nexora Log Intelligence - Client Bootstrap
nli-controller-agent.service|Nexora Log Intelligence - Main Controller
nli-controller-bootstrap.service|Nexora Log Intelligence - Controller Bootstrap
EOF
if grep -RInE 'vjay-hacker|vijay-hacker|vja-hacker' \
  "$ROOT/README.md" "$ROOT/packaging" "$ROOT/scripts" "$ROOT/web" "$ROOT/docs/WEBVIEW_SUPABASE_DEPLOYMENT.md"; then
  echo 'stale GitHub owner remains in release inputs' >&2
  exit 1
fi
python3 - "$ROOT/configs/client-control.json" <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if value.get("controller_url"):
    raise SystemExit("client-control.json must require first-run setup, not ship a controller endpoint")
if "ca_file" not in value:
    raise SystemExit("client-control.json must expose the private-CA setting")
PY
grep -Fq 'method: '\''OPTIONS'\''' "$ROOT/web/app.js" || { echo 'WebView does not verify the login preflight' >&2; exit 1; }
grep -Fq "'x-nli-controller'" "$ROOT/api/v1/[...path].js" || { echo 'Vercel proxy does not preserve controller identity' >&2; exit 1; }
grep -Fq "'allow'" "$ROOT/api/v1/[...path].js" || { echo 'Vercel proxy does not preserve login methods' >&2; exit 1; }
echo "release infrastructure static validation: PASS ($MODE)"
