#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
OUTPUT_DIR=${1:?usage: build.sh OUTPUT_DIR}
: "${NLI_BUILD_SCRATCH:?NLI_BUILD_SCRATCH must point to session-owned scratch space}"
# shellcheck disable=SC1091
source "$ROOT/packaging/release.env"
# shellcheck disable=SC1091
source "$ROOT/packaging/docker/images.env"

bash "$ROOT/scripts/release/preflight.sh"
revision=$(git -C "$ROOT" rev-parse HEAD)
epoch=$(git -C "$ROOT" show -s --format=%ct HEAD)
mkdir -p "$OUTPUT_DIR"
[[ -z $(find "$OUTPUT_DIR" -mindepth 1 -print -quit) ]] || { echo "output directory is not empty: $OUTPUT_DIR" >&2; exit 1; }
work=$(mktemp -d "$NLI_BUILD_SCRATCH/nli-release.XXXXXXXX")
mkdir -p "$work"/{go-cache,go-mod,pass1/bin,pass1-rpm,pass1-work,pass2/bin,pass2-rpm,pass2-work}

build_binaries() {
  local destination=$1 skip_tests=${2:-0}
  docker run --rm \
    --mount "type=bind,src=$ROOT,dst=/src,readonly" \
    --mount "type=bind,src=$destination,dst=/out" \
    --mount "type=bind,src=$work/go-cache,dst=/cache/go-build" \
    --mount "type=bind,src=$work/go-mod,dst=/cache/go-mod" \
    --workdir /src \
    -e ROOT=/src -e OUTPUT_DIR=/out -e SOURCE_REVISION="$revision" -e SOURCE_DATE_EPOCH="$epoch" \
    -e NLI_SKIP_TESTS="$skip_tests" -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/go-mod \
    "$GO_IMAGE" bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin; bash packaging/build-binaries.sh'
}

rpm_command='bash /src/packaging/docker/install-rpm-tools.sh >/dev/null; bash /src/packaging/build-rpms.sh'
build_rpms() {
  local binaries=$1 destination=$2 rpm_work=$3
  docker run --rm \
    --mount "type=bind,src=$ROOT,dst=/src,readonly" \
    --mount "type=bind,src=$binaries,dst=/binaries,readonly" \
    --mount "type=bind,src=$destination,dst=/out" \
    --mount "type=bind,src=$rpm_work,dst=/work" \
    --workdir /src \
    -e ROOT=/src -e BIN_ROOT=/binaries -e OUTPUT_DIR=/out -e WORK_DIR=/work \
    -e SOURCE_REVISION="$revision" -e SOURCE_DATE_EPOCH="$epoch" \
    "$RPM_BUILD_IMAGE" bash -lc "$rpm_command"
}

build_binaries "$work/pass1"
build_rpms "$work/pass1" "$work/pass1-rpm" "$work/pass1-work"
build_binaries "$work/pass2" 1
build_rpms "$work/pass2" "$work/pass2-rpm" "$work/pass2-work"

for name in nli_client_ver20.rpm nli_clinet_ver20.rpm nli_controller_agent_ver20.rpm webview_ver20.zip webapp_ver20.zip install-controller-ver20.sh install-client-ver20.sh install-controller-aws-ver20.sh install-client-aws-ver20.sh install-controller-aws-cloudflare-ver20.sh aws-rhel-preflight-ver20.sh aws-security-group-plan-ver20.sh setup-cloudflare-tunnel-v20.sh verify-cloudflare-tunnel-v20.sh uninstall-cloudflare-tunnel-v20.sh cloudflare-quick-diagnosis-v20.sh vercel-preflight.mjs uninstall-controller-ver20.sh uninstall-client-ver20.sh INSTALLATION_AND_CONFIGURATION.md V20_AWS_RED_HAT_INSTALLATION.md V20_CLOUDFLARE_TUNNEL.md VERCEL_DEPLOYMENT.md CLOUDFLARE_PAGES_DEPLOYMENT.md WEBAPP_OPERATOR_AND_PARSER_GUIDE.md V20_OPERATOR_DEMO_GUIDE.md V20_PRODUCTION_ACCEPTANCE_TESTS.md V20_SUBSYSTEM_ORCHESTRATION.md V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md V20_R1_TEST_RESULTS.md V20_R1_QRW_OPERATOR_TEST_GUIDE.md V20_R2_TEST_RESULTS.md V20_R2_QRW_OPERATOR_GUIDE.md V20_R3_TEST_RESULTS.md V20_R3_QRW_OPERATOR_GUIDE.md V20_QUARANTINE_RESOLUTION_WIZARD.md NLI_V20_UPES_GPRS_ARCHITECTURE.svg PARSER_CATALOG.md V20_MIGRATION_AND_ROLLBACK.md SOURCE_BINARY_PROVENANCE.txt SOURCE_REVISION WEBVIEW_SHA256SUMS RELEASE_ID; do
  cmp -s "$work/pass1-rpm/$name" "$work/pass2-rpm/$name" || { echo "non-deterministic output: $name" >&2; exit 1; }
done
cp -a "$work/pass1-rpm/." "$OUTPUT_DIR/"

docker run --rm \
  --mount "type=bind,src=$ROOT,dst=/src,readonly" \
  --mount "type=bind,src=$OUTPUT_DIR,dst=/artifacts" \
  --workdir /src \
  "$GO_IMAGE" bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin GOCACHE=/tmp/gocache GOTMPDIR=/tmp/gotmp; mkdir -p "$GOCACHE" "$GOTMPDIR"; { go test ./internal/benchmarks -run="^$" -bench=. -benchmem -benchtime=250ms -count=5; go test ./parser-policy ./internal/mixedkv -run="^$" -bench=. -benchmem -benchtime=500ms -count=5; } > /artifacts/BENCHMARKS.txt'

docker run --rm \
  --mount "type=bind,src=$ROOT,dst=/src,readonly" \
  --mount "type=bind,src=$OUTPUT_DIR,dst=/artifacts" \
  --workdir /src \
  -e NLI_FIELD_ACCOUNTING_REPORT=/artifacts/SAMPLE_FIELD_ACCOUNTING.json \
  "$GO_IMAGE" bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin GOCACHE=/tmp/gocache GOTMPDIR=/tmp/gotmp; mkdir -p "$GOCACHE" "$GOTMPDIR"; go test ./internal/migration -run="^TestSample(LogCorpusFieldAccounting|FieldAccountingDuplicateSemantics)$" -count=1'

docker run --rm \
  --mount "type=bind,src=$ROOT,dst=/src,readonly" \
  --mount "type=bind,src=$OUTPUT_DIR,dst=/artifacts" \
  "$EVIDENCE_IMAGE" python /src/scripts/release/generate-evidence.py \
    --artifacts /artifacts --version "$NLI_VERSION" --release "$NLI_RELEASE" \
    --revision "$revision" --source-date-epoch "$epoch" --images /src/packaging/docker/images.env

verify_command='bash /src/packaging/docker/install-rpm-tools.sh >/dev/null; ROOT=/src bash /src/scripts/release/create-bundle.sh /artifacts; ROOT=/src bash /src/scripts/release/verify-artifacts.sh /artifacts'
docker run --rm \
  --mount "type=bind,src=$ROOT,dst=/src,readonly" \
  --mount "type=bind,src=$OUTPUT_DIR,dst=/artifacts" \
  -e ROOT=/src "$RPM_BUILD_IMAGE" bash -lc "$verify_command"

printf 'reproducible unsigned release: PASS\nartifacts: %s\nscratch: %s\n' "$OUTPUT_DIR" "$work"
