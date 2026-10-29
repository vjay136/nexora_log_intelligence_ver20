import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(fileURLToPath(new URL('..', import.meta.url)));

test('subsystem workspaces explain every acronym and the truthful execution boundary', async () => {
  const app = await readFile(resolve(root, 'web/app.js'), 'utf8');
  for (const name of [
    'Nexora Hypersonic Log Engine',
    'Nexora Adaptive Parser Intelligence',
    'Nexora Assurance Lab',
    'Nexora Unified Real-Time Synchronization Fabric',
    'Nexora Universal Parsing Intelligence & Recovery Engine',
    'Evidence-Gated Parser Evolution',
    'Unstoppable Parsing Engine Support',
    'Guided Parser Repair Studio',
  ]) assert.ok(app.includes(name), `WebApp does not explain ${name}`);
  assert.ok(app.includes('Ordinary live record'), 'orchestration view must identify the live hot path');
  assert.ok(app.includes('Evidence-gated recovery'), 'orchestration view must identify the recovery path');
  assert.ok(app.includes('do not run on every record'), 'UI must not imply assurance/evolution is in the hot path');
});

test('operator orchestration document keeps successful and recovery paths separate', async () => {
  const guide = await readFile(resolve(root, 'docs/V20_SUBSYSTEM_ORCHESTRATION.md'), 'utf8');
  assert.match(guide, /source -> NHLE -> customer destination\/checkpoint -> NURSF telemetry -> WebApp/);
  assert.match(guide, /quarantine -> UPES bounded observation \+ operator GPRS\/NAL diagnosis -> optional NAPI\/NUPIRE\/EGPE evidence/);
  assert.match(guide, /Targeted replay remains blocked until the originating client acknowledges/);
  assert.match(guide, /NAPI, NAL, NUPIRE, EGPE, UPES, and GPRS do not execute during this successful-record path/);
  assert.match(guide, /AI as the final recovery option/);
  assert.match(guide, /Successful records never enter the AI queue/);
});
