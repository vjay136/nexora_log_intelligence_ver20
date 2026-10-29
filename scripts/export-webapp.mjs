import { cp, mkdir, rm, stat, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const target = resolve(root, 'build/web-only');
const required = ['web/index.html', 'web/app.js', 'web/styles.css', 'web/nexora-logo-v20.png', 'web/login-banner-v20.png', 'web/nexora-poster-v20.png', 'web/vite.config.js', 'web/package.json', 'api/v1/[...path].js', 'vercel.json'];
for (const name of required) {
  try {
    await stat(resolve(root, name));
  } catch {
    throw new Error(`Cannot export WebApp: required source is missing: ${name}`);
  }
}
await rm(target, { recursive: true, force: true });
await mkdir(target, { recursive: true });
await mkdir(resolve(target, 'web'), { recursive: true });
for (const name of ['index.html', 'app.js', 'styles.css', 'nexora-logo-v20.png', 'login-banner-v20.png', 'nexora-poster-v20.png', 'nli-mark.svg', '_headers', 'vite.config.js', 'package.json', 'package-lock.json', 'README.txt']) {
  await cp(resolve(root, 'web', name), resolve(target, 'web', name));
}
for (const name of ['api', 'functions']) await cp(resolve(root, name), resolve(target, name), { recursive: true });
await mkdir(resolve(target, 'scripts'), { recursive: true });
for (const name of ['build-webapp.mjs', 'copy-v20-brand-assets.mjs']) {
  await cp(resolve(root, 'scripts', name), resolve(target, 'scripts', name));
}
await cp(resolve(root, 'vercel.json'), resolve(target, 'vercel.json'));
await cp(resolve(root, 'package.json'), resolve(target, 'package.json'));
await cp(resolve(root, 'package-lock.json'), resolve(target, 'package-lock.json'));
const manifest = { name: 'nexora-log-intelligence-v20-web-deploy', version: '20.0.0', private: true, type: 'module', engines: { node: '24.x' }, scripts: { build: 'npm --prefix web run build' } };
await writeFile(resolve(target, 'package.json'), `${JSON.stringify(manifest, null, 2)}\n`);
await rm(resolve(target, 'package-lock.json'), { force: true });
await writeFile(resolve(target, '.gitignore'), `.vercel/\nbuild/\nnode_modules/\n.env\n.env.*\n!.env.example\n`);
await writeFile(resolve(target, '.env.example'), `# Server-side Vercel variable. Use the stable public HTTPS hostname created by your Cloudflare Tunnel.\n# Do not add a trailing slash or /api/v1 path. Do not put controller, Supabase, or AI secrets in this repository.\nNLI_CONTROLLER_ORIGIN=https://nli-controller.example.com\n`);
await writeFile(resolve(target, 'README.md'), `# Nexora Log Intelligence V20 Web Deployment\n\nThis repository contains only the Vite WebApp and its same-origin Vercel API proxy. It excludes the controller/client source, RPM build inputs, Supabase schema, browser tests, and local operator scripts.\n\n## Deploy on Vercel\n\n1. Import this repository and keep **Root Directory** set to \`./\`.\n2. Select the supported **Vite** preset. The committed \`vercel.json\` supplies the install, build, output, function, and security-header settings.\n3. Add one server-side environment variable in Production and Preview:\n   \`NLI_CONTROLLER_ORIGIN=https://<your-stable-public-controller-hostname>\`\n4. Use a public HTTPS DNS hostname provided by a named Cloudflare Tunnel or another trusted reverse proxy. Do not use an EC2 private DNS name, RFC1918 address, localhost, a port, a path, or a trailing slash.\n5. Deploy. The browser calls same-origin \`/api/v1/*\`; the included serverless function forwards those requests to the controller.\n\nNever add the enrollment token, controller HMAC/encryption keys, Supabase secret keys, AI keys, or \`.env*\` files to this repository or Vercel browser variables.\n\n## Static and serverless contract\n\nThe single application implementation is the Vite build in \`web/\`; its production files are emitted to \`build/vite-web\`. A host that serves static files can use that directory for the UI. Authenticated controller access also requires a server-side function at \`/api/v1/*\` that forwards requests to \`NLI_CONTROLLER_ORIGIN\` while preserving the request method, path, query, headers, cookies, and response headers. Keep controller credentials and the origin out of browser code. Platform-specific function adapters belong in the deployment repository for that host; do not fork the WebApp source.\n\nSee [DEPLOYMENT.md](DEPLOYMENT.md) for the platform-neutral contract and Vercel details.\n\n## Verify locally\n\nRun \`npm ci --prefix web --ignore-scripts\` and \`npm run build\`. The output is generated at \`build/vite-web\`.\n`);
await writeFile(resolve(target, 'DEPLOYMENT.md'), `# Deployment contract\n\nThis export has one UI implementation: \`web/\`, built by Vite into \`build/vite-web\`. Choose a host that can serve that static directory and run a server-side function route for \`/api/v1/*\`.\n\nThe function reads the server-only \`NLI_CONTROLLER_ORIGIN\` setting and forwards the incoming HTTP method, path, query, headers, cookies, and body to the controller. It returns the controller status, response headers, and body to the same-origin browser request. The browser must use same-origin API paths. The controller origin must be a public HTTPS origin reachable by the selected host.\n\nVercel is configured as the supported preset in \`vercel.json\`: Vite build output, an API catch-all function, and security headers. For another static/serverless platform, configure its static output and function routing to satisfy the contract above; retain this Vite source and do not create a second frontend implementation. Add platform-specific adapter files only in the dedicated export repository.\n\nNever expose controller credentials, enrollment tokens, encryption keys, Supabase secret keys, or AI provider keys to the browser or deployment build output.\n`);
console.log(`Created clean web-only deployment source at ${target}`);
