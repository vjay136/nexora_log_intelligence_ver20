import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
test('V20 operator guide inventories every current primary navigation route', async () => {
  const app = await readFile(resolve(root, 'web/app.js'), 'utf8');
  const guide = await readFile(resolve(root, 'docs/V20_OPERATOR_DEMO_GUIDE.md'), 'utf8');
  const nav = app.match(/const NAV = \[([\s\S]*?)\n  \];/)?.[1];
  assert.ok(nav, 'NAV route list must remain discoverable');
  const ids = [...nav.matchAll(/\['[^']+',\s*'([^']+)'\s*,\s*'([^']+)'/g)].map(([, id, title]) => [id, title]);
  assert.ok(ids.length > 0, 'NAV should contain routes');
  for (const [id, title] of ids) {
    assert.ok(guide.includes(`\`${id}\``), `guide missing route id ${id} (${title})`);
  }
  for (const label of ['Unified Sync Fabric', 'NHLE Live', 'NAPI Live', 'NAL Live', 'NUPIRE / EGPE', 'Monitoring (11 views)', 'Assurance Lab (11 tools)', 'UPES · Parser Recovery', 'Guided Parser Repair Studio (GPRS)']) {
    assert.ok(guide.includes(label), `guide missing significant workspace section ${label}`);
  }
});

test('shared time formatter hides zero and pre-2000 timestamps', async () => {
  const app = await readFile(resolve(root, 'web/app.js'), 'utf8');
  assert.match(app, /date\.getUTCFullYear\(\)\s*<\s*2000/);
});
