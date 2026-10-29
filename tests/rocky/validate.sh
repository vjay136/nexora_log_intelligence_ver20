#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_DIR=${ARTIFACT_DIR:-/artifacts}
PREVIOUS_DIR=${PREVIOUS_DIR:-/previous}
TEST_ROOT=${TEST_ROOT:-/tests}
REQUIRE_PREVIOUS=${REQUIRE_PREVIOUS:-1}
fail() { echo "Rocky validation: $*" >&2; exit 1; }

client="$ARTIFACT_DIR/nli_client_ver20.rpm"
alias="$ARTIFACT_DIR/nli_clinet_ver20.rpm"
controller="$ARTIFACT_DIR/nli_controller_agent_ver20.rpm"
for rpm_file in "$client" "$alias" "$controller"; do
  [[ -s $rpm_file ]] || fail "missing $(basename "$rpm_file")"
  [[ $(od -An -tx1 -N4 "$rpm_file" | tr -d ' \n') == edabeedb ]] || fail "invalid RPM magic: $(basename "$rpm_file")"
done
cmp -s "$client" "$alias" || fail "compatibility alias differs from canonical client RPM"
[[ $(rpm -qp --qf '%{NAME}' "$alias") == nli-client ]] || fail "alias creates a conflicting package identity"

previous_client=''
previous_controller=''
if [[ -d $PREVIOUS_DIR ]]; then
  previous_client=$(find "$PREVIOUS_DIR" -maxdepth 1 -type f \( -name 'nli-client-*.rpm' -o -name 'nli_client_*.rpm' \) -print -quit)
  previous_controller=$(find "$PREVIOUS_DIR" -maxdepth 1 -type f \( -name 'nli-controller-agent-*.rpm' -o -name 'nli_controller_agent_*.rpm' \) -print -quit)
fi
if [[ -n $previous_client && -n $previous_controller ]]; then
  [[ $(rpm -qp --qf '%{NAME}' "$previous_client") == nli-client ]] || fail "previous client RPM has unexpected package name"
  [[ $(rpm -qp --qf '%{NAME}' "$previous_controller") == nli-controller-agent ]] || fail "previous controller RPM has unexpected package name"
  previous_client_version=$(rpm -qp --qf '%{VERSION}' "$previous_client")
  previous_controller_version=$(rpm -qp --qf '%{VERSION}' "$previous_controller")
  current_client_version=$(rpm -qp --qf '%{VERSION}' "$client")
  current_controller_version=$(rpm -qp --qf '%{VERSION}' "$controller")
  [[ $previous_client_version == "$current_client_version" ]] || fail "previous client must be a genuine earlier Ver20 RPM"
  [[ $previous_controller_version == "$current_controller_version" ]] || fail "previous controller must be a genuine earlier Ver20 RPM"
  previous_client_nevra=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' "$previous_client")
  previous_controller_nevra=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' "$previous_controller")
  current_client_nevra=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' "$client")
  current_controller_nevra=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' "$controller")
  [[ $previous_client_nevra != "$current_client_nevra" ]] || fail "previous client RPM is identical to the release under test"
  [[ $previous_controller_nevra != "$current_controller_nevra" ]] || fail "previous controller RPM is identical to the release under test"
  previous_client_release=$(rpm -qp --qf '%{RELEASE}' "$previous_client")
  previous_controller_release=$(rpm -qp --qf '%{RELEASE}' "$previous_controller")
  current_client_release=$(rpm -qp --qf '%{RELEASE}' "$client")
  current_controller_release=$(rpm -qp --qf '%{RELEASE}' "$controller")
  [[ ${previous_client_release%%.*} =~ ^[0-9]+$ && ${current_client_release%%.*} =~ ^[0-9]+$ && ${previous_client_release%%.*} -lt ${current_client_release%%.*} ]] || fail "previous client RPM is not older than the release under test"
  [[ ${previous_controller_release%%.*} =~ ^[0-9]+$ && ${current_controller_release%%.*} =~ ^[0-9]+$ && ${previous_controller_release%%.*} -lt ${current_controller_release%%.*} ]] || fail "previous controller RPM is not older than the release under test"
  dnf -y install "$previous_client" "$previous_controller"
  install -d -o nli -g nli -m 0750 /var/lib/nli /var/lib/nli-controller
  echo pre-upgrade-client >/var/lib/nli/release-test.marker
  echo pre-upgrade-controller >/var/lib/nli-controller/release-test.marker
  dnf -y upgrade "$client" "$controller"
  [[ $(cat /var/lib/nli/release-test.marker) == pre-upgrade-client ]] || fail "client state lost on upgrade"
  [[ $(cat /var/lib/nli-controller/release-test.marker) == pre-upgrade-controller ]] || fail "controller state lost on upgrade"
else
  [[ $REQUIRE_PREVIOUS == 0 ]] || fail "real previous client/controller RPMs are required for upgrade and rollback acceptance"
  dnf -y install "$client" "$controller"
fi

[[ $(rpm -q --qf '%{VERSION}' nli-client) == 20.0.0 ]] || fail "client version is not 20.0.0"
[[ $(rpm -q --qf '%{VERSION}' nli-controller-agent) == 20.0.0 ]] || fail "controller version is not 20.0.0"
release_marker="$ARTIFACT_DIR/RELEASE_ID"
[[ -s $release_marker ]] || fail "RELEASE_ID is missing"
expected_release=$(sed -n 's/^NLI_RELEASE=//p' "$release_marker")
[[ $expected_release =~ ^[1-9][0-9]*$ ]] || fail "RELEASE_ID has an invalid NLI_RELEASE: $expected_release"
for package in nli-client nli-controller-agent; do
  package_release=$(rpm -q --qf '%{RELEASE}' "$package")
  [[ $package_release == "$expected_release" || $package_release == "$expected_release".* ]] || fail "$package release does not match RELEASE_ID $expected_release (with optional distro suffix): $package_release"
done
verification=$(rpm -V nli-client nli-controller-agent || true)
unexpected_verification=$(printf '%s\n' "$verification" | grep -vE '/etc/nli/controller\.env$' || true)
[[ -z $unexpected_verification ]] || fail "unexpected rpm -V differences: $unexpected_verification"

for item in \
  '/usr/bin/nli root:root 755' \
  '/usr/bin/nli-agent root:root 755' \
  '/usr/bin/nli-client-control root:root 755' \
  '/usr/bin/nli-controller-agent root:root 755' \
  '/usr/sbin/nli-supabase-setup root:root 755' \
  '/usr/share/nli/supabase/nli_schema.sql root:root 644' \
  '/usr/sbin/nli-admin-reset root:root 755' \
  '/etc/nli/nli.json root:nli 640' \
  '/etc/nli/controller.json root:nli 640' \
  '/var/lib/nli nli:nli 750' \
  '/var/lib/nli-controller nli:nli 750'; do
  read -r path owner mode <<<"$item"
  actual=$(stat -c '%U:%G %a' "$path")
  [[ $actual == "$owner $mode" ]] || fail "$path ownership/mode is $actual, expected $owner $mode"
done

systemd-analyze verify /usr/lib/systemd/system/nli-agent.service /usr/lib/systemd/system/nli-client-control.service /usr/lib/systemd/system/nli-client-bootstrap.service /usr/lib/systemd/system/nli-controller-agent.service /usr/lib/systemd/system/nli-controller-bootstrap.service
cat >/etc/nli/controller.env <<'ENV'
NLI_BOOTSTRAP_ADMIN_PASSWORD=ReleaseTestAdminPassword-2026
NLI_ENROLLMENT_TOKEN=ReleaseTestEnrollmentToken-2026
ENV
chown root:nli /etc/nli/controller.env
chmod 0640 /etc/nli/controller.env
python3 - <<'PY'
import json
path = "/etc/nli/controller.json"
with open(path, encoding="utf-8") as stream:
    value = json.load(stream)
value["storage_provider"] = "local"
value["assurance_retention_enabled"] = False
with open(path, "w", encoding="utf-8") as stream:
    json.dump(value, stream, indent=2, sort_keys=True)
    stream.write("\n")
PY
python3 - <<'PY'
import json
path = "/etc/nli/client-control.json"
with open(path, encoding="utf-8") as stream:
    value = json.load(stream)
value["controller_url"] = "http://127.0.0.1:12382"
value["allow_insecure_http"] = True
value["poll_seconds"] = 5
value["metrics_url"] = "http://127.0.0.1:12380/metrics"
with open(path, "w", encoding="utf-8") as stream:
    json.dump(value, stream, indent=2, sort_keys=True)
    stream.write("\n")
PY
cat >/etc/nli/client-control.env <<'ENV'
NLI_ENROLLMENT_TOKEN=ReleaseTestEnrollmentToken-2026
ENV
chown root:nli /etc/nli/client-control.json /etc/nli/client-control.env
chmod 0640 /etc/nli/client-control.json /etc/nli/client-control.env
for input_name in auto imap pop webtop mta smtp-in smtp-out pabd capd pss; do
  [[ -f /var/log/nli/input/${input_name}.log ]] || fail "RPM did not create input file: ${input_name}.log"
done
assert_services_healthy() {
  local phase=${1:?phase is required}
  for service in nli-controller-agent nli-agent nli-client-control; do
    for _ in $(seq 1 30); do systemctl is-active --quiet "$service" && break; sleep 1; done
    systemctl is-active --quiet "$service" || { journalctl -u "$service" --no-pager -n 100; fail "$service is not active after $phase"; }
  done
  sleep 6
  for service in nli-controller-agent nli-agent nli-client-control; do
    systemctl is-active --quiet "$service" || { journalctl -u "$service" --no-pager -n 100; fail "$service did not remain active after $phase"; }
  done
}

systemctl daemon-reload
systemctl enable --now nli-controller-agent.service nli-agent.service nli-client-control.service
assert_services_healthy initial-start

for output_family in auto imap pop webtop-webmail mta smtp-in smtp-out pabd capd pss; do
  ! compgen -G "/var/log/nli/output/${output_family}_normalised_[0-9]*.jsonl" >/dev/null || fail "agent created an unused normalised output: ${output_family}"
  ! compgen -G "/var/log/nli/output/${output_family}_quarantine_[0-9]*.jsonl" >/dev/null || fail "agent created an unused quarantine output: ${output_family}"
done
[[ -d /var/lib/nli/parsers/quarantine ]] || fail "atomic parser synchronization omitted the runtime quarantine directory"
[[ $(stat -c '%U:%G %a' /var/lib/nli/parsers) == 'root:nli 750' ]] || fail "atomic parser root did not preserve root:nli 0750 ownership"
[[ $(stat -c '%U:%G %a' /var/lib/nli/parsers/quarantine) == 'nli:nli 750' ]] || fail "agent quarantine runtime did not preserve nli:nli 0750 ownership"
while IFS= read -r parser_file; do
  [[ $(stat -c '%U:%G %a' "$parser_file") == 'root:nli 640' ]] || fail "synchronized parser file has unsafe or unreadable ownership: $parser_file"
done < <(find /var/lib/nli/parsers -maxdepth 1 -type f -print)
runuser -u nli -- test -w /var/lib/nli/parsers/quarantine || fail "runtime quarantine directory is not writable by the nli agent"

count_auto_records() {
  local files=()
  shopt -s nullglob
  files=(/var/log/nli/output/auto_normalised_[0-9]*.jsonl /var/log/nli/output/auto_quarantine_[0-9]*.jsonl)
  if (( ${#files[@]} == 0 )); then
    printf '0\n'
  else
    cat "${files[@]}" | wc -l
  fi
}
for index in $(seq 1 49); do
  printf '{"event":"release-lifecycle-%02d","status":"ok"}\n' "$index"
done >>/var/log/nli/input/auto.log
for _ in $(seq 1 30); do
  auto_records=$(count_auto_records)
  (( auto_records <= 49 )) || fail "49 input records produced $auto_records routed records"
  [[ $auto_records == 49 ]] && break
  sleep 1
done
[[ $(count_auto_records) == 49 ]] || fail "49 input records did not produce exactly 49 normalised-plus-quarantine records"
sleep 6
[[ $(count_auto_records) == 49 ]] || fail "idle agent duplicated the 49-record input"
systemctl restart nli-agent
for _ in $(seq 1 30); do systemctl is-active --quiet nli-agent && break; sleep 1; done
systemctl is-active --quiet nli-agent || fail "agent did not restart after exact-count acceptance"
sleep 6
[[ $(count_auto_records) == 49 ]] || fail "agent restart replayed already checkpointed input"

[[ $(nli version) == 'nli 20.0.0' ]] || fail "CLI reports the wrong version"
[[ $(nli-agent --version) == 'nli-agent 20.0.0' ]] || fail "agent reports the wrong version"
[[ $(nli-client-control --version) == 'nli-client-control 20.0.0' ]] || fail "client-control reports the wrong version"
[[ $(nli-controller-agent --version) == 'nli-control 20.0.0' ]] || fail "controller reports the wrong version"

nli parse --service smtp --multiline=false - <"$TEST_ROOT/release/smtp_input.txt" >/tmp/smtp-actual.json
python3 - "$TEST_ROOT/release/smtp_expected.json" /tmp/smtp-actual.json <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: expected = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream: actual = json.load(stream)
if actual != expected:
    raise SystemExit(f"SMTP mismatch\nexpected={expected!r}\nactual={actual!r}")
PY

for helper in /usr/sbin/nli-client-setup /usr/sbin/nli-controller-setup /usr/sbin/nli-supabase-setup /usr/sbin/nli-admin-reset; do
  [[ -x $helper ]] || fail "installed setup helper is missing or not executable: $helper"
done
grep -Fxq 'Description=Nexora Log Intelligence - Client Agent' /usr/lib/systemd/system/nli-agent.service || fail 'client agent service has the wrong display name'
grep -Fxq 'Description=Nexora Log Intelligence - Client' /usr/lib/systemd/system/nli-client-control.service || fail 'client control service has the wrong display name'
grep -Fxq 'Description=Nexora Log Intelligence - Main Controller' /usr/lib/systemd/system/nli-controller-agent.service || fail 'controller service has the wrong display name'
! grep -q '^Documentation=' /usr/lib/systemd/system/nli-agent.service || fail 'client agent service exposes a repository link'
! grep -q '^Documentation=' /usr/lib/systemd/system/nli-client-control.service || fail 'client control service exposes a repository link'
! grep -q '^Documentation=' /usr/lib/systemd/system/nli-controller-agent.service || fail 'controller service exposes a repository link'
! grep -RIEq 'vjay-hacker|vijay-hacker|vja-hacker' /usr/lib/systemd/system/nli-*.service || fail "installed service contains an old GitHub owner"

curl --fail --silent --show-error -D /tmp/health.headers -o /tmp/health.json http://127.0.0.1:12382/api/v1/health
tr -d '\r' </tmp/health.headers | grep -Eiq '^X-NLI-Controller:[[:space:]]*v20$' || fail "health endpoint does not identify the NLI V20 controller"
python3 - /tmp/health.json <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert value.get("service") == "nli-controller-agent"
assert value.get("api_version") == "v1"
PY
preflight_status=$(curl --silent --show-error -D /tmp/login-options.headers -o /dev/null -w '%{http_code}' -X OPTIONS http://127.0.0.1:12382/api/v1/auth/login)
[[ $preflight_status == 204 ]] || fail "login preflight returned HTTP $preflight_status"
tr -d '\r' </tmp/login-options.headers | grep -Eiq '^X-NLI-Controller:[[:space:]]*v20$' || fail "login preflight lacks controller identity"
grep -Eiq '^Allow:.*POST' /tmp/login-options.headers || fail "login preflight does not advertise POST"

curl --fail --silent --show-error http://127.0.0.1:12382/ >/tmp/webview-index.html
for marker in 'href="./styles.css"' 'src="./app.js"' 'auth-showcase' 'Users, RBAC &amp; Audit'; do
  grep -Fq "$marker" /tmp/webview-index.html || fail "WebView index is missing $marker"
done
curl --fail --silent --show-error -D /tmp/webview-css.headers -o /tmp/webview-styles.css http://127.0.0.1:12382/styles.css
curl --fail --silent --show-error -D /tmp/webview-js.headers -o /tmp/webview-app.js http://127.0.0.1:12382/app.js
curl --fail --silent --show-error -o /tmp/webview-logo.svg http://127.0.0.1:12382/nli-mark.svg
grep -Eiq '^content-type:[[:space:]]*text/css' /tmp/webview-css.headers || fail "WebView stylesheet has the wrong MIME type"
grep -Eiq '^content-type:.*(javascript|ecmascript)' /tmp/webview-js.headers || fail "WebView script has the wrong MIME type"
grep -Fq '.auth-shell' /tmp/webview-styles.css || fail "premium WebView styles are missing"
grep -Fq 'const NAV' /tmp/webview-app.js || fail "WebView application script is missing"
grep -Fq "method: 'OPTIONS'" /tmp/webview-app.js || fail "WebView does not verify the controller login contract"
grep -Fq '<svg' /tmp/webview-logo.svg || fail "WebView logo is missing or invalid"
[[ ! -e /usr/share/nli/webview_ver20/e2e.spec.js && ! -e /usr/share/nli/webview_ver20/package.json && ! -e /usr/share/nli/webview_ver20/playwright.config.js ]] || fail "development-only WebView files were installed"
curl --fail --silent --show-error -c /tmp/nli.cookies -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"ReleaseTestAdminPassword-2026"}' \
  http://127.0.0.1:12382/api/v1/auth/login >/tmp/login.json
curl --fail --silent --show-error -b /tmp/nli.cookies http://127.0.0.1:12382/api/v1/auth/me >/tmp/me.json

echo restart-client >/var/lib/nli/restart.marker
echo restart-controller >/var/lib/nli-controller/restart.marker
systemctl restart nli-controller-agent nli-agent nli-client-control
assert_services_healthy restart
[[ $(cat /var/lib/nli/restart.marker) == restart-client ]] || fail "client state lost on restart"
[[ $(cat /var/lib/nli-controller/restart.marker) == restart-controller ]] || fail "controller state lost on restart"

if [[ -n $previous_client && -n $previous_controller ]]; then
  systemctl stop nli-client-control nli-agent nli-controller-agent
  dnf -y downgrade "$previous_client" "$previous_controller"
  [[ -f /var/lib/nli/release-test.marker && -f /var/lib/nli-controller/release-test.marker ]] || fail "state lost on rollback"
  dnf -y upgrade "$client" "$controller"
  systemctl daemon-reload
  systemctl start nli-controller-agent nli-agent nli-client-control
  assert_services_healthy re-upgrade
fi

echo uninstall-client >/var/lib/nli/uninstall.marker
echo uninstall-controller >/var/lib/nli-controller/uninstall.marker
echo '# release test modification' >>/etc/nli/nli.json
echo '# release test modification' >>/etc/nli/controller.json
dnf -y remove nli-client nli-controller-agent
[[ $(cat /var/lib/nli/uninstall.marker) == uninstall-client ]] || fail "client state removed on uninstall"
[[ $(cat /var/lib/nli-controller/uninstall.marker) == uninstall-controller ]] || fail "controller state removed on uninstall"
[[ -e /etc/nli/nli.json.rpmsave || -e /etc/nli/nli.json ]] || fail "modified client config was discarded"
[[ -e /etc/nli/controller.json.rpmsave || -e /etc/nli/controller.json ]] || fail "modified controller config was discarded"

echo "Rocky validation: PASS ($(cat /etc/rocky-release))"
