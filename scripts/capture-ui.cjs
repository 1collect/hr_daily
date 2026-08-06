const { chromium } = require('playwright');
const fs = require('fs');

const baseURL = process.env.CAPTURE_URL || 'http://127.0.0.1:6770';
const output = process.env.CAPTURE_OUTPUT || '/screenshots';
const prefix = process.env.CAPTURE_PREFIX || 'ui';
const username = process.env.CAPTURE_USERNAME;
const password = process.env.CAPTURE_PASSWORD;
const width = Number(process.env.CAPTURE_WIDTH || 1920);
const height = Number(process.env.CAPTURE_HEIGHT || 1080);

if (!username || !password) {
  throw new Error('CAPTURE_USERNAME and CAPTURE_PASSWORD are required');
}

(async () => {
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1 });
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.screenshot({ path: `${output}/${prefix}-login.png` });
  await page.locator('#login-name').fill(username);
  await page.locator('#login-password').fill(password);
  await page.locator('#login-form button[type="submit"]').click();
  await page.locator('.sheet-wrap').waitFor({ state: 'visible' });
  await page.screenshot({ path: `${output}/${prefix}-report.png` });

  const readonlyCell = page.locator('.sheet tbody input:disabled').first();
  if (await readonlyCell.count()) {
    const cursor = await readonlyCell.evaluate(el => getComputedStyle(el).cursor);
    if (cursor !== 'pointer') throw new Error(`Expected pointer cursor for manager cell, got ${cursor}`);
    await readonlyCell.hover();
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${output}/${prefix}-tooltip.png` });
  }

  await page.locator('.sheet-wrap').evaluate(el => { el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight - 130); });
  await page.waitForTimeout(180);
  await page.screenshot({ path: `${output}/${prefix}-report-scroll.png` });

  const hiredCell = page.locator('.summary-hires').filter({ hasText: /[1-9]/ }).first();
  if (await hiredCell.count()) {
    await hiredCell.click();
    await page.locator('.summary-modal').waitFor({ state: 'visible' });
    await page.waitForTimeout(220);
    await page.screenshot({ path: `${output}/${prefix}-hired-summary.png` });
    await page.locator('.close-info').click();
  }

  const pages = [
    ['Выгрузка', 'export'],
    ['Пользователи', 'users'],
    ['Представительства', 'offices'],
    ['Настройки', 'settings'],
  ];
  for (const [title, file] of pages) {
    const item = page.locator(`.nav-item[title="${title}"]`);
    if (await item.count()) {
      await item.click();
      await page.mouse.move(width - 30, height - 30);
      await page.waitForTimeout(250);
      await page.screenshot({ path: `${output}/${prefix}-${file}.png` });
      if (file === 'users') {
        await page.locator('#new-user').click();
        await page.locator('.user-form').waitFor({ state: 'visible' });
        await page.waitForTimeout(220);
        await page.screenshot({ path: `${output}/${prefix}-user-modal.png` });
        await page.locator('.modal .close').click();
      }
      if (file === 'offices') {
        await page.locator('#new-office').click();
        await page.locator('.office-form').waitFor({ state: 'visible' });
        await page.waitForTimeout(220);
        await page.screenshot({ path: `${output}/${prefix}-office-modal.png` });
        await page.locator('.modal .close').click();
      }
    }
  }
  await browser.close();
})();
