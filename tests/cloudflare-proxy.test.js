import test from 'node:test';
import assert from 'node:assert/strict';
import { onRequest } from '../functions/api/v1/[[path]].js';

test('Cloudflare proxy requires a public HTTPS controller origin', async () => {
  const response = await onRequest({ request: new Request('https://app.example.com/api/v1/health'), env: { NLI_CONTROLLER_ORIGIN: 'https://192.168.1.20:12382' } });
  assert.equal(response.status, 503);
  assert.match((await response.json()).error, /public HTTPS DNS hostname/);
});

test('Cloudflare proxy forwards same-origin auth headers to a public controller DNS hostname', async t => {
  const originalFetch = globalThis.fetch;
  let observed;
  globalThis.fetch = async (url, options) => {
    observed = { url: String(url), options };
    return new Response(JSON.stringify({ service: 'nli-controller-agent' }), { status: 200, headers: { 'content-type': 'application/json', 'x-nli-controller': 'v20' } });
  };
  t.after(() => { globalThis.fetch = originalFetch; });
  const response = await onRequest({ request: new Request('https://app.example.com/api/v1/system?detail=1', { headers: { cookie: 'nli_session=test', 'x-csrf-token': 'csrf', authorization: 'Bearer caller-token' } }), env: { NLI_CONTROLLER_ORIGIN: 'https://controller.example.com:8443' } });
  assert.equal(response.status, 200);
  assert.equal(observed.url, 'https://controller.example.com:8443/api/v1/system?detail=1');
  assert.equal(observed.options.headers.get('cookie'), 'nli_session=test');
  assert.equal(observed.options.headers.get('x-csrf-token'), 'csrf');
  assert.equal(observed.options.headers.get('authorization'), 'Bearer caller-token');
  assert.equal(observed.options.headers.get('x-nli-proxy'), 'cloudflare-pages');
  assert.equal(response.headers.get('x-nli-controller'), 'v20');
  assert.equal(response.headers.get('cache-control'), 'no-store');
});

test('Cloudflare proxy preserves a streaming SSE body and advertises reconnect mode', async t => {
  const originalFetch = globalThis.fetch;
  let releaseSecond;
  globalThis.fetch = async () => new Response(new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode('event: snapshot\ndata: {"files":[]}\n\n'));
      releaseSecond = () => controller.enqueue(new TextEncoder().encode(': heartbeat\n\n'));
    },
  }), { status: 200, headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' } });
  t.after(() => { globalThis.fetch = originalFetch; });
  const response = await onRequest({ request: new Request('https://app.example.com/api/v1/telemetry/stream', { headers: { accept: 'text/event-stream' } }), env: { NLI_CONTROLLER_ORIGIN: 'https://controller.example.com' } });
  assert.equal(response.headers.get('x-nli-stream-mode'), 'sse');
  assert.equal(response.headers.get('content-type'), 'text/event-stream');
  assert.equal(response.headers.get('cache-control'), 'no-cache');
  releaseSecond();
  const reader = response.body.getReader();
  const { value } = await reader.read();
  assert.match(new TextDecoder().decode(value), /event: snapshot/);
  await reader.cancel();
});
