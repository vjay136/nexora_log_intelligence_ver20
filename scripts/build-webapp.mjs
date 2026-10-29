import { copyFile, mkdir, readdir, rm } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const output = resolve(root, 'build/web-public');
await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true });
const assets = ['index.html', 'app.js', 'styles.css', 'README.txt', 'nli-mark.svg', 'nexora-logo-v20.png', 'login-banner-v20.png', 'nexora-poster-v20.png', '_headers'];
// Only browser assets are published. Go sources, SQL, samples and config files
// remain outside the public output directory.
for (const name of assets) await copyFile(resolve(root, 'web', name), resolve(output, name));
const unexpected = (await readdir(output)).filter(name => !assets.includes(name));
if (unexpected.length) throw new Error(`Unexpected public output files: ${unexpected.join(', ')}; use a clean build directory.`);
console.log(`NLI V20: built ${assets.length} public assets in build/web-public`);
