import { readFileSync, writeFileSync, mkdirSync, unlinkSync, existsSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execSync } from 'node:child_process';
import { chromium } from '@playwright/test';

const currentDir = dirname(fileURLToPath(import.meta.url));
const publicDir = resolve(currentDir, '..', 'public');
mkdirSync(publicDir, { recursive: true });

const svgPath = resolve(publicDir, 'favicon.svg');
const masterPng = resolve(publicDir, 'icon-512.png');

async function generate() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 512, height: 512 } });
  await page.goto('file://' + svgPath);
  await page.screenshot({ path: masterPng, omitBackground: true });
  await browser.close();

  const pyCode = [
    'from PIL import Image',
    'import os',
    `p = r"${publicDir}"`,
    `m = Image.open(r"${masterPng}").convert("RGBA")`,
    'm.resize((180, 180), Image.Resampling.LANCZOS).save(os.path.join(p, "apple-touch-icon.png"))',
    'm.resize((32, 32), Image.Resampling.LANCZOS).save(os.path.join(p, "favicon-32x32.png"))',
    'm.resize((16, 16), Image.Resampling.LANCZOS).save(os.path.join(p, "favicon-16x16.png"))',
    'm.save(os.path.join(p, "favicon.ico"), format="ICO", sizes=[(16,16),(32,32),(48,48),(64,64),(128,128),(256,256)])',
    `if os.path.exists(r"${masterPng}"): os.remove(r"${masterPng}")`,
    'print("Successfully generated favicon assets")'
  ].join('\n');

  execSync(`python3 -c '${pyCode}'`, { stdio: 'inherit' });
}

generate().catch((err) => {
  console.error(err);
  process.exit(1);
});
