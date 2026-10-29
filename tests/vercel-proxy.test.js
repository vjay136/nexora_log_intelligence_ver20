import test from 'node:test';
import assert from 'node:assert/strict';
import handler from '../api/v1/[...path].js';

function responseMock() {
  return {
    statusCode: 200,
    headers: {},
    body: null,
    ended: false,
    status(code) { this.statusCode = code; return this; },
    json(value) { this.body = value; this.ended = true; return this; },
    setHeader(name, value) { this.headers[name.toLowerCase()] = value; },
    end() { this.ended = true; },
  };
}

test('proxy fails closed without a configured controller', async () => {
  delete process.env.NLI_CONTROLLER_ORIGIN;
  const response = responseMock();
  await handler({ method: 'GET', url: '/api/v1/health', headers: {} }, response);
  assert.equal(response.statusCode, 503);
  assert.match(response.body.error, /not configured/);
});

test('proxy rejects private controller origins', async () => {
  process.env.NLI_CONTROLLER_ORIGIN = 'https://192.168.1.20';
  const response = responseMock();
  await handler({ method: 'GET', url: '/api/v1/health', headers: {} }, response);
  assert.equal(response.statusCode, 503);
  assert.match(response.body.error, /public HTTPS hostname/);
});

test('proxy requires a hostname so traffic remains on the configured Cloudflare edge', async () => {
  process.env.NLI_CONTROLLER_ORIGIN = 'https://1.1.1.1';
  const response = responseMock();
  await handler({ method: 'GET', url: '/api/v1/health', headers: {} }, response);
  assert.equal(response.statusCode, 503);
  assert.match(response.body.error, /public HTTPS hostname/);
  delete process.env.NLI_CONTROLLER_ORIGIN;
});

test('proxy forwards only the controller API path and authenticated headers', async () => {
  process.env.NLI_CONTROLLER_ORIGIN = 'https://controller.example.com';
  const originalFetch = globalThis.fetch;
  let observed;
  globalThis.fetch = async (url, options) => {
    observed = { url: String(url), options };
    return new Response(null, { status: 204, headers: { 'cache-control': 'no-store', authorization: 'Bearer must-not-reflect' } });
  };
  try {
    const response = responseMock();
    response.destroyed = true;
    await handler({
      method: 'GET',
      url: '/api/v1/system?detail=1',
      headers: { cookie: 'nli_token=test', 'x-csrf-token': 'csrf', authorization: 'Bearer node-token' },
    }, response);
    assert.equal(response.statusCode, 204);
    assert.equal(observed.url, 'https://controller.example.com/api/v1/system?detail=1');
    assert.equal(observed.options.headers.get('cookie'), 'nli_token=test');
    assert.equal(observed.options.headers.get('x-csrf-token'), 'csrf');
    assert.equal(observed.options.headers.get('authorization'), 'Bearer node-token');
    assert.equal(response.headers.authorization, undefined);
    assert.equal(response.headers['x-nli-upstream'], 'controller');
  } finally {
    globalThis.fetch = originalFetch;
    delete process.env.NLI_CONTROLLER_ORIGIN;
  }
});



test('proxy preserves the NLI controller login preflight contract', async () => {
  process.env.NLI_CONTROLLER_ORIGIN = 'https://controller.example.com';
  const originalFetch = globalThis.fetch;
  let observed;
  globalThis.fetch = async (url, options) => {
    observed = { url: String(url), options };
    return new Response(null, {
      status: 204,
      headers: {
        allow: 'POST, OPTIONS',
        'cache-control': 'no-store',
        'x-nli-controller': 'v20',
      },
    });
  };
  try {
    const response = responseMock();
    await handler({ method: 'OPTIONS', url: '/api/v1/auth/login', headers: { accept: 'application/json' } }, response);
    assert.equal(response.statusCode, 204);
    assert.equal(observed.url, 'https://controller.example.com/api/v1/auth/login');
    assert.equal(observed.options.body, undefined);
    assert.equal(response.headers.allow, 'POST, OPTIONS');
    assert.equal(response.headers['x-nli-controller'], 'v20');
    assert.equal(response.headers['x-nli-upstream'], 'controller');
  } finally {
    globalThis.fetch = originalFetch;
    delete process.env.NLI_CONTROLLER_ORIGIN;
  }
});

test('proxy streams the first SSE event promptly and advertises browser reconnect mode', async () => {
  process.env.NLI_CONTROLLER_ORIGIN = 'https://controller.example.com';
  const originalFetch = globalThis.fetch;
  let releaseSecond;
  globalThis.fetch = async () => new Response('event: snapshot\ndata: {"files":[]}\n\n', { status: 200, headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' } });
  try {
    const response = responseMock();
    response.destroy = () => {};
    let firstChunk;
    const first = new Promise(resolve => { response.write = chunk => { if (firstChunk === undefined) { firstChunk = String(chunk); resolve(firstChunk); } return true; }; });
    const handling = handler({ method: 'GET', url: '/api/v1/telemetry/stream', headers: { accept: 'text/event-stream' } }, response);
    assert.match(await Promise.race([first, new Promise((_, reject) => setTimeout(() => reject(new Error('SSE first chunk was buffered')), 1000))]), /event: snapshot/);
    assert.equal(response.headers['x-nli-stream-mode'], 'sse');
    assert.equal(response.headers['content-type'], 'text/event-stream');
    assert.equal(response.headers['cache-control'], 'no-cache');
    await handling;
  } finally {
    globalThis.fetch = originalFetch;
    delete process.env.NLI_CONTROLLER_ORIGIN;
  }
});
