#!/usr/bin/env bash
set -euo pipefail

ROOT=${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}
# shellcheck disable=SC1091
source "$ROOT/packaging/release.env"

fail() { printf 'release preflight: %s\n' "$*" >&2; exit 1; }

[[ -d "$ROOT/.git" ]] || fail "source must be a Git checkout"
[[ -z "$(git -C "$ROOT" status --porcelain)" ]] || fail "source revision is not frozen and clean"
[[ $(git -C "$ROOT" rev-parse HEAD) == "${SOURCE_REVISION:-$(git -C "$ROOT" rev-parse HEAD)}" ]] || fail "SOURCE_REVISION does not match HEAD"
[[ $(sed -n 's/^module //p' "$ROOT/go.mod") == "$NLI_MODULE" ]] || fail "go.mod module must be $NLI_MODULE"
runtime_version=$(sed -n 's/^const[[:space:]]\+Version[[:space:]]*=[[:space:]]*"\([^"]*\)"/\1/p' "$ROOT/internal/engine/engine.go")
[[ $runtime_version == "$NLI_VERSION" ]] || fail "runtime version is not $NLI_VERSION"

for forbidden in \
  packaging/build-simple.ps1 \
  packaging/build-local.ps1 \
  packaging/build-local.sh \
  packaging/rpm/nli-agent.spec \
  packaging/systemd/nli-control.service \
  packaging/systemd/nli-manager.service; do
  [[ ! -e "$ROOT/$forbidden" ]] || fail "forbidden legacy release path remains: $forbidden"
done

bad_archive_pattern='Compress-Archive.*[.]rpm|Rename-Item.*[.]rpm|placeholder[[:space:]]+RPM|ZIP-as-[R]PM'
if grep -RInE "$bad_archive_pattern" \
  "$ROOT/packaging" "$ROOT/scripts/release" "$ROOT/tests/release" "$ROOT/tests/rocky"; then
  fail "archive masquerading as RPM is reachable"
fi

if find "$ROOT" -path "$ROOT/.git" -prune -o -name '*.disabled' -print | grep -q .; then
  fail "disabled tests remain in the integrated revision"
fi

web_assets=("$ROOT/web/index.html" "$ROOT/web/app.js" "$ROOT/web/styles.css" "$ROOT/web/nli-mark.svg" "$ROOT/web/nexora-logo-v20.png" "$ROOT/web/login-banner-v20.png" "$ROOT/web/nexora-poster-v20.png")
for asset in "${web_assets[@]}"; do
  [[ -s "$asset" ]] || fail "WebView asset is missing: ${asset#$ROOT/}"
done
if grep -nE 'webview_ver1|v1\.0\.0|"0\.4\.0"' "${web_assets[@]}"; then
  fail "WebView contains stale release identifiers"
fi
for label in 'Command Center' 'Assurance Lab' 'Fleet Operations' 'Parser Catalog & Priority' \
  'Migration Center' 'KV Delimiter Config' 'Static Positional Patterns' \
  'Field Capture & Projection' 'Regex, Templates & Studio' 'Live Metadata' \
  'Field Integrity & Corpus Proof' 'Unknown & Quarantine' 'Template Drift' \
  'Performance & Backpressure' 'Parser Creation Workspace' 'AI Resolution & Providers' \
  'Rollouts' 'Signed Artifacts & Upgrades' 'Users, RBAC & Audit' \
  'System Health & Storage'; do
  grep -Fqi "$label" "$ROOT/web/app.js" || fail "WebView gate missing: $label"
done

git -C "$ROOT" diff --check
printf 'release preflight: PASS (%s-%s, %s)\n' "$NLI_VERSION" "$NLI_RELEASE" "$(git -C "$ROOT" rev-parse HEAD)"
