#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_DIR=${1:?usage: verify-artifacts.sh ARTIFACT_DIR}
ROOT=${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}
# shellcheck disable=SC1091
source "$ROOT/packaging/release.env"
fail() { echo "artifact verification: $*" >&2; exit 1; }

client="$ARTIFACT_DIR/nli_client_ver20.rpm"
alias="$ARTIFACT_DIR/nli_clinet_ver20.rpm"
controller="$ARTIFACT_DIR/nli_controller_agent_ver20.rpm"
web="$ARTIFACT_DIR/webview_ver20.zip"
webapp="$ARTIFACT_DIR/webapp_ver20.zip"
operator_files=(
  "$ARTIFACT_DIR/install-controller-ver20.sh"
  "$ARTIFACT_DIR/install-client-ver20.sh"
  "$ARTIFACT_DIR/uninstall-controller-ver20.sh"
  "$ARTIFACT_DIR/uninstall-client-ver20.sh"
)
aws_scripts=(
  "$ARTIFACT_DIR/install-controller-aws-ver20.sh"
  "$ARTIFACT_DIR/install-client-aws-ver20.sh"
  "$ARTIFACT_DIR/install-controller-aws-cloudflare-ver20.sh"
  "$ARTIFACT_DIR/aws-rhel-preflight-ver20.sh"
  "$ARTIFACT_DIR/aws-security-group-plan-ver20.sh"
)
cloudflare_scripts=(
  "$ARTIFACT_DIR/setup-cloudflare-tunnel-v20.sh"
  "$ARTIFACT_DIR/verify-cloudflare-tunnel-v20.sh"
  "$ARTIFACT_DIR/uninstall-cloudflare-tunnel-v20.sh"
  "$ARTIFACT_DIR/cloudflare-quick-diagnosis-v20.sh"
)
for file in "$webapp" "$client" "$alias" "$controller" "$web" \
  "$ARTIFACT_DIR/V20_AWS_RED_HAT_INSTALLATION.md" "${aws_scripts[@]}" "${cloudflare_scripts[@]}" \
  "$ARTIFACT_DIR/V20_CLOUDFLARE_TUNNEL.md" "$ARTIFACT_DIR/VERCEL_DEPLOYMENT.md" "$ARTIFACT_DIR/CLOUDFLARE_PAGES_DEPLOYMENT.md" "$ARTIFACT_DIR/vercel-preflight.mjs" \
  "$ARTIFACT_DIR/INSTALLATION_AND_CONFIGURATION.md" \
  "$ARTIFACT_DIR/WEBAPP_OPERATOR_AND_PARSER_GUIDE.md" \
  "$ARTIFACT_DIR/PARSER_CATALOG.md" "$ARTIFACT_DIR/V20_MIGRATION_AND_ROLLBACK.md" "${operator_files[@]}"; do
  [[ -s $file ]] || fail "missing $(basename "$file")"
done
for script in "${aws_scripts[@]}" "${cloudflare_scripts[@]}"; do
  [[ -x $script ]] || fail "deployment script is not executable: $(basename "$script")"
  bash -n "$script"
done
for script in "${operator_files[@]}"; do
  [[ -x $script ]] || fail "operator script is not executable: $(basename "$script")"
  bash -n "$script"
  bash "$script" --help | grep -Fq 'Usage:' || fail "operator script help failed: $(basename "$script")"
done

bundle_name="nli-v${NLI_VERSION}-${NLI_RELEASE}-${NLI_ARCH}"
bundle="$ARTIFACT_DIR/$bundle_name.tar.gz"
bundle_checksum="$bundle.sha256"
[[ -s $bundle && -s $bundle_checksum ]] || fail "missing versioned release archive or archive checksum: $bundle_name"
(cd "$ARTIFACT_DIR" && sha256sum -c "$(basename "$bundle_checksum")") || fail 'versioned release archive checksum failed'
bundle_work=$(mktemp -d "${TMPDIR:-/tmp}/nli-bundle-verify.XXXXXXXX")
trap 'rm -rf -- "$bundle_work"' EXIT
while IFS= read -r entry; do
  [[ $entry == "$bundle_name" || $entry == "$bundle_name/"* ]] || fail "archive entry escapes versioned root: $entry"
  [[ ! $entry =~ (^|/)\.\.(/|$) ]] || fail "archive entry contains parent traversal: $entry"
done < <(tar -tzf "$bundle")
tar -xzf "$bundle" -C "$bundle_work"
bundle_dir="$bundle_work/$bundle_name"
[[ -d $bundle_dir ]] || fail 'versioned release archive did not create its expected root directory'
while IFS= read -r -d '' source; do
  name=$(basename "$source")
  [[ -f "$bundle_dir/$name" ]] || fail "versioned release archive is missing $name"
  cmp -s "$source" "$bundle_dir/$name" || fail "versioned release archive differs from release artifact: $name"
done < <(find "$ARTIFACT_DIR" -maxdepth 1 -type f ! -name "$bundle_name.tar.gz" ! -name "$bundle_name.tar.gz.sha256" -print0)

marker="$bundle_dir/RELEASE_ID"
[[ $(awk -F= '$1=="NLI_VERSION" {print $2; exit}' "$marker") == "$NLI_VERSION" ]] || fail 'RELEASE_ID version mismatch'
[[ $(awk -F= '$1=="NLI_RELEASE" {print $2; exit}' "$marker") == "$NLI_RELEASE" ]] || fail 'RELEASE_ID release mismatch'
[[ $(awk -F= '$1=="NLI_ARCH" {print $2; exit}' "$marker") == "$NLI_ARCH" ]] || fail 'RELEASE_ID architecture mismatch'

for rpm_file in "$client" "$alias" "$controller"; do
  magic=$(od -An -tx1 -N4 "$rpm_file" | tr -d ' \n')
  [[ $magic == edabeedb ]] || fail "$(basename "$rpm_file") has non-RPM magic $magic"
done
cmp -s "$client" "$alias" || fail "compatibility alias is not byte-identical to canonical client RPM"

unzip -t "$webapp" >/dev/null || fail "invalid Vercel bundle"
webapp_entries=$(unzip -Z1 "$webapp")
for entry in vercel.json package.json api/v1/\[...path\].js functions/api/v1/\[\[path\]\].js web/index.html web/nli-mark.svg web/nexora-logo-v20.png web/login-banner-v20.png web/nexora-poster-v20.png web/package.json web/package-lock.json web/vite.config.js web/_headers; do
  grep -Fxq "$entry" <<<"$webapp_entries" || fail "Vercel bundle is missing $entry"
done
for image in nexora-logo-v20.png login-banner-v20.png nexora-poster-v20.png; do
  source_hash=$(sha256sum "$ROOT/web/$image" | awk '{print $1}')
  bundle_hash=$(unzip -p "$webapp" "web/$image" | sha256sum | awk '{print $1}')
  [[ $source_hash == "$bundle_hash" ]] || fail "Vercel bundle changed official binary asset: $image"
done

query() { rpm -qp --qf "$2" "$1"; }
[[ $(query "$client" '%{NAME}') == "$NLI_CLIENT_PACKAGE" ]] || fail "client package name mismatch"
[[ $(query "$controller" '%{NAME}') == "$NLI_CONTROLLER_PACKAGE" ]] || fail "controller package name mismatch"
expected_repo='https://github.com/vjay136/nexora_log_intelligence_ver20.git'
[[ $(query "$client" '%{URL}') == "$expected_repo" ]] || fail "client package repository URL mismatch"
[[ $(query "$controller" '%{URL}') == "$expected_repo" ]] || fail "controller package repository URL mismatch"
rpm -qp --requires "$controller" | grep -Fxq hostname || fail "controller RPM does not declare the hostname setup dependency"
for rpm_file in "$client" "$controller"; do
  [[ $(query "$rpm_file" '%{VERSION}') == "$NLI_VERSION" ]] || fail "version mismatch in $(basename "$rpm_file")"
  rpm_release=$(query "$rpm_file" '%{RELEASE}')
  [[ $rpm_release == "$NLI_RELEASE" || $rpm_release == "$NLI_RELEASE".* ]] || fail "release mismatch in $(basename "$rpm_file"): expected $NLI_RELEASE with optional distro suffix, got $rpm_release"
  [[ $(query "$rpm_file" '%{ARCH}') == "$NLI_ARCH" ]] || fail "architecture mismatch in $(basename "$rpm_file")"
  rpm -K "$rpm_file" >/dev/null || fail "RPM digest/signature check failed for $(basename "$rpm_file")"
done

# Exercise the exact operator path that previously rejected valid distro-suffixed
# releases such as 2.el9. This verifies package identity, V20 version, release
# syntax, architecture, and the bundle checksum without changing the host.
bash "$bundle_dir/install-controller-ver20.sh" --verify-only --rpm "$bundle_dir/nli_controller_agent_ver20.rpm"
bash "$bundle_dir/install-client-ver20.sh" --verify-only --rpm "$bundle_dir/nli_client_ver20.rpm"

client_payload=$(rpm -qpl "$client")
controller_payload=$(rpm -qpl "$controller")
for path in /usr/bin/nli /usr/bin/nli-agent /usr/bin/nli-client-control /usr/bin/nli-bench \
  /usr/sbin/nli-client-setup /etc/nli/nli.json /etc/nli/client-control.json \
  /usr/lib/systemd/system/nli-agent.service /usr/lib/systemd/system/nli-client-control.service \
  /usr/lib/systemd/system/nli-client-bootstrap.service /var/log/nli/input /var/log/nli/output; do
  grep -Fxq "$path" <<<"$client_payload" || fail "client payload missing $path"
done
for path in /usr/bin/nli-controller-agent /usr/sbin/nli-controller-setup /usr/sbin/nli-supabase-setup /usr/sbin/nli-admin-reset /usr/share/nli/supabase/nli_schema.sql /etc/nli/controller.json \
  /usr/lib/systemd/system/nli-controller-agent.service /usr/lib/systemd/system/nli-controller-bootstrap.service /usr/share/nli/webview_ver20/index.html \
  /usr/share/nli/webview_ver20/styles.css /usr/share/nli/webview_ver20/app.js /usr/share/nli/webview_ver20/nli-mark.svg /usr/share/nli/webview_ver20/nexora-logo-v20.png /usr/share/nli/webview_ver20/login-banner-v20.png /usr/share/nli/webview_ver20/nexora-poster-v20.png /usr/share/nli/webview_ver20/README.txt \
  /usr/share/nli/webview_ver20/Open-NLI-WebView.ps1; do
  grep -Fxq "$path" <<<"$controller_payload" || fail "controller payload missing $path"
done
! grep -Eq 'webview_ver1(/|$)' <<<"$controller_payload" || fail "controller payload contains webview_ver1"

expected_assets=$(wc -l <"$ARTIFACT_DIR/WEBVIEW_SHA256SUMS")
web_entries=$(unzip -Z1 "$web")
zip_assets=$(grep -vc '/$' <<<"$web_entries")
rpm_assets=$(rpm -qp --dump "$controller" | awk '$1 ~ "^/usr/share/nli/webview_ver20/" && $5 ~ /^010/ {count++} END {print count+0}')
[[ $expected_assets -eq 9 && $expected_assets -eq $zip_assets && $expected_assets -eq $rpm_assets ]] || fail "WebView asset counts differ or include unexpected files: manifest=$expected_assets zip=$zip_assets rpm=$rpm_assets"
for forbidden in e2e.spec.js package.json playwright.config.js; do
  ! grep -Fq "/$forbidden" <<<"$web_entries" || fail "WebView ZIP contains development-only asset $forbidden"
  ! grep -Fq "/usr/share/nli/webview_ver20/$forbidden" <<<"$controller_payload" || fail "controller RPM contains development-only asset $forbidden"
done
while read -r expected relative; do
  zip_hash=$(unzip -p "$web" "webview_ver20/$relative" | sha256sum | awk '{print $1}')
  rpm_hash=$(rpm -qp --dump "$controller" | awk -v path="/usr/share/nli/webview_ver20/$relative" '$1==path {print $4}')
  [[ $expected == "$zip_hash" && $expected == "$rpm_hash" ]] || fail "WebView asset mismatch: $relative"
done <"$ARTIFACT_DIR/WEBVIEW_SHA256SUMS"

assert_entry() {
  local rpm_file=$1 path=$2 expected_mode=$3 expected_owner=$4 expected_group=$5 line actual_mode actual_owner actual_group
  line=$(rpm -qp --dump "$rpm_file" | awk -v path="$path" '$1==path {print; exit}')
  [[ -n $line ]] || fail "payload entry missing: $path"
  read -r _ _ _ _ actual_mode actual_owner actual_group _ <<<"$line"
  [[ $actual_mode == "$expected_mode" && $actual_owner == "$expected_owner" && $actual_group == "$expected_group" ]] || \
    fail "$path has $actual_mode $actual_owner:$actual_group, expected $expected_mode $expected_owner:$expected_group"
}
assert_entry "$client" /usr/bin/nli 0100755 root root
assert_entry "$client" /usr/bin/nli-agent 0100755 root root
assert_entry "$client" /usr/bin/nli-client-control 0100755 root root
assert_entry "$client" /usr/sbin/nli-client-setup 0100755 root root
assert_entry "$client" /etc/nli/nli.json 0100640 root nli
assert_entry "$client" /var/lib/nli 040750 nli nli
assert_entry "$client" /var/lib/nli/parsers 040750 root nli
assert_entry "$client" /var/lib/nli/parsers/quarantine 040750 nli nli
assert_entry "$controller" /usr/bin/nli-controller-agent 0100755 root root
assert_entry "$controller" /usr/sbin/nli-controller-setup 0100755 root root
assert_entry "$controller" /usr/sbin/nli-supabase-setup 0100755 root root
assert_entry "$controller" /usr/share/nli/supabase/nli_schema.sql 0100644 root root
assert_entry "$controller" /usr/sbin/nli-admin-reset 0100755 root root
assert_entry "$controller" /etc/nli/controller.json 0100640 root nli
assert_entry "$controller" /var/lib/nli-controller 040750 nli nli

assert_normalized_payload() {
  local rpm_file=$1 installed_path=$2 source_path=$3 expected actual
  expected=$(sed 's/\r$//' "$source_path" | sha256sum | awk '{print $1}')
  actual=$(rpm -qp --dump "$rpm_file" | awk -v path="$installed_path" '$1==path {print $4; exit}')
  [[ -n $actual && $actual == "$expected" ]] || fail "normalized payload differs: $installed_path"
}
assert_normalized_payload "$client" /usr/sbin/nli-client-setup "$ROOT/scripts/setup-client-rocky.sh"
assert_normalized_payload "$client" /usr/lib/systemd/system/nli-agent.service "$ROOT/packaging/systemd/nli-agent.service"
assert_normalized_payload "$client" /usr/lib/systemd/system/nli-client-control.service "$ROOT/packaging/systemd/nli-client-control.service"
assert_normalized_payload "$client" /usr/lib/systemd/system/nli-client-bootstrap.service "$ROOT/packaging/systemd/nli-client-bootstrap.service"
assert_normalized_payload "$controller" /usr/sbin/nli-controller-setup "$ROOT/scripts/setup-controller-rocky.sh"
assert_normalized_payload "$controller" /usr/sbin/nli-supabase-setup "$ROOT/scripts/setup-supabase-rocky.sh"
assert_normalized_payload "$controller" /usr/sbin/nli-admin-reset "$ROOT/scripts/reset-admin-rocky.sh"
assert_normalized_payload "$controller" /usr/lib/systemd/system/nli-controller-agent.service "$ROOT/packaging/systemd/nli-controller-agent.service"
assert_normalized_payload "$controller" /usr/lib/systemd/system/nli-controller-bootstrap.service "$ROOT/packaging/systemd/nli-controller-bootstrap.service"

for evidence in SHA256SUMS RELEASE_MANIFEST.json SBOM.spdx.json provenance.intoto.json SOURCE_BINARY_PROVENANCE.txt SOURCE_REVISION WEBVIEW_SHA256SUMS BENCHMARKS.txt INSTALLATION_AND_CONFIGURATION.md WEBAPP_OPERATOR_AND_PARSER_GUIDE.md V20_OPERATOR_DEMO_GUIDE.md V20_PRODUCTION_ACCEPTANCE_TESTS.md V20_SUBSYSTEM_ORCHESTRATION.md V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md V20_R1_TEST_RESULTS.md V20_R1_QRW_OPERATOR_TEST_GUIDE.md V20_R2_TEST_RESULTS.md V20_R2_QRW_OPERATOR_GUIDE.md V20_R3_TEST_RESULTS.md V20_R3_QRW_OPERATOR_GUIDE.md V20_QUARANTINE_RESOLUTION_WIZARD.md NLI_V20_UPES_GPRS_ARCHITECTURE.svg PARSER_CATALOG.md SAMPLE_FIELD_ACCOUNTING.json RELEASE_ID; do
  [[ -s "$ARTIFACT_DIR/$evidence" ]] || fail "missing $evidence"
done
python3 - "$ARTIFACT_DIR/SAMPLE_FIELD_ACCOUNTING.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    report = json.load(stream)
totals = report.get("totals", {})
keys = report.get("known_key_inventory", {})
assert report.get("verdict") == "pass"
assert totals.get("records") == totals.get("accounted_records") == 728
assert totals.get("parsed_records") == totals.get("exact_parity_records") == 723
assert totals.get("parsed_comparable_raw_fields") == totals.get("parsed_generated_json_fields") == 13101
assert totals.get("expected_quarantined_records") == 5
assert totals.get("unexpected_quarantined_records") == totals.get("failing_records") == 0
assert keys.get("declared_family_keys") == keys.get("covered_family_keys") == 278
assert keys.get("unmapped_family_keys") == 0
PY
grep -q '^Benchmark' "$ARTIFACT_DIR/BENCHMARKS.txt" || fail "BENCHMARKS.txt contains no benchmark samples"
(cd "$ARTIFACT_DIR" && sha256sum -c SHA256SUMS)
revision=$(tr -d '\r\n' <"$ARTIFACT_DIR/SOURCE_REVISION")
[[ $revision =~ ^[0-9a-f]{40}$ ]] || fail "invalid SOURCE_REVISION"
[[ $(grep -c '^=== .* ===$' "$ARTIFACT_DIR/SOURCE_BINARY_PROVENANCE.txt") -eq 5 ]] || fail "expected five binary provenance records"
[[ $(grep -Fc $'\tbuild\tvcs.revision='"$revision" "$ARTIFACT_DIR/SOURCE_BINARY_PROVENANCE.txt") -eq 5 ]] || fail "binary revisions do not match SOURCE_REVISION"
[[ $(grep -Fc $'\tbuild\tvcs.modified=false' "$ARTIFACT_DIR/SOURCE_BINARY_PROVENANCE.txt") -eq 5 ]] || fail "dirty binary provenance found"
grep -Fq $'\tmod\t'"$NLI_MODULE" "$ARTIFACT_DIR/SOURCE_BINARY_PROVENANCE.txt" || fail "binaries do not declare module $NLI_MODULE"
if grep -Eq $'\tmod\tnexora\.local/nli\t' "$ARTIFACT_DIR/SOURCE_BINARY_PROVENANCE.txt"; then
  fail "v4 module provenance found"
fi
for document in "$ARTIFACT_DIR/RELEASE_MANIFEST.json" "$ARTIFACT_DIR/SBOM.spdx.json" "$ARTIFACT_DIR/provenance.intoto.json"; do
  grep -Eq '^(\{|\[)' "$document" || fail "invalid JSON document header: $(basename "$document")"
done

echo 'artifact verification: PASS'
