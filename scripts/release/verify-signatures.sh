#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_DIR=${1:?usage: verify-signatures.sh ARTIFACT_DIR}
verified=0

if [[ -n ${COSIGN_PUBLIC_KEY:-} ]]; then
  command -v cosign >/dev/null || { echo 'cosign is required' >&2; exit 1; }
  for file in "$ARTIFACT_DIR"/{SHA256SUMS,RELEASE_MANIFEST.json,SBOM.spdx.json,provenance.intoto.json}; do
    [[ -s $file.sig ]] || { echo "missing $file.sig" >&2; exit 1; }
    cosign verify-blob --key "$COSIGN_PUBLIC_KEY" --signature "$file.sig" "$file"
  done
  verified=1
elif [[ -n ${GPG_KEYRING:-} ]]; then
  command -v gpg >/dev/null || { echo 'gpg is required' >&2; exit 1; }
  for file in "$ARTIFACT_DIR"/{SHA256SUMS,RELEASE_MANIFEST.json,SBOM.spdx.json,provenance.intoto.json}; do
    [[ -s $file.asc ]] || { echo "missing $file.asc" >&2; exit 1; }
    gpg --batch --no-default-keyring --keyring "$GPG_KEYRING" --verify "$file.asc" "$file"
  done
  verified=1
fi

if [[ ${REQUIRE_RPM_SIGNATURES:-0} == 1 ]]; then
  for rpm_file in "$ARTIFACT_DIR/nli_client_ver20.rpm" "$ARTIFACT_DIR/nli_controller_agent_ver20.rpm"; do
    rpm -Kv "$rpm_file" | grep -Eq 'Header V[0-9]+ RSA/SHA[0-9]+ Signature.*: OK' || { echo "RPM signature missing or invalid: $rpm_file" >&2; exit 1; }
  done
  verified=1
fi

[[ $verified == 1 ]] || { echo 'no signature verification policy configured' >&2; exit 1; }
echo 'release signatures: PASS'
