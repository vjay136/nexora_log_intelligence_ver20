import test from 'node:test';
import assert from 'node:assert/strict';
import { assertPublicDNS, runPreflight, validateOrigin } from '../scripts/vercel-preflight.mjs';

test('Vercel preflight accepts an HTTPS DNS origin and rejects local, private, or decorated origins', () => {
  assert.equal(validateOrigin('https://nli.example.net').origin, 'https://nli.example.net');
  for (const origin of ['http://nli.example.net', 'https://localhost', 'https://localhost.', 'https://node.local', 'https://node.internal', 'https://192.168.1.5', 'https://1.1.1.1', 'https://[fd00::1]', 'https://user:pass@nli.example.net', 'https://nli.example.net/path']) {
    assert.throws(() => validateOrigin(origin), /public HTTPS origin/);
  }
});

test('Vercel preflight checks DNS, Cloudflare health, login OPTIONS, and same-origin proxy contract', async t => {
  const originalFetch = globalThis.fetch;
  const requested = [];
  globalThis.fetch = async (url, options) => {
    requested.push({ url: String(url), options });
    if (String(url) === 'https://nli.example.net/api/v1/health') return new Response(JSON.stringify({ service: 'nli-controller-agent', api_version: 'v1' }), { status: 200, headers: { 'x-nli-controller': 'v20', 'cf-ray': 'test-ray' } });
    if (String(url) === 'https://nli.example.net/api/v1/auth/login') return new Response(null, { status: 204, headers: { allow: 'POST, OPTIONS', 'x-nli-controller': 'v20', 'cache-control': 'no-store' } });
    return new Response(JSON.stringify({ service: 'nli-controller-agent', api_version: 'v1' }), { status: 200, headers: { 'x-nli-controller': 'v20', 'x-nli-upstream': 'controller' } });
  };
  t.after(() => { globalThis.fetch = originalFetch; });
  let lookedUp;
  await runPreflight({ origin: 'https://nli.example.net', appUrl: 'https://nli.vercel.app', lookupImpl: async host => { lookedUp = host; } });
  assert.equal(lookedUp, 'nli.example.net');
  assert.deepEqual(requested.map(item => item.url), [
    'https://nli.example.net/api/v1/health',
    'https://nli.example.net/api/v1/auth/login',
    'https://nli.vercel.app/api/v1/health',
  ]);
  assert.equal(requested[1].options.method, 'OPTIONS');
  assert.equal(requested.some(item => JSON.stringify(item.options.headers || {}).includes('authorization')), false);
});

test('Vercel preflight rejects DNS that resolves to any private address', async () => {
  await assert.rejects(assertPublicDNS('controller.example.net', async () => [
    { address: '203.0.113.8' },
    { address: '10.0.0.12' },
  ]), /private\/internal address/);
});
