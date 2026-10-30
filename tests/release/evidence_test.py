#!/usr/bin/env python3
import hashlib
import json
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest


class EvidenceGeneratorTest(unittest.TestCase):
    def test_deterministic_valid_evidence(self) -> None:
        root = pathlib.Path(__file__).resolve().parents[2]
        generator = root / "scripts" / "release" / "generate-evidence.py"
        with tempfile.TemporaryDirectory() as directory:
            artifacts = pathlib.Path(directory)
            names = (
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
                "V20_R2_TEST_RESULTS.md",
                "V20_R2_QRW_OPERATOR_GUIDE.md",
                "V20_R3_TEST_RESULTS.md",
                "V20_R3_QRW_OPERATOR_GUIDE.md",
                "V20_QUARANTINE_RESOLUTION_WIZARD.md",
                "NLI_V20_UPES_GPRS_ARCHITECTURE.svg",
                "PARSER_CATALOG.md",
                "V20_MIGRATION_AND_ROLLBACK.md",
                "BENCHMARKS.txt",
                "SAMPLE_FIELD_ACCOUNTING.json",
                "RELEASE_ID",
            )
            for index, name in enumerate(names):
                if name == "BENCHMARKS.txt":
                    content = b"BenchmarkFixture-8  5  100 ns/op\n"
                elif name == "SAMPLE_FIELD_ACCOUNTING.json":
                    content = b'{"verdict":"pass"}\n'
                elif name == "RELEASE_ID":
                    content = b"NLI_VERSION=20.0.0\nNLI_RELEASE=2\nNLI_ARCH=x86_64\nSOURCE_REVISION=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"
                else:
                    content = f"fixture-{index}".encode()
                (artifacts / name).write_bytes(content)
            (artifacts / "SOURCE_BINARY_PROVENANCE.txt").write_text(
                "\tmod\tnexora.local/nli-v20\t(devel)\n"
                "\tdep\texample.invalid/module\tv1.2.3\th1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
                encoding="utf-8",
            )
            images = artifacts / "images.env"
            images.write_text("GO_IMAGE=example.invalid/go@sha256:abc\n", encoding="utf-8")
            command = [
                sys.executable,
                str(generator),
                "--artifacts", str(artifacts),
                "--version", "20.0.0",
                "--release", "7",
                "--revision", "a" * 40,
                "--source-date-epoch", "1791200000",
                "--images", str(images),
            ]
            subprocess.run(command, check=True)
            evidence_names = ("SHA256SUMS", "RELEASE_MANIFEST.json", "SBOM.spdx.json", "provenance.intoto.json")
            first = {name: (artifacts / name).read_bytes() for name in evidence_names}
            subprocess.run(command, check=True)
            self.assertEqual(first, {name: (artifacts / name).read_bytes() for name in evidence_names})

            checksums = (artifacts / "SHA256SUMS").read_text(encoding="ascii").splitlines()
            self.assertEqual(len(checksums), len(names))
            for line, name in zip(checksums, names):
                expected = hashlib.sha256((artifacts / name).read_bytes()).hexdigest()
                self.assertEqual(line, f"{expected}  {name}")

            sbom = json.loads((artifacts / "SBOM.spdx.json").read_text(encoding="utf-8"))
            ids = {package["SPDXID"] for package in sbom["packages"]} | {"SPDXRef-DOCUMENT"}
            for relationship in sbom["relationships"]:
                self.assertIn(relationship["spdxElementId"], ids)
                self.assertIn(relationship["relatedSpdxElement"], ids)
            for package in sbom["packages"]:
                for checksum in package.get("checksums", []):
                    self.assertRegex(checksum["checksumValue"], re.compile(r"^[0-9a-f]{64}$"))

            provenance = json.loads((artifacts / "provenance.intoto.json").read_text(encoding="utf-8"))
            self.assertEqual(provenance["_type"], "https://in-toto.io/Statement/v1")
            self.assertEqual(len(provenance["subject"]), len(names))


if __name__ == "__main__":
    unittest.main()
