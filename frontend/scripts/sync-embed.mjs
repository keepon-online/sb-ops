import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const currentDir = dirname(fileURLToPath(import.meta.url));
const frontendDir = resolve(currentDir, '..');
const distDir = resolve(frontendDir, 'dist');
// Backend lives at the repo root (internal/web/embedded), not under backend/.
const embedDir = resolve(frontendDir, '..', 'internal', 'web', 'embedded');

mkdirSync(embedDir, { recursive: true });
rmSync(resolve(embedDir, 'assets'), { recursive: true, force: true });

if (existsSync(distDir)) {
  for (const item of readdirSync(distDir)) {
    const src = resolve(distDir, item);
    const dest = resolve(embedDir, item);
    if (item === 'assets') {
      cpSync(src, dest, { recursive: true });
    } else {
      cpSync(src, dest);
    }
  }
}
