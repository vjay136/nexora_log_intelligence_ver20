#!/usr/bin/env python3
import argparse
import base64
import binascii
import datetime as dt
import hashlib
import json
import pathlib
import re
import uuid

DELIVERABLES = (
    "nli_client_ver20.rpm",
    "nli_clinet_ver20.rpm",
    "nli_controller_agent_ver20.rpm",
    "webview_ver20.zip",
    "webapp_ver20.zip",
    "install-controller-ver20.sh",
    "install-client-ver20.sh",
    "install-controller-aws-ver20.sh",
    "install-client-aws-ver20.sh",
    "install-controller-aws-cloudflare-ver20.sh",
    "aws-rhel-preflight-ver20.sh",
    "aws-security-group-plan-ver20.sh",
    "setup-cloudflare-tunnel-v20.sh",
    "verify-cloudflare-tunnel-v20.sh",
    "uninstall-cloudflare-tunnel-v20.sh",
    "cloudflare-quick-diagnosis-v20.sh",
    "vercel-preflight.mjs",
    "uninstall-controller-ver20.sh",
    "uninstall-client-ver20.sh",
    "INSTALLATION_AND_CONFIGURATION.md",
    "V20_AWS_RED_HAT_INSTALLATION.md",
    "V20_CLOUDFLARE_TUNNEL.md",
    "VERCEL_DEPLOYMENT.md",
    "CLOUDFLARE_PAGES_DEPLOYMENT.md",
    "WEBAPP_OPERATOR_AND_PARSER_GUIDE.md",
    "V20_OPERATOR_DEMO_GUIDE.md",
    "V20_PRODUCTION_ACCEPTANCE_TESTS.md",
    "V20_SUBSYSTEM_ORCHESTRATION.md",
    "V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md",
    "V20_R1_TEST_RESULTS.md",
    "V20_R1_QRW_OPERATOR_TEST_GUIDE.md",
    "V20_QUARANTINE_RESOLUTION_WIZARD.md",
    "NLI_V20_UPES_GPRS_ARCHITECTURE.svg",
    "PARSER_CATALOG.md",
    "V20_MIGRATION_AND_ROLLBACK.md",
    "BENCHMARKS.txt",
    "SAMPLE_FIELD_ACCOUNTING.json",
    "RELEASE_ID",
)


def digest(path: pathlib.Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def load_env(path: pathlib.Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if line and not line.startswith("#"):
            key, value = line.split("=", 1)
            values[key] = value
    return values


def write_json(path: pathlib.Path, value: object) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8", newline="\n")


def parse_modules(text: str) -> list[tuple[str, str, str]]:
    modules: set[tuple[str, str, str]] = set()
    for line in text.splitlines():
        match = re.match(r"\s*(mod|dep)\s+([^\s]+)\s+([^\s]+)(?:\s+h1:([^\s]+))?", line)
        if match:
            checksum = ""
            if match.group(4):
                try:
                    checksum = base64.b64decode(match.group(4), validate=True).hex()
                except (ValueError, binascii.Error):
                    checksum = ""
            modules.add((match.group(2), match.group(3), checksum))
    return sorted(modules)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifacts", required=True, type=pathlib.Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--release", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--source-date-epoch", required=True, type=int)
    parser.add_argument("--images", required=True, type=pathlib.Path)
    args = parser.parse_args()

    out = args.artifacts.resolve()
    files = [out / name for name in DELIVERABLES]
    missing = [str(path) for path in files if not path.is_file()]
    if missing:
        raise SystemExit("missing deliverables: " + ", ".join(missing))

    subjects = [{"name": path.name, "digest": {"sha256": digest(path)}} for path in files]
    sizes = {path.name: path.stat().st_size for path in files}
    images = load_env(args.images)
    created = dt.datetime.fromtimestamp(args.source_date_epoch, tz=dt.timezone.utc).isoformat().replace("+00:00", "Z")

    checksums = "".join(f"{item['digest']['sha256']}  {item['name']}\n" for item in subjects)
    (out / "SHA256SUMS").write_text(checksums, encoding="ascii", newline="\n")

    manifest = {
        "schema_version": 1,
        "product": "Nexora Log Intelligence Ver20",
        "version": args.version,
        "release": args.release,
        "source_revision": args.revision,
        "source_date_epoch": args.source_date_epoch,
        "builder_images": images,
        "artifacts": [
            {"name": item["name"], "size": sizes[item["name"]], "sha256": item["digest"]["sha256"]}
            for item in subjects
        ],
    }
    write_json(out / "RELEASE_MANIFEST.json", manifest)

    provenance_text = (out / "SOURCE_BINARY_PROVENANCE.txt").read_text(encoding="utf-8")
    modules = parse_modules(provenance_text)
    namespace_seed = hashlib.sha256((args.revision + args.version + args.release).encode()).hexdigest()
    packages = [{
        "SPDXID": "SPDXRef-Product",
        "name": "nli-v20-release",
        "versionInfo": f"{args.version}-{args.release}",
        "downloadLocation": "NOASSERTION",
        "filesAnalyzed": False,
        "licenseConcluded": "LicenseRef-Proprietary",
        "licenseDeclared": "LicenseRef-Proprietary",
        "copyrightText": "NOASSERTION",
    }]
    relationships = [{
        "spdxElementId": "SPDXRef-DOCUMENT",
        "relationshipType": "DESCRIBES",
        "relatedSpdxElement": "SPDXRef-Product",
    }]
    for index, item in enumerate(subjects, start=1):
        spdx_id = f"SPDXRef-Artifact-{index}"
        packages.append({
            "SPDXID": spdx_id,
            "name": item["name"],
            "versionInfo": f"{args.version}-{args.release}",
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": False,
            "licenseConcluded": "LicenseRef-Proprietary",
            "licenseDeclared": "LicenseRef-Proprietary",
            "copyrightText": "NOASSERTION",
            "checksums": [{"algorithm": "SHA256", "checksumValue": item["digest"]["sha256"]}],
        })
        relationships.append({"spdxElementId": "SPDXRef-Product", "relationshipType": "CONTAINS", "relatedSpdxElement": spdx_id})

    for index, (name, version, checksum) in enumerate(modules, start=1):
        spdx_id = f"SPDXRef-GoModule-{index}"
        package = {
            "SPDXID": spdx_id,
            "name": name,
            "versionInfo": version,
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": False,
            "licenseConcluded": "NOASSERTION",
            "licenseDeclared": "NOASSERTION",
            "copyrightText": "NOASSERTION",
        }
        if checksum:
            package["checksums"] = [{"algorithm": "SHA256", "checksumValue": checksum}]
        packages.append(package)
        relationships.append({"spdxElementId": "SPDXRef-Product", "relationshipType": "DEPENDS_ON", "relatedSpdxElement": spdx_id})
    sbom = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"nli-{args.version}-{args.release}",
        "documentNamespace": f"https://nexora.local/spdx/{namespace_seed}",
        "creationInfo": {"created": created, "creators": ["Tool: nli-release-evidence/1"]},
        "packages": packages,
        "relationships": relationships,
    }
    write_json(out / "SBOM.spdx.json", sbom)

    statement = {
        "_type": "https://in-toto.io/Statement/v1",
        "subject": subjects,
        "predicateType": "https://slsa.dev/provenance/v1",
        "predicate": {
            "buildDefinition": {
                "buildType": "https://nexora.local/buildtypes/nli-rpm/v1",
                "externalParameters": {"version": args.version, "release": args.release, "architecture": "x86_64"},
                "internalParameters": {"sourceDateEpoch": args.source_date_epoch, "images": images},
                "resolvedDependencies": [{"uri": f"git+file:nli-v20@{args.revision}", "digest": {"gitCommit": args.revision}}],
            },
            "runDetails": {
                "builder": {"id": "https://nexora.local/builders/nli-release-v1"},
                "metadata": {"invocationId": str(uuid.UUID(namespace_seed[:32])), "startedOn": created, "finishedOn": created},
            },
        },
    }
    write_json(out / "provenance.intoto.json", statement)


if __name__ == "__main__":
    main()
