import { copyFile } from 'node:fs/promises';
import { resolve } from 'node:path';
const root = resolve(import.meta.dirname, '..');
const output = resolve(root, 'build/vite-web');
for (const name of ['nexora-logo-v20.png', 'login-banner-v20.png', 'nexora-poster-v20.png']) {
  await copyFile(resolve(root, 'web', name), resolve(output, name));
}
