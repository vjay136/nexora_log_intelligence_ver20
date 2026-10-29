import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const setup = readFileSync(new URL('../scripts/setup-cloudflare-tunnel-v20.sh', import.meta.url), 'utf8');
const verify = readFileSync(new URL('../scripts/verify-cloudflare-tunnel-v20.sh', import.meta.url), 'utf8');
const uninstall = readFileSync(new URL('../scripts/uninstall-cloudflare-tunnel-v20.sh', import.meta.url), 'utf8');
const quick = readFileSync(new URL('../scripts/cloudflare-quick-diagnosis-v20.sh', import.meta.url), 'utf8');

test('named setup uses a protected token file and keeps credentials out of the unit', () => {
  assert.match(setup, /https:\/\/127\.0\.0\.1:12382/);
  assert.match(setup, /--token-file/);
  assert.match(setup, /install -o root -g cloudflared -m 0640/);
  assert.match(setup, /https:\/\/pkg\.cloudflare\.com\/cloudflared\.repo/);
  assert.match(setup, /systemctl enable cloudflared\.service/);
  assert.match(setup, /systemctl restart cloudflared\.service/);
  assert.doesNotMatch(setup, /--token\s+\$|TUNNEL_TOKEN=|noTLSVerify/);
});

test('verification checks tunnel service, controller health and outbound edge registration', () => {
  assert.match(verify, /systemctl is-active --quiet cloudflared\.service/);
  assert.match(verify, /systemctl is-active --quiet nli-controller-agent\.service/);
  assert.match(verify, /127\.0\.0\.1:12382\/api\/v1\/health/);
  assert.match(verify, /https:\/\/\$\{hostname\}\/api\/v1\/health/);
  assert.match(verify, /Registered tunnel connection/);
});

test('quick tunnel is temporary and separate from the named production service', () => {
  assert.match(quick, /--url https:\/\/127\.0\.0\.1:12382 --no-tls-verify/);
  assert.match(quick, /HOME="\$scratch"/);
  assert.match(quick, /pkg\.cloudflare\.com\/cloudflared\.repo/);
  assert.doesNotMatch(quick, /systemctl enable|systemctl start/);
  assert.match(uninstall, /cloudflared\.service/);
  assert.match(uninstall, /RPM and Cloudflare dashboard tunnel were left in place/);
});
