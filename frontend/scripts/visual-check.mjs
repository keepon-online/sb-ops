import { chromium } from '@playwright/test';

const baseURL = process.env.SB_VISUAL_BASE_URL || 'http://127.0.0.1:18031';
const username = process.env.SB_VISUAL_USERNAME || 'admin';
const password = process.env.SB_VISUAL_PASSWORD;

if (!password) throw new Error('SB_VISUAL_PASSWORD is required');

const viewports = [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 }
];

const browser = await chromium.launch({ headless: true });
try {
  for (const viewport of viewports) {
    const context = await browser.newContext({ viewport });
    const page = await context.newPage();
    const consoleErrors = [];
    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors.push(message.text());
    });

    await page.goto(`${baseURL}/login`, { waitUntil: 'domcontentloaded' });
    await page.getByLabel('用户名').fill(username);
    await page.getByLabel('密码').fill(password);
    await page.getByRole('button', { name: '登录' }).click();
    await page.waitForURL('**/overview');
    await page.goto(`${baseURL}/users`, { waitUntil: 'domcontentloaded' });
    await page.getByRole('heading', { name: '用户订阅' }).waitFor();
    if (viewport.width < 992) {
      await page.getByRole('button', { name: '打开导航' }).click();
      const userMenuItem = page.getByRole('menuitem', { name: '用户订阅' });
      await userMenuItem.waitFor();
      await userMenuItem.click();
      await userMenuItem.waitFor({ state: 'hidden' });
    }

    const layout = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      contentText: document.querySelector('.content')?.textContent?.trim().length || 0,
      overflowingButtons: Array.from(document.querySelectorAll('button'))
        .filter((element) => element.scrollWidth > element.clientWidth + 1)
        .map((element) => element.getAttribute('aria-label') || element.textContent?.trim() || 'button')
    }));

    if (layout.documentWidth > layout.viewportWidth) {
      throw new Error(`${viewport.width}px viewport has horizontal overflow: ${layout.documentWidth}px`);
    }
    if (layout.contentText === 0) throw new Error(`${viewport.width}px viewport has blank content`);
    if (layout.overflowingButtons.length > 0) {
      throw new Error(`${viewport.width}px viewport has overflowing buttons: ${layout.overflowingButtons.join(', ')}`);
    }
    if (consoleErrors.length > 0) throw new Error(`${viewport.width}px console errors: ${consoleErrors.join(' | ')}`);

    await page.screenshot({ path: `/tmp/sb-users-${viewport.width}x${viewport.height}.png`, fullPage: true });
    await context.close();
  }
} finally {
  await browser.close();
}
