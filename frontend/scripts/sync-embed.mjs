import { cpSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const currentDir = dirname(fileURLToPath(import.meta.url));
const frontendDir = resolve(currentDir, '..');
const distDir = resolve(frontendDir, 'dist');
// Backend lives at the repo root (internal/web/embedded), not under backend/.
const embedDir = resolve(frontendDir, '..', 'internal', 'web', 'embedded');

mkdirSync(embedDir, { recursive: true });
rmSync(resolve(embedDir, 'assets'), { recursive: true, force: true });

cpSync(resolve(distDir, 'index.html'), resolve(embedDir, 'index.html'));

if (existsSync(resolve(distDir, 'assets'))) {
  cpSync(resolve(distDir, 'assets'), resolve(embedDir, 'assets'), { recursive: true });
}
