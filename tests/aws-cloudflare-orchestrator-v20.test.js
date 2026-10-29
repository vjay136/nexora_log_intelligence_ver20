import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('../scripts/install-controller-aws-cloudflare-ver20.sh', import.meta.url), 'utf8');

test('AWS and Cloudflare orchestration keeps private and public identities separate', () => {
  assert.match(source, /--private-endpoint/);
  assert.match(source, /--public-hostname/);
  assert.match(source, /--tunnel-token-file/);
  assert.match(source, /install-controller-aws-ver20\.sh/);
  assert.match(source, /setup-cloudflare-tunnel-v20\.sh/);
  assert.match(source, /verify-cloudflare-tunnel-v20\.sh/);
  assert.match(source, /NLI_CONTROLLER_ORIGIN=https:\/\/%s/);
  assert.doesNotMatch(source, /cat\s+.*token|set\s+-x/);
});
