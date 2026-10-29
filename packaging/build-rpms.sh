#!/usr/bin/env bash
set -euo pipefail

ROOT=${ROOT:-/src}
BIN_ROOT=${BIN_ROOT:-/binaries}
OUTPUT_DIR=${OUTPUT_DIR:-/out}
WORK_DIR=${WORK_DIR:-/work}
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required}
SOURCE_REVISION=${SOURCE_REVISION:?SOURCE_REVISION is required}
# shellcheck disable=SC1091
source "$ROOT/packaging/release.env"
NLI_VERSION=${NLI_VERSION_OVERRIDE:-$NLI_VERSION}
NLI_RELEASE=${NLI_RELEASE_OVERRIDE:-$NLI_RELEASE}

fail() { echo "RPM build: $*" >&2; exit 1; }
normalize_lf() {
  python3 - "$@" <<'PY'
import pathlib, sys
for raw in sys.argv[1:]:
    path = pathlib.Path(raw)
    data = path.read_bytes()
    normalized = data.replace(b"\r\n", b"\n")
    if normalized != data:
        path.write_bytes(normalized)
PY
}
mkdir -p "$OUTPUT_DIR" "$WORK_DIR"
[[ -z $(find "$OUTPUT_DIR" -mindepth 1 -print -quit) ]] || fail "output directory is not empty"
[[ -z $(find "$WORK_DIR" -mindepth 1 -print -quit) ]] || fail "work directory is not empty"
for tool in rpmbuild rpm zip unzip file sha256sum tar gzip python3; do command -v "$tool" >/dev/null || fail "missing tool: $tool"; done

install -m 0755 "$ROOT/scripts/install-controller-ver20.sh" "$OUTPUT_DIR/install-controller-ver20.sh"
install -m 0755 "$ROOT/scripts/install-client-ver20.sh" "$OUTPUT_DIR/install-client-ver20.sh"
install -m 0755 "$ROOT/scripts/install-controller-aws-ver20.sh" "$ROOT/scripts/install-client-aws-ver20.sh" "$ROOT/scripts/install-controller-aws-cloudflare-ver20.sh" "$ROOT/scripts/aws-rhel-preflight-ver20.sh" "$ROOT/scripts/aws-security-group-plan-ver20.sh" "$OUTPUT_DIR/"
install -m 0755 "$ROOT/scripts/setup-cloudflare-tunnel-v20.sh" "$ROOT/scripts/verify-cloudflare-tunnel-v20.sh" "$ROOT/scripts/uninstall-cloudflare-tunnel-v20.sh" "$ROOT/scripts/cloudflare-quick-diagnosis-v20.sh" "$OUTPUT_DIR/"
install -m 0644 "$ROOT/scripts/vercel-preflight.mjs" "$OUTPUT_DIR/vercel-preflight.mjs"
install -m 0755 "$ROOT/scripts/uninstall-controller-ver20.sh" "$OUTPUT_DIR/uninstall-controller-ver20.sh"
install -m 0755 "$ROOT/scripts/uninstall-client-ver20.sh" "$OUTPUT_DIR/uninstall-client-ver20.sh"
install -m 0644 "$ROOT/docs/INSTALLATION_AND_CONFIGURATION.md" "$OUTPUT_DIR/INSTALLATION_AND_CONFIGURATION.md"
install -m 0644 "$ROOT/docs/WEBAPP_OPERATOR_AND_PARSER_GUIDE.md" "$OUTPUT_DIR/WEBAPP_OPERATOR_AND_PARSER_GUIDE.md"
install -m 0644 "$ROOT/docs/V20_OPERATOR_DEMO_GUIDE.md" "$OUTPUT_DIR/V20_OPERATOR_DEMO_GUIDE.md"
install -m 0644 "$ROOT/docs/V20_PRODUCTION_ACCEPTANCE_TESTS.md" "$OUTPUT_DIR/V20_PRODUCTION_ACCEPTANCE_TESTS.md"
install -m 0644 "$ROOT/docs/V20_SUBSYSTEM_ORCHESTRATION.md" "$OUTPUT_DIR/V20_SUBSYSTEM_ORCHESTRATION.md"
install -m 0644 "$ROOT/docs/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md" "$OUTPUT_DIR/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md"
install -m 0644
install -m 0644 "$ROOT/docs/V20_R1_TEST_RESULTS.md" "$OUTPUT_DIR/V20_R1_TEST_RESULTS.md"
install -m 0644 "$ROOT/docs/V20_R1_QRW_OPERATOR_TEST_GUIDE.md" "$OUTPUT_DIR/V20_R1_QRW_OPERATOR_TEST_GUIDE.md"
install -m 0644 "$ROOT/docs/V20_QUARANTINE_RESOLUTION_WIZARD.md" "$OUTPUT_DIR/V20_QUARANTINE_RESOLUTION_WIZARD.md"
install -m 0644 "$ROOT/docs/architecture/NLI_V20_UPES_GPRS_ARCHITECTURE.svg" "$OUTPUT_DIR/NLI_V20_UPES_GPRS_ARCHITECTURE.svg"
install -m 0644 "$ROOT/docs/V20_AWS_RED_HAT_INSTALLATION.md" "$OUTPUT_DIR/V20_AWS_RED_HAT_INSTALLATION.md"
install -m 0644 "$ROOT/docs/V20_CLOUDFLARE_TUNNEL.md" "$OUTPUT_DIR/V20_CLOUDFLARE_TUNNEL.md"
install -m 0644 "$ROOT/docs/VERCEL_DEPLOYMENT.md" "$OUTPUT_DIR/VERCEL_DEPLOYMENT.md"
install -m 0644 "$ROOT/docs/CLOUDFLARE_PAGES_DEPLOYMENT.md" "$OUTPUT_DIR/CLOUDFLARE_PAGES_DEPLOYMENT.md"
install -m 0644 "$ROOT/docs/PARSER_CATALOG.md" "$OUTPUT_DIR/PARSER_CATALOG.md"
install -m 0644 "$ROOT/docs/release/V20_MIGRATION_AND_ROLLBACK.md" "$OUTPUT_DIR/V20_MIGRATION_AND_ROLLBACK.md"
cat >"$OUTPUT_DIR/RELEASE_ID" <<EOF
NLI_VERSION=$NLI_VERSION
NLI_RELEASE=$NLI_RELEASE
NLI_ARCH=$NLI_ARCH
SOURCE_REVISION=$SOURCE_REVISION
EOF
normalize_lf "$OUTPUT_DIR/install-controller-ver20.sh" "$OUTPUT_DIR/install-client-ver20.sh" \
  "$OUTPUT_DIR/uninstall-controller-ver20.sh" "$OUTPUT_DIR/uninstall-client-ver20.sh" \
  "$OUTPUT_DIR/install-controller-aws-ver20.sh" "$OUTPUT_DIR/install-client-aws-ver20.sh" \
  "$OUTPUT_DIR/install-controller-aws-cloudflare-ver20.sh" \
  "$OUTPUT_DIR/aws-rhel-preflight-ver20.sh" "$OUTPUT_DIR/aws-security-group-plan-ver20.sh" \
  "$OUTPUT_DIR/setup-cloudflare-tunnel-v20.sh" "$OUTPUT_DIR/verify-cloudflare-tunnel-v20.sh" \
  "$OUTPUT_DIR/uninstall-cloudflare-tunnel-v20.sh" "$OUTPUT_DIR/cloudflare-quick-diagnosis-v20.sh" \
  "$OUTPUT_DIR/vercel-preflight.mjs" \
  "$OUTPUT_DIR/INSTALLATION_AND_CONFIGURATION.md" "$OUTPUT_DIR/WEBAPP_OPERATOR_AND_PARSER_GUIDE.md" "$OUTPUT_DIR/V20_OPERATOR_DEMO_GUIDE.md" "$OUTPUT_DIR/V20_PRODUCTION_ACCEPTANCE_TESTS.md" "$OUTPUT_DIR/V20_SUBSYSTEM_ORCHESTRATION.md" "$OUTPUT_DIR/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md" "$OUTPUT_DIR/V20_R1_TEST_RESULTS.md" "$OUTPUT_DIR/V20_R1_QRW_OPERATOR_TEST_GUIDE.md" "$OUTPUT_DIR/V20_QUARANTINE_RESOLUTION_WIZARD.md" "$OUTPUT_DIR/NLI_V20_UPES_GPRS_ARCHITECTURE.svg" "$OUTPUT_DIR/V20_AWS_RED_HAT_INSTALLATION.md" \
  "$OUTPUT_DIR/V20_CLOUDFLARE_TUNNEL.md" "$OUTPUT_DIR/V20_MIGRATION_AND_ROLLBACK.md" "$OUTPUT_DIR/VERCEL_DEPLOYMENT.md" "$OUTPUT_DIR/CLOUDFLARE_PAGES_DEPLOYMENT.md" "$OUTPUT_DIR/PARSER_CATALOG.md" \
  "$OUTPUT_DIR/RELEASE_ID"

for binary in nli nli-agent nli-client-control nli-bench nli-controller-agent; do
  [[ -x "$BIN_ROOT/bin/$binary" ]] || fail "missing binary: $binary"
  file "$BIN_ROOT/bin/$binary" | grep -Eq 'ELF 64-bit.*x86-64.*statically linked' || fail "invalid binary: $binary"
done
[[ -s "$BIN_ROOT/SOURCE_BINARY_PROVENANCE.txt" ]] || fail "missing binary provenance"

TOP="$WORK_DIR/rpmbuild"
STAGE="$WORK_DIR/stage"
mkdir -p "$TOP"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS} "$STAGE"

CLIENT="$STAGE/nli-client-$NLI_VERSION"
mkdir -p "$CLIENT"/{bin,sbin,configs/parsers,systemd}
install -m 0755 "$BIN_ROOT/bin/nli" "$BIN_ROOT/bin/nli-agent" "$BIN_ROOT/bin/nli-client-control" "$BIN_ROOT/bin/nli-bench" "$CLIENT/bin/"
install -m 0755 "$ROOT/scripts/setup-client-rocky.sh" "$CLIENT/sbin/nli-client-setup"
install -m 0644 "$ROOT/configs/nli.json" "$ROOT/configs/nli.env" "$ROOT/configs/client-control.json" "$ROOT/configs/client-control.env" "$ROOT/configs/outputs.example.json" "$CLIENT/configs/"
find "$ROOT/configs/parsers" -maxdepth 1 -type f -name '*.json' -print0 | LC_ALL=C sort -z | while IFS= read -r -d '' parser; do install -m 0644 "$parser" "$CLIENT/configs/parsers/"; done
install -m 0644 "$ROOT/packaging/systemd/nli-agent.service" "$ROOT/packaging/systemd/nli-client-control.service" "$ROOT/packaging/systemd/nli-client-bootstrap.service" "$CLIENT/systemd/"
install -m 0644 "$ROOT/README.md" "$CLIENT/README.md"
install -m 0644 "$ROOT/docs/PARSER_CATALOG.md" "$CLIENT/PARSER_CATALOG.md"
install -m 0644 "$ROOT/docs/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md" "$ROOT/docs/V20_R1_TEST_RESULTS.md" "$ROOT/docs/V20_R1_QRW_OPERATOR_TEST_GUIDE.md" "$ROOT/docs/V20_QUARANTINE_RESOLUTION_WIZARD.md" "$ROOT/docs/architecture/NLI_V20_UPES_GPRS_ARCHITECTURE.svg" "$CLIENT/"
mkdir -p "$CLIENT/metrics"
install -m 0644 "$ROOT"/metrics/* "$CLIENT/metrics/"
while IFS= read -r -d '' text_file; do normalize_lf "$text_file"; done < <(
  find "$CLIENT/sbin" "$CLIENT/configs" "$CLIENT/systemd" -type f -print0
)
normalize_lf "$CLIENT/README.md"
normalize_lf "$CLIENT/PARSER_CATALOG.md"
while IFS= read -r -d '' text_file; do normalize_lf "$text_file"; done < <(
  find "$CLIENT/metrics" -type f \( -name '*.md' -o -name '*.yml' -o -name '*.yaml' \) -print0
)
python3 "$ROOT/tests/release/metrics_packaging_test.py" --root "$ROOT" --stage "$CLIENT/metrics"

WEB_ROOT="$STAGE/webview_ver20"
mkdir -p "$WEB_ROOT"
runtime_web_assets=(index.html styles.css app.js nli-mark.svg nexora-logo-v20.png login-banner-v20.png nexora-poster-v20.png README.txt Open-NLI-WebView.ps1)
for relative in "${runtime_web_assets[@]}"; do
  asset="$ROOT/web/$relative"
  [[ -s $asset ]] || fail "missing production WebView asset: $relative"
  install -m 0644 "$asset" "$WEB_ROOT/$relative"
done
while IFS= read -r -d '' text_file; do normalize_lf "$text_file"; done < <(
  find "$WEB_ROOT" -type f \( -name '*.html' -o -name '*.css' -o -name '*.js' -o -name '*.svg' -o -name '*.txt' \) -print0
)
[[ -s "$WEB_ROOT/index.html" ]] || fail "WebView index.html was not staged"
[[ $(find "$WEB_ROOT" -maxdepth 1 -type f | wc -l) -eq ${#runtime_web_assets[@]} ]] || fail "unexpected production WebView asset count"
find "$WEB_ROOT" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
(
  cd "$WEB_ROOT"
  find . -type f -printf '%P\0' | LC_ALL=C sort -z | xargs -0 -r sha256sum
) >"$OUTPUT_DIR/WEBVIEW_SHA256SUMS"
[[ -s "$OUTPUT_DIR/WEBVIEW_SHA256SUMS" ]] || fail "WebView asset manifest is empty"
(
  cd "$STAGE"
  find webview_ver20 -type f -print | LC_ALL=C sort | zip -X -q "$OUTPUT_DIR/webview_ver20.zip" -@
)

python3 "$ROOT/scripts/release/build-webapp.py" --root "$ROOT" --output "$OUTPUT_DIR/webapp_ver20.zip" --epoch "$SOURCE_DATE_EPOCH"

CONTROLLER="$STAGE/nli-controller-agent-$NLI_VERSION"
mkdir -p "$CONTROLLER"/{bin,sbin,configs,systemd,web,supabase}
install -m 0755 "$BIN_ROOT/bin/nli-controller-agent" "$BIN_ROOT/bin/nli" "$CONTROLLER/bin/"
install -m 0755 "$ROOT/scripts/setup-controller-rocky.sh" "$CONTROLLER/sbin/nli-controller-setup"
install -m 0755 "$ROOT/scripts/setup-supabase-rocky.sh" "$CONTROLLER/sbin/nli-supabase-setup"
install -m 0755 "$ROOT/scripts/reset-admin-rocky.sh" "$CONTROLLER/sbin/nli-admin-reset"
install -m 0644 "$ROOT/supabase/nli_schema.sql" "$CONTROLLER/supabase/nli_schema.sql"
install -m 0644 "$ROOT/packaging/config/controller.json" "$CONTROLLER/configs/controller.json"
install -m 0644 "$ROOT/configs/controller.env" "$CONTROLLER/configs/controller.env"
install -m 0644 "$ROOT/configs/geoip.env" "$CONTROLLER/configs/geoip.env"
install -m 0644 "$ROOT/packaging/systemd/nli-controller-agent.service" "$ROOT/packaging/systemd/nli-controller-bootstrap.service" "$ROOT/packaging/systemd/nli-geoip-update.service" "$ROOT/packaging/systemd/nli-geoip-update.timer" "$CONTROLLER/systemd/"
cp -a "$WEB_ROOT/." "$CONTROLLER/web/"
install -m 0644 "$ROOT/README.md" "$CONTROLLER/README.md"
install -m 0644 "$ROOT/docs/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md" "$ROOT/docs/V20_R1_TEST_RESULTS.md" "$ROOT/docs/V20_R1_QRW_OPERATOR_TEST_GUIDE.md" "$ROOT/docs/V20_QUARANTINE_RESOLUTION_WIZARD.md" "$ROOT/docs/architecture/NLI_V20_UPES_GPRS_ARCHITECTURE.svg" "$CONTROLLER/"
mkdir -p "$CONTROLLER/metrics"
install -m 0644 "$ROOT"/metrics/* "$CONTROLLER/metrics/"
while IFS= read -r -d '' text_file; do normalize_lf "$text_file"; done < <(
  find "$CONTROLLER/sbin" "$CONTROLLER/configs" "$CONTROLLER/systemd" "$CONTROLLER/supabase" -type f -print0
)
normalize_lf "$CONTROLLER/README.md"
while IFS= read -r -d '' text_file; do normalize_lf "$text_file"; done < <(
  find "$CONTROLLER/metrics" -type f \( -name '*.md' -o -name '*.yml' -o -name '*.yaml' \) -print0
)
python3 "$ROOT/tests/release/metrics_packaging_test.py" --root "$ROOT" --stage "$CONTROLLER/metrics"
python3 "$ROOT/tests/release/release_docs_packaging_test.py" --root "$ROOT" --output "$OUTPUT_DIR" --client "$CLIENT" --controller "$CONTROLLER"

make_tarball() {
  local parent=$1 directory=$2 destination=$3
  find "$parent/$directory" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
  tar --sort=name --format=gnu --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner -C "$parent" -cf - "$directory" | gzip -n -9 >"$destination"
}
make_tarball "$STAGE" "nli-client-$NLI_VERSION" "$TOP/SOURCES/nli-client-$NLI_VERSION.tar.gz"
make_tarball "$STAGE" "nli-controller-agent-$NLI_VERSION" "$TOP/SOURCES/nli-controller-agent-$NLI_VERSION.tar.gz"
install -m 0644 "$ROOT/packaging/rpm/nli-client.spec" "$ROOT/packaging/rpm/nli-controller-agent.spec" "$TOP/SPECS/"

export SOURCE_DATE_EPOCH
rpm_args=(
  --define "_topdir $TOP"
  --define "_buildhost reproducible.nli.local"
  --define "_source_filedigest_algorithm 8"
  --define "_binary_filedigest_algorithm 8"
  --define "clamp_mtime_to_source_date_epoch 1"
  --define "use_source_date_epoch_as_buildtime 1"
  --define "nli_version $NLI_VERSION"
  --define "nli_release $NLI_RELEASE"
)
rpmbuild "${rpm_args[@]}" -bb "$TOP/SPECS/nli-client.spec"
rpmbuild "${rpm_args[@]}" -bb "$TOP/SPECS/nli-controller-agent.spec"

client_rpm=$(find "$TOP/RPMS" -type f -name 'nli-client-*.x86_64.rpm' -print -quit)
controller_rpm=$(find "$TOP/RPMS" -type f -name 'nli-controller-agent-*.x86_64.rpm' -print -quit)
[[ -n $client_rpm && -n $controller_rpm ]] || fail "rpmbuild did not produce both RPMs"
install -m 0644 "$client_rpm" "$OUTPUT_DIR/nli_client_ver20.rpm"
install -m 0644 "$controller_rpm" "$OUTPUT_DIR/nli_controller_agent_ver20.rpm"
cp -f "$OUTPUT_DIR/nli_client_ver20.rpm" "$OUTPUT_DIR/nli_clinet_ver20.rpm"
install -m 0644 "$BIN_ROOT/SOURCE_BINARY_PROVENANCE.txt" "$OUTPUT_DIR/SOURCE_BINARY_PROVENANCE.txt"
printf '%s\n' "$SOURCE_REVISION" >"$OUTPUT_DIR/SOURCE_REVISION"

for rpm_file in "$OUTPUT_DIR"/*.rpm; do
  [[ $(od -An -tx1 -N4 "$rpm_file" | tr -d ' \n') == edabeedb ]] || fail "non-RPM output: $rpm_file"
done
cmp -s "$OUTPUT_DIR/nli_client_ver20.rpm" "$OUTPUT_DIR/nli_clinet_ver20.rpm" || fail "client alias differs"
rpm -qpi "$OUTPUT_DIR/nli_client_ver20.rpm" >"$OUTPUT_DIR/nli_client_ver20.rpm.info.txt"
rpm -qpi "$OUTPUT_DIR/nli_controller_agent_ver20.rpm" >"$OUTPUT_DIR/nli_controller_agent_ver20.rpm.info.txt"
rpm -qpl "$OUTPUT_DIR/nli_client_ver20.rpm" >"$OUTPUT_DIR/nli_client_ver20.files.txt"
rpm -qpl "$OUTPUT_DIR/nli_controller_agent_ver20.rpm" >"$OUTPUT_DIR/nli_controller_agent_ver20.files.txt"
rpm -qp --qf '[%{FILENAMES} %{FILEMODES:perms} %{FILEUSERNAME} %{FILEGROUPNAME}\n]' "$OUTPUT_DIR/nli_client_ver20.rpm" >"$OUTPUT_DIR/nli_client_ver20.permissions.txt"
rpm -qp --qf '[%{FILENAMES} %{FILEMODES:perms} %{FILEUSERNAME} %{FILEGROUPNAME}\n]' "$OUTPUT_DIR/nli_controller_agent_ver20.rpm" >"$OUTPUT_DIR/nli_controller_agent_ver20.permissions.txt"

for required in /usr/bin/nli /usr/bin/nli-agent /usr/bin/nli-client-control /usr/bin/nli-bench /usr/sbin/nli-client-setup /etc/nli/nli.json /etc/nli/parsers /usr/share/doc/nli-client/PARSER_CATALOG.md /usr/share/doc/nli-client/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md /usr/share/doc/nli-client/V20_R1_TEST_RESULTS.md /usr/share/doc/nli-client/V20_R1_QRW_OPERATOR_TEST_GUIDE.md /usr/share/doc/nli-client/V20_QUARANTINE_RESOLUTION_WIZARD.md /usr/share/doc/nli-client/NLI_V20_UPES_GPRS_ARCHITECTURE.svg /usr/share/nli/metrics/README.md /usr/share/nli/metrics/metric-catalog.json /usr/share/nli/metrics/prometheus-scrape.yml /usr/share/nli/metrics/grafana-dashboard.json /usr/lib/systemd/system/nli-agent.service /usr/lib/systemd/system/nli-client-control.service /usr/lib/systemd/system/nli-client-bootstrap.service /var/lib/nli /var/lib/nli-control-client /var/log/nli/input /var/log/nli/output; do
  grep -Fxq "$required" "$OUTPUT_DIR/nli_client_ver20.files.txt" || fail "client payload missing $required"
done
mapfile -t profile_assets < <(find "$ROOT/configs/parsers" -maxdepth 1 -type f -name '*.parserpack.json' -printf '%f\n' | LC_ALL=C sort)
[[ ${#profile_assets[@]} -ge 8 ]] || fail "expected at least the eight reviewed baseline parser profiles"
for asset in "${profile_assets[@]}"; do
  installed="/etc/nli/parsers/$asset"
  grep -Fxq "$installed" "$OUTPUT_DIR/nli_client_ver20.files.txt" || fail "client payload missing reviewed profile $installed"
  grep -Fxq "$installed -rw-r--r-- root nli" "$OUTPUT_DIR/nli_client_ver20.permissions.txt" || fail "reviewed profile has wrong mode/owner: $installed"
  source_hash=$(sed 's/\r$//' "$ROOT/configs/parsers/$asset" | sha256sum | awk '{print $1}')
  rpm_hash=$(rpm -qp --dump "$OUTPUT_DIR/nli_client_ver20.rpm" | awk -v path="$installed" '$1==path {print $4}')
  [[ -n $rpm_hash && $source_hash == "$rpm_hash" ]] || fail "reviewed profile differs in RPM: $asset"
done
[[ $(grep -Ec '^/etc/nli/parsers/[^/]+\.parserpack\.json$' "$OUTPUT_DIR/nli_client_ver20.files.txt") -eq ${#profile_assets[@]} ]] || fail "unexpected reviewed profile count in client RPM"
for required in /usr/bin/nli-controller-agent /usr/bin/nli /usr/sbin/nli-controller-setup /usr/sbin/nli-supabase-setup /usr/sbin/nli-admin-reset /usr/share/doc/nli-controller-agent/V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md /usr/share/doc/nli-controller-agent/V20_R1_TEST_RESULTS.md /usr/share/doc/nli-controller-agent/V20_R1_QRW_OPERATOR_TEST_GUIDE.md /usr/share/doc/nli-controller-agent/V20_QUARANTINE_RESOLUTION_WIZARD.md /usr/share/doc/nli-controller-agent/NLI_V20_UPES_GPRS_ARCHITECTURE.svg /usr/share/nli/supabase/nli_schema.sql /usr/share/nli/metrics/README.md /usr/share/nli/metrics/metric-catalog.json /usr/share/nli/metrics/prometheus-scrape.yml /usr/share/nli/metrics/grafana-dashboard.json /etc/nli/controller.json /etc/nli/geoip.env /usr/lib/systemd/system/nli-controller-agent.service /usr/lib/systemd/system/nli-controller-bootstrap.service /usr/lib/systemd/system/nli-geoip-update.service /usr/lib/systemd/system/nli-geoip-update.timer /usr/share/nli/webview_ver20/index.html /usr/share/nli/webview_ver20/styles.css /usr/share/nli/webview_ver20/app.js /usr/share/nli/webview_ver20/nli-mark.svg /usr/share/nli/webview_ver20/nexora-logo-v20.png /usr/share/nli/webview_ver20/login-banner-v20.png /usr/share/nli/webview_ver20/nexora-poster-v20.png /usr/share/nli/webview_ver20/README.txt /usr/share/nli/webview_ver20/Open-NLI-WebView.ps1 /var/lib/nli-controller; do
  grep -Fxq "$required" "$OUTPUT_DIR/nli_controller_agent_ver20.files.txt" || fail "controller payload missing $required"
done
! grep -Eq 'webview_ver(1|5|6|7)(/|$)' "$OUTPUT_DIR/nli_controller_agent_ver20.files.txt" || fail "stale pre-V20 WebView payload"
expected_assets=$(wc -l <"$OUTPUT_DIR/WEBVIEW_SHA256SUMS")
zip_assets=$(unzip -Z1 "$OUTPUT_DIR/webview_ver20.zip" | grep -vc '/$')
rpm_assets=$(rpm -qp --dump "$OUTPUT_DIR/nli_controller_agent_ver20.rpm" | awk '$1 ~ "^/usr/share/nli/webview_ver20/" && $5 ~ /^010/ {count++} END {print count+0}')
[[ $expected_assets -eq $zip_assets && $expected_assets -eq $rpm_assets ]] || fail "WebView asset counts differ: source=$expected_assets zip=$zip_assets rpm=$rpm_assets"
while read -r expected relative; do
  zip_hash=$(unzip -p "$OUTPUT_DIR/webview_ver20.zip" "webview_ver20/$relative" | sha256sum | awk '{print $1}')
  rpm_hash=$(rpm -qp --dump "$OUTPUT_DIR/nli_controller_agent_ver20.rpm" | awk -v path="/usr/share/nli/webview_ver20/$relative" '$1==path {print $4}')
  [[ $expected == "$zip_hash" && $expected == "$rpm_hash" ]] || fail "WebView asset differs: $relative"
done <"$OUTPUT_DIR/WEBVIEW_SHA256SUMS"
echo "RPM build: PASS"
