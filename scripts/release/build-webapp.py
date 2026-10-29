#!/usr/bin/env python3
"""Produce a deterministic, source-only Vercel/Cloudflare deployment bundle."""
import argparse
import datetime
import pathlib
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument("--root", required=True, type=pathlib.Path)
parser.add_argument("--output", required=True, type=pathlib.Path)
parser.add_argument("--epoch", required=True, type=int)
args = parser.parse_args()
root = args.root.resolve()
files = [
    "package.json", "package-lock.json", "vercel.json", "wrangler.jsonc", ".env.local.example",
    "web/index.html", "web/app.js", "web/styles.css", "web/nli-mark.svg",
    "web/nexora-logo-v20.png", "web/login-banner-v20.png", "web/nexora-poster-v20.png",
    "web/_headers", "web/README.txt", "web/package.json", "web/package-lock.json", "web/vite.config.js",
    "web/Open-NLI-WebView.ps1", "api/v1/[...path].js",
    "functions/api/v1/[[path]].js", "web/e2e.spec.js", "tests/vercel-proxy.test.js",
    "tests/cloudflare-proxy.test.js", "tests/vercel-preflight.test.js",
    "scripts/deploy-vercel.ps1", "scripts/vercel-preflight.mjs",
    "scripts/copy-v20-brand-assets.mjs",
]
# Include the deployment build helper when present, but never generated output,
# local environment files, dependency folders, or controller credentials.
files += [p.relative_to(root).as_posix() for p in sorted((root / "scripts").glob("*web*.mjs"))]
for optional in ("docs/WEBVIEW_SUPABASE_DEPLOYMENT.md", "docs/V20_DEPLOYMENT.md",
                 "docs/VERCEL_DEPLOYMENT.md", "docs/CLOUDFLARE_PAGES_DEPLOYMENT.md",
                 "scripts/build-vercel.mjs"):
    if (root / optional).is_file():
        files.append(optional)
timestamp = datetime.datetime.fromtimestamp(max(args.epoch, 315532800), datetime.timezone.utc).timetuple()[:6]
args.output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(args.output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
    for name in sorted(set(files)):
        source = root / name
        if not source.is_file():
            raise SystemExit(f"missing deployment input: {name}")
        info = zipfile.ZipInfo(name, timestamp)
        info.create_system = 3
        info.external_attr = 0o100644 << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        content = source.read_bytes()
        if source.suffix.lower() in {".html", ".css", ".js", ".mjs", ".json", ".md", ".txt", ".svg", ".example", ".jsonc"}:
            content = content.replace(b"\r\n", b"\n")
        archive.writestr(info, content)
print(f"Vercel/Cloudflare deployment bundle: {args.output.name}")
