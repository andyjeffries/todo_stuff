// Capture the README screenshots against the seeded instance started by
// scripts/screenshots.sh. Convention (matching the prior screenshots):
//   - Desktop:  1440×900 viewport @ 2× DSR  →  2880×1800 PNG
//   - Mobile:   iPhone 11 Pro device preset →  1125×2436 PNG, full-page
//
// All inputs come from env vars that the bash wrapper sets, so this file is
// the only place that knows about Playwright APIs.

import { chromium, devices } from 'playwright';
import { mkdirSync } from 'node:fs';

const BASE = process.env.TODOSTUFF_BASE      ?? 'http://localhost:18636';
const EMAIL = process.env.TODOSTUFF_EMAIL    ?? 'demo@todostuff.local';
const PASS = process.env.TODOSTUFF_PASSWORD  ?? 'demoseed12345';
const PROJ_READ = process.env.TODOSTUFF_PROJ_READ ?? '33333333-3333-3333-3333-333333333333';
const OUT = process.env.TODOSTUFF_OUT        ?? './docs/screenshots';

mkdirSync(OUT, { recursive: true });

async function login(context) {
  const page = await context.newPage();
  await page.goto(`${BASE}/login`);
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASS);
  await Promise.all([
    page.waitForURL(/\/today/),
    page.click('button[type="submit"]'),
  ]);
  await page.close();
}

async function shoot(page, file, opts = {}) {
  // Brief settle so HTMX swaps and CSS transitions finish before capture.
  await page.waitForTimeout(250);
  await page.screenshot({ path: `${OUT}/${file}`, fullPage: false, ...opts });
  console.log(` → ${file}`);
}

const browser = await chromium.launch();

// ---------- Desktop ----------
const desktop = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
});
await login(desktop);
const dp = await desktop.newPage();

await dp.goto(`${BASE}/today`, { waitUntil: 'networkidle' });
await shoot(dp, 'today.png');

// detail-panel: click the reading task to open the slide-over
await dp.click('text=Finish chapter 4 of Designing Data-Intensive Applications');
await dp.waitForSelector('#detail-panel[aria-hidden="false"]');
await dp.waitForTimeout(400);
await shoot(dp, 'detail-panel.png');
await dp.click('[data-detail-close]');
await dp.waitForTimeout(400);

await dp.goto(`${BASE}/projects/${PROJ_READ}`, { waitUntil: 'networkidle' });
await shoot(dp, 'project.png');

await dp.goto(`${BASE}/upcoming`, { waitUntil: 'networkidle' });
await shoot(dp, 'upcoming.png');

await dp.goto(`${BASE}/logbook`, { waitUntil: 'networkidle' });
await shoot(dp, 'logbook.png');

await dp.goto(`${BASE}/profile`, { waitUntil: 'networkidle' });
await shoot(dp, 'profile.png');

// quick-add: open the modal from /today
await dp.goto(`${BASE}/today`, { waitUntil: 'networkidle' });
await dp.click('button[data-quick-add]');
await dp.waitForSelector('#quick-add-input:focus');
await dp.fill('#quick-add-input', 'Book the Edinburgh trip');
await shoot(dp, 'quick-add.png');

await desktop.close();

// ---------- Mobile ----------
// Full-page so the screenshot includes the whole scroll region (matches the
// prior 1125×2436 dimensions in docs/screenshots/).
const mobile = await browser.newContext({ ...devices['iPhone 11 Pro'] });
await login(mobile);
const mp = await mobile.newPage();

await mp.goto(`${BASE}/today`, { waitUntil: 'networkidle' });
await shoot(mp, 'mobile-today.png', { fullPage: true });

await mp.click('button[data-sidebar-open]');
await mp.waitForTimeout(300);
await shoot(mp, 'mobile-sidebar.png', { fullPage: true });

await mobile.close();
await browser.close();
console.log('done');
