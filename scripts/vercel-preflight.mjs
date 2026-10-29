import { lookup } from 'node:dns/promises';
import { readFile } from 'node:fs/promises';
import { isIP } from 'node:net';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { isPrivateHost } from '../api/v1/[...path].js';

const timeoutMs = 12_000;

export function validateOrigin(raw) {
  let url;
  try { url = new URL(String(raw || '').trim()); } catch { throw new Error('Set NLI_CONTROLLER_ORIGIN to the controller public HTTPS origin.'); }
  if (url.protocol !== 'https:' || !url.hostname || isIP(url.hostname) !== 0 || url.username || url.password || url.pathname !== '/' || url.search || url.hash || isPrivateHost(url.hostname)) {
    throw new Error('NLI_CONTROLLER_ORIGIN must be a public HTTPS origin with no credentials, path, query, or fragment.');
  }
  return url;
}

export async function assertPublicDNS(hostname, lookupImpl = lookup) {
  if (isIP(hostname)) return;
  let records;
  try { records = await lookupImpl(hostname, { all: true, verbatim: true }); }
  catch { throw new Error('Controller hostname does not resolve in public DNS.'); }
  if (!records.length || records.some(record => isPrivateHost(record.address))) {
    throw new Error('Controller hostname has no public address or resolves to a private/internal address.');
  }
}

async function fetchChecked(url, options = {}) {
  const response = await fetch(url, { ...options, redirect: 'manual', signal: AbortSignal.timeout(timeoutMs) });
  if (response.status >= 300 && response.status < 400) throw new Error('Endpoint redirected; configure the final HTTPS hostname as NLI_CONTROLLER_ORIGIN.');
  return response;
}

export async function runPreflight({ origin, appUrl, lookupImpl = assertPublicDNS } = {}) {
  const controller = validateOrigin(origin);
  await lookupImpl(controller.hostname);
  const health = await fetchChecked(new URL('/api/v1/health', controller), { headers: { accept: 'application/json' } });
  if (!health.ok || health.headers.get('x-nli-controller') !== 'v20') throw new Error('Controller health contract failed (expected HTTP 200 and X-NLI-Controller v20).');
  let payload;
  try { payload = await health.json(); } catch { throw new Error('Controller health response was not valid JSON.'); }
  if (payload.service !== 'nli-controller-agent' || payload.api_version !== 'v1') throw new Error('Controller health payload is incompatible.');
  if (!health.headers.get('cf-ray')) throw new Error('Controller hostname did not return Cloudflare CF-RAY; verify its public hostname is routed through Cloudflare.');

  const login = await fetchChecked(new URL('/api/v1/auth/login', controller), { method: 'OPTIONS', headers: { accept: 'application/json' } });
  const allowed = (login.headers.get('allow') || '').split(',').map(value => value.trim().toUpperCase());
  if (login.status !== 204 || !allowed.includes('POST') || !allowed.includes('OPTIONS') || login.headers.get('x-nli-controller') !== 'v20' || login.headers.get('cache-control') !== 'no-store') {
    throw new Error('Controller login OPTIONS contract failed; preserve OPTIONS, Allow, X-NLI-Controller, and Cache-Control through Cloudflare.');
  }

  if (appUrl) {
    let app;
    try { app = new URL(appUrl); } catch { throw new Error('Optional --app-url must be an absolute HTTPS deployment URL.'); }
    if (app.protocol !== 'https:' || app.username || app.password || app.search || app.hash || app.pathname !== '/') throw new Error('--app-url must be an HTTPS origin.');
    const proxied = await fetchChecked(new URL('/api/v1/health', app), { headers: { accept: 'application/json' } });
    if (!proxied.ok || proxied.headers.get('x-nli-upstream') !== 'controller' || proxied.headers.get('x-nli-controller') !== 'v20') {
      throw new Error('Vercel same-origin proxy health contract failed; check the production environment variable and redeploy.');
    }
    console.log('Vercel same-origin /api/v1/health proxy contract: passed.');
  }
  console.log('Controller public DNS, Cloudflare edge, HTTPS health, and login OPTIONS contract: passed.');
  console.log('Browser API traffic is same-origin through Vercel; no cross-origin browser CORS permission is required.');
}

function argValue(args, name) {
  const index = args.indexOf(name);
  return index < 0 ? undefined : args[index + 1];
}

async function localOrigin() {
  if (process.env.NLI_CONTROLLER_ORIGIN) return process.env.NLI_CONTROLLER_ORIGIN;
  try {
    const source = await readFile(resolve(dirname(fileURLToPath(import.meta.url)), '..', '.env.local'), 'utf8');
    const match = source.match(/^\s*NLI_CONTROLLER_ORIGIN\s*=\s*(.*?)\s*$/m);
    return match?.[1]?.replace(/^(?:"(.*)"|'(.*)')$/, (_, doubleQuoted, singleQuoted) => doubleQuoted ?? singleQuoted) || '';
  } catch { return ''; }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    await runPreflight({ origin: argValue(process.argv.slice(2), '--origin') || await localOrigin(), appUrl: argValue(process.argv.slice(2), '--app-url') });
  } catch (error) {
    console.error(`Vercel preflight failed: ${error.message}`);
    process.exitCode = 1;
  }
}
