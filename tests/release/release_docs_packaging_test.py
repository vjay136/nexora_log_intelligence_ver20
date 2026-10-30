#!/usr/bin/env python3
"""Verify the UPES/GPRS release guide and diagram are shipped everywhere."""

from __future__ import annotations

import argparse
from pathlib import Path


DOC = "V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md"
WIZARD = "V20_QUARANTINE_RESOLUTION_WIZARD.md"
R1_RESULTS = "V20_R1_TEST_RESULTS.md"
R1_OPERATOR = "V20_R1_QRW_OPERATOR_TEST_GUIDE.md"
R2_RESULTS = "V20_R2_TEST_RESULTS.md"
R2_OPERATOR = "V20_R2_QRW_OPERATOR_GUIDE.md"
R3_RESULTS = "V20_R3_TEST_RESULTS.md"
R3_OPERATOR = "V20_R3_QRW_OPERATOR_GUIDE.md"
DIAGRAM = "NLI_V20_UPES_GPRS_ARCHITECTURE.svg"


def fail(message: str) -> None:
    raise SystemExit(f"UPES/GPRS release-doc packaging: FAIL: {message}")


def validate(root: Path, output: Path, client: Path, controller: Path) -> None:
    sources = {
        DOC: root / "docs" / DOC,
        WIZARD: root / "docs" / WIZARD,
        R1_RESULTS: root / "docs" / R1_RESULTS,
        R1_OPERATOR: root / "docs" / R1_OPERATOR,
        R2_RESULTS: root / "docs" / R2_RESULTS,
        R2_OPERATOR: root / "docs" / R2_OPERATOR,
        R3_RESULTS: root / "docs" / R3_RESULTS,
        R3_OPERATOR: root / "docs" / R3_OPERATOR,
        DIAGRAM: root / "docs" / "architecture" / DIAGRAM,
    }
    for name, source in sources.items():
        if not source.is_file() or source.stat().st_size == 0:
            fail(f"source artifact is missing or empty: {source}")
        for target_dir in (output, client, controller):
            target = target_dir / name
            if not target.is_file() or target.read_bytes() != source.read_bytes():
                fail(f"{name} is missing or differs in {target_dir}")

    guide = sources[DOC].read_text(encoding="utf-8")
    for claim in (
        "20.0.0-2",
        "does not accept or store a raw record or source path",
        "does not claim a",
        "submitted_checksum",
        "approved_checksum",
        "independent rollout approval",
        "does not declare the release build",
    ):
        if claim not in guide:
            fail(f"guide does not document the expected release boundary: {claim!r}")

    wizard = sources[WIZARD].read_text(encoding="utf-8")
    for claim in (
        "nine-stage workflow",
        "20250812 130035122+0100 aaa-gmailrgo009 mail 3609 0 140293768701696 Note;systemlogs_for_data",
        "independent approval",
        "originating client's exact applied",
        "bundle acknowledgement",
        "AI output is advisory",
        "letters, digits, and underscores",
    ):
        if claim not in wizard:
            fail(f"wizard guide does not document the expected boundary: {claim!r}")

    results = sources[R1_RESULTS].read_text(encoding="utf-8")
    for claim in ("20.0.0-1", "62/62", "Full Go suite", "approval-gated"):
        if claim not in results:
            fail(f"R1 test report does not record expected evidence: {claim!r}")

    operator = sources[R1_OPERATOR].read_text(encoding="utf-8")
    for claim in ("Propose to all eligible clients", "Offboard", "exact desired hash", "different authorized operator"):
        if claim not in operator:
            fail(f"R1 operator guide does not document expected control: {claim!r}")

    results_r2 = sources[R2_RESULTS].read_text(encoding="utf-8")
    for claim in ("20.0.0-2", "HTTP 530", "malformed parser isolation", "automatic rollback"):
        if claim not in results_r2:
            fail(f"R2 test report does not record expected evidence: {claim!r}")

    operator_r2 = sources[R2_OPERATOR].read_text(encoding="utf-8")
    for claim in ("Verify origin acknowledgement", "Request targeted replay", "creator cannot approve", "exact desired bundle hash"):
        if claim not in operator_r2:
            fail(f"R2 operator guide does not document expected control: {claim!r}")

    results_r3 = sources[R3_RESULTS].read_text(encoding="utf-8")
    for claim in ("20.0.0-3", "terminal", "operator-defined field names", "Legacy parser"):
        if claim not in results_r3:
            fail(f"R3 test report does not record expected evidence: {claim!r}")

    operator_r3 = sources[R3_OPERATOR].read_text(encoding="utf-8")
    for claim in ("Create mappings from my field names", "Ignore / exclude", "Repair as draft", "728 accounted records"):
        if claim not in operator_r3:
            fail(f"R3 operator guide does not document expected control: {claim!r}")

    for relative in (
        "packaging/build-rpms.sh",
        "packaging/rpm/nli-client.spec",
        "packaging/rpm/nli-controller-agent.spec",
        "packaging/build-release.ps1",
        "scripts/release/build.sh",
        "scripts/release/generate-evidence.py",
        "scripts/release/verify-artifacts.sh",
        "tests/release/package-infra.sh",
    ):
        source = (root / relative).read_text(encoding="utf-8")
        for name in sources:
            if name not in source:
                fail(f"{relative} does not reference packaged artifact {name}")

    if f"![UPES and governed parser-candidate lifecycle]({DIAGRAM})" not in guide:
        fail("guide diagram link must resolve when the archive or RPM docs are read")

    for relative in (
        "CHANGELOG.md",
        "docs/release/README.md",
        "docs/release/V20_MIGRATION_AND_ROLLBACK.md",
        "packaging/rpm/nli-client.spec",
        "packaging/rpm/nli-controller-agent.spec",
    ):
        if "20.0.0-3" not in (root / relative).read_text(encoding="utf-8"):
            fail(f"{relative} does not identify the current release")

    release_env = (root / "packaging" / "release.env").read_text(encoding="utf-8")
    if "NLI_VERSION=20.0.0" not in release_env or "NLI_RELEASE=3" not in release_env:
        fail("packaging release identity is not 20.0.0-3")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--client", required=True, type=Path)
    parser.add_argument("--controller", required=True, type=Path)
    args = parser.parse_args()
    validate(args.root.resolve(), args.output.resolve(), args.client.resolve(), args.controller.resolve())
    print("UPES/GPRS release-doc packaging: PASS")


if __name__ == "__main__":
    main()
