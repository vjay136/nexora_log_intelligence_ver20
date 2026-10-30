#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
: "${NLI_BUILD_SCRATCH:?NLI_BUILD_SCRATCH is required}"
# shellcheck disable=SC1091
source "$ROOT/packaging/release.env"
# shellcheck disable=SC1091
source "$ROOT/packaging/docker/images.env"
revision=$(git -C "$ROOT" rev-parse HEAD)
epoch=$(git -C "$ROOT" show -s --format=%ct HEAD)
work=$(mktemp -d "$NLI_BUILD_SCRATCH/nli-package-infra.XXXXXXXX")
mkdir -p "$work"/{fixture/source,fixture/binaries/bin,previous,previous-work,pass1,pass1-work,pass2,pass2-work,final}

fixture_script='set -euo pipefail
export PATH=/usr/local/go/bin:/usr/bin:/bin
cd /fixture/source
printf "module nexora.local/nli-v20\n\ngo 1.24.9\n" > go.mod
cat > main.go <<"GO"
package main

import (
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
)

const expectedSMTP = `{"action":"disconnect","auth_fail":6,"auth_ok":false,"auth_tries":7,"auth_user":"managers999@012.meg.in","checkpoint":"AUTH","code":"2005/1","date":"20260822","event":"SieveRuleMsg","eventType":"PolicyMsgInfo","event_type":"Note","fromhost":"11.22.33.44","geo":"IL","helo":"Site6","hostname":"severrgo001","id":"6A291BF5048F23DE","major_pid":"230675","minor_pid":"140347436861184","msg":"PolicyMsgInfo","pid_status":0,"policy":"outbound","reason":"badpassword","rule":"INOUT_action","score":0,"service":"smtp","time":"050048557+0200"}`

func main() {
    name := filepath.Base(os.Args[0])
    if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
        if name == "nli-controller-agent" { name = "nli-control" }
        fmt.Println(name, "20.0.0")
        return
    }
    switch name {
    case "nli":
        if len(os.Args) > 1 && os.Args[1] == "parse" {
            _, _ = io.Copy(io.Discard, os.Stdin)
            fmt.Println(expectedSMTP)
            return
        }
    case "nli-agent":
        mux := http.NewServeMux()
        mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) })
        mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "nli_logs_received_total 1\nnli_logs_parsed_total 1\n") })
        _ = http.ListenAndServe("127.0.0.1:12380", mux)
    case "nli-controller-agent":
        mux := http.NewServeMux()
        mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            w.Header().Set("X-NLI-Controller", "v20")
            w.Header().Set("Cache-Control", "no-store")
            if r.Method != http.MethodGet {
                w.Header().Set("Allow", "GET")
                w.WriteHeader(http.StatusMethodNotAllowed)
                _, _ = io.WriteString(w, `{"error":"GET required"}`)
                return
            }
            _, _ = io.WriteString(w, `{"service":"nli-controller-agent","api_version":"v1","status":"ok"}`)
        })
        mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            w.Header().Set("X-NLI-Controller", "v20")
            w.Header().Set("Cache-Control", "no-store")
            w.Header().Set("Allow", "POST, OPTIONS")
            if r.Method == http.MethodOptions { w.WriteHeader(http.StatusNoContent); return }
            if r.Method != http.MethodPost { w.WriteHeader(http.StatusMethodNotAllowed); return }
            http.SetCookie(w, &http.Cookie{Name: "nli_session", Value: "fixture", Path: "/"})
            _, _ = io.WriteString(w, `{"username":"admin","role":"admin"}`)
        })
        mux.HandleFunc("/api/v1/auth/me", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"username":"admin","role":"admin"}`) })
        mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<!doctype html><html><body>NLI v20 fixture</body></html>") })
        _ = http.ListenAndServe("127.0.0.1:12382", mux)
    case "nli-client-control":
        select {}
    }
}
GO
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" -o /fixture/binaries/fixture .
for name in nli nli-agent nli-client-control nli-bench nli-controller-agent; do cp /fixture/binaries/fixture "/fixture/binaries/bin/$name"; done
: > /fixture/binaries/SOURCE_BINARY_PROVENANCE.txt
for binary in /fixture/binaries/bin/*; do
  echo "=== $(basename "$binary") ===" >> /fixture/binaries/SOURCE_BINARY_PROVENANCE.txt
  go version -m "$binary" >> /fixture/binaries/SOURCE_BINARY_PROVENANCE.txt
  printf "\tbuild\tvcs.revision=%s\n\tbuild\tvcs.modified=false\n" "$SOURCE_REVISION" >> /fixture/binaries/SOURCE_BINARY_PROVENANCE.txt
  sha256sum "$binary" >> /fixture/binaries/SOURCE_BINARY_PROVENANCE.txt
done'
docker run --rm --mount "type=bind,src=$work/fixture,dst=/fixture" -e SOURCE_REVISION="$revision" \
  "$GO_IMAGE" bash -lc "$fixture_script"

rpm_command='bash /src/packaging/docker/install-rpm-tools.sh >/dev/null; bash /src/packaging/build-rpms.sh'
build_rpm_fixture() {
  local output=$1 rpm_work=$2 version=$3 release=$4
  docker run --rm \
    --mount "type=bind,src=$ROOT,dst=/src,readonly" \
    --mount "type=bind,src=$work/fixture/binaries,dst=/binaries,readonly" \
    --mount "type=bind,src=$output,dst=/out" \
    --mount "type=bind,src=$rpm_work,dst=/work" \
    -e ROOT=/src -e BIN_ROOT=/binaries -e OUTPUT_DIR=/out -e WORK_DIR=/work \
    -e SOURCE_REVISION="$revision" -e SOURCE_DATE_EPOCH="$epoch" \
    -e NLI_VERSION_OVERRIDE="$version" -e NLI_RELEASE_OVERRIDE="$release" \
    "$RPM_BUILD_IMAGE" bash -lc "$rpm_command"
}
# Synthetic Release 1 payload for local package lifecycle mechanics only;
# tag CI still downloads the genuine published Ver20 Release 1 RPMs.
build_rpm_fixture "$work/previous" "$work/previous-work" 20.0.0 1
build_rpm_fixture "$work/pass1" "$work/pass1-work" "$NLI_VERSION" "$NLI_RELEASE"
build_rpm_fixture "$work/pass2" "$work/pass2-work" "$NLI_VERSION" "$NLI_RELEASE"
for name in nli_client_ver20.rpm nli_clinet_ver20.rpm nli_controller_agent_ver20.rpm webview_ver20.zip webapp_ver20.zip install-controller-ver20.sh install-client-ver20.sh install-controller-aws-ver20.sh install-client-aws-ver20.sh install-controller-aws-cloudflare-ver20.sh aws-rhel-preflight-ver20.sh aws-security-group-plan-ver20.sh setup-cloudflare-tunnel-v20.sh verify-cloudflare-tunnel-v20.sh uninstall-cloudflare-tunnel-v20.sh cloudflare-quick-diagnosis-v20.sh vercel-preflight.mjs uninstall-controller-ver20.sh uninstall-client-ver20.sh INSTALLATION_AND_CONFIGURATION.md V20_AWS_RED_HAT_INSTALLATION.md V20_CLOUDFLARE_TUNNEL.md VERCEL_DEPLOYMENT.md CLOUDFLARE_PAGES_DEPLOYMENT.md WEBAPP_OPERATOR_AND_PARSER_GUIDE.md PARSER_CATALOG.md V20_MIGRATION_AND_ROLLBACK.md V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md V20_R1_TEST_RESULTS.md V20_R1_QRW_OPERATOR_TEST_GUIDE.md V20_R2_TEST_RESULTS.md V20_R2_QRW_OPERATOR_GUIDE.md V20_R3_TEST_RESULTS.md V20_R3_QRW_OPERATOR_GUIDE.md V20_QUARANTINE_RESOLUTION_WIZARD.md NLI_V20_UPES_GPRS_ARCHITECTURE.svg SOURCE_BINARY_PROVENANCE.txt SOURCE_REVISION WEBVIEW_SHA256SUMS RELEASE_ID; do
  cmp -s "$work/pass1/$name" "$work/pass2/$name" || { echo "fixture output is non-deterministic: $name" >&2; exit 1; }
done
cp -a "$work/pass1/." "$work/final/"
printf 'BenchmarkFixture-8  5  100 ns/op\n' >"$work/final/BENCHMARKS.txt"
cat >"$work/final/SAMPLE_FIELD_ACCOUNTING.json" <<'JSON'
{
  "schema_version": 1,
  "verdict": "pass",
  "known_key_inventory": {
    "declared_family_keys": 278,
    "covered_family_keys": 278,
    "unmapped_family_keys": 0
  },
  "totals": {
    "records": 728,
    "accounted_records": 728,
    "parsed_records": 723,
    "exact_parity_records": 723,
    "parsed_comparable_raw_fields": 13101,
    "parsed_generated_json_fields": 13101,
    "expected_quarantined_records": 5,
    "unexpected_quarantined_records": 0,
    "failing_records": 0
  }
}
JSON

docker run --rm --mount "type=bind,src=$ROOT,dst=/src,readonly" --mount "type=bind,src=$work/final,dst=/artifacts" \
  "$EVIDENCE_IMAGE" python /src/scripts/release/generate-evidence.py \
  --artifacts /artifacts --version "$NLI_VERSION" --release "$NLI_RELEASE" --revision "$revision" \
  --source-date-epoch "$epoch" --images /src/packaging/docker/images.env

docker run --rm --mount "type=bind,src=$ROOT,dst=/src,readonly" --mount "type=bind,src=$work/final,dst=/artifacts" \
  "$EVIDENCE_IMAGE" sh -lc 'for f in /artifacts/RELEASE_MANIFEST.json /artifacts/SBOM.spdx.json /artifacts/provenance.intoto.json; do python -m json.tool "$f" >/dev/null; done'
verify_command='bash /src/packaging/docker/install-rpm-tools.sh >/dev/null; ROOT=/src bash /src/scripts/release/create-bundle.sh /artifacts; ROOT=/src bash /src/scripts/release/verify-artifacts.sh /artifacts'
docker run --rm --mount "type=bind,src=$ROOT,dst=/src,readonly" --mount "type=bind,src=$work/final,dst=/artifacts" \
  "$RPM_BUILD_IMAGE" bash -lc "$verify_command"

if [[ ${RUN_ROCKY_FIXTURE:-0} == 1 ]]; then
  RUN_TWO_NODE_ACCEPTANCE=0 bash "$ROOT/tests/rocky/run-matrix.sh" "$work/final" "$work/previous"
fi

echo "release package infrastructure fixture: PASS"
echo "fixture evidence: $work/final"
echo "fixture previous RPMs: $work/previous"
