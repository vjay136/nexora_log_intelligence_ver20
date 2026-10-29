#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_DIR=${1:?usage: sign-artifacts.sh ARTIFACT_DIR [rpm|attest]}
PHASE=${2:-attest}
REQUIRE_SIGNATURES=${REQUIRE_SIGNATURES:-0}
signed=0

if [[ $PHASE == rpm && -n ${RPM_GPG_NAME:-} ]]; then
  command -v rpmsign >/dev/null || { echo 'rpmsign is required for RPM signing' >&2; exit 1; }
  for rpm in "$ARTIFACT_DIR/nli_client_ver20.rpm" "$ARTIFACT_DIR/nli_controller_agent_ver20.rpm"; do
    rpmsign --define "_gpg_name $RPM_GPG_NAME" --addsign "$rpm"
  done
  cp -f "$ARTIFACT_DIR/nli_client_ver20.rpm" "$ARTIFACT_DIR/nli_clinet_ver20.rpm"
  signed=1
fi

if [[ $PHASE == attest && -n ${COSIGN_KEY:-} ]]; then
  command -v cosign >/dev/null || { echo 'cosign is required for detached signatures' >&2; exit 1; }
  for file in "$ARTIFACT_DIR"/{SHA256SUMS,RELEASE_MANIFEST.json,SBOM.spdx.json,provenance.intoto.json}; do
    cosign sign-blob --yes --key "env://COSIGN_KEY" --output-signature "$file.sig" "$file"
  done
  signed=1
elif [[ $PHASE == attest && -n ${GPG_KEY_ID:-} ]]; then
  command -v gpg >/dev/null || { echo 'gpg is required for detached signatures' >&2; exit 1; }
  for file in "$ARTIFACT_DIR"/{SHA256SUMS,RELEASE_MANIFEST.json,SBOM.spdx.json,provenance.intoto.json}; do
    gpg --batch --yes --local-user "$GPG_KEY_ID" --armor --detach-sign --output "$file.asc" "$file"
  done
  signed=1
fi

if [[ $REQUIRE_SIGNATURES == 1 && $signed == 0 ]]; then
  echo "required $PHASE signing credentials were not supplied" >&2
  exit 1
fi
[[ $signed == 1 ]] && echo "$PHASE signing: complete" || echo "$PHASE signing: skipped (no key configured)"
