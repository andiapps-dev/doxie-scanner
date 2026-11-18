import { test, expect } from '@playwright/test';

// The README's still screenshots, desktop and phone width, taken from the
// same seeded app as the demo recording (see record-demo.sh, which runs this
// before the demo so the scan is still in its freshly processed state).
// Written to $SCREENSHOT_DIR (default ../docs/screenshots).

const DIR = process.env.SCREENSHOT_DIR || '../docs/screenshots';
const SCAN_NAME = 'Scan 2026-07-21 10:42';

// No scanner is attached, so show the connected state by changing only that
// part of the real status response (which also carries the feature flags).
async function connectScanner(page) {
  await page.route('**/api/scanner/status', async (route) => {
    const real = await (await route.fetch()).json();
    await route.fulfill({ json: { ...real, connected: true, error: undefined, vid: '2740', pid: '000c', driver: 'doxie-dx400' } });
  });
}

async function imagesLoaded(page) {
  await page.waitForFunction(() =>
    Array.from(document.querySelectorAll('#page-grid img')).every((i) => i.complete && i.naturalWidth > 0),
  );
}

async function shot(page, name) {
  await page.waitForTimeout(400); // let transitions finish
  await page.screenshot({ path: `${DIR}/${name}.png` });
}

test('desktop', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop');
  await connectScanner(page);
  await page.goto('/');
  await expect(page.locator('#scanner-badge')).toHaveText(/scanner connected/i, { timeout: 10_000 });
  await page.locator('#job-list').getByText(SCAN_NAME).click();
  await expect(page.locator('#page-grid .page-thumbnail')).toHaveCount(3);
  await imagesLoaded(page);
  await shot(page, 'scans-desktop');

  // Two pages ticked: the selection bar, with its export menu open.
  await page.locator('#page-grid .page-thumbnail').nth(0).locator('.combine-check').click();
  await page.locator('#page-grid .page-thumbnail').nth(1).locator('.combine-check').click();
  await expect(page.locator('#combine-bar')).toBeVisible();
  await page.locator('#combine-bar .dropdown-toggle').click();
  await expect(page.locator('#combine-bar .export-scope')).toBeVisible();
  await shot(page, 'selection-desktop');
});

test('phone', async ({ page }, info) => {
  test.skip(info.project.name !== 'phone');
  await connectScanner(page);
  await page.goto('/');
  await expect(page.locator('#scanner-badge')).toHaveText(/scanner connected/i, { timeout: 10_000 });
  await expect(page.locator('#job-list').getByText(SCAN_NAME)).toBeVisible();
  await shot(page, 'scans-phone');

  await page.locator('#job-list').getByText(SCAN_NAME).tap();
  await expect(page.locator('#page-grid .page-thumbnail')).toHaveCount(3);
  await imagesLoaded(page);
  await shot(page, 'pages-phone');

  // The same view in dark mode (then back to light for the rest).
  await page.evaluate(() => window.DoxieTheme.setMode('dark'));
  await shot(page, 'dark-phone');
  await page.evaluate(() => window.DoxieTheme.setMode('light'));

  await page.locator('#page-grid .page-thumbnail').nth(0).locator('.combine-check').tap();
  await page.locator('#page-grid .page-thumbnail').nth(1).locator('.combine-check').tap();
  await expect(page.locator('#combine-bar')).toBeVisible();
  await page.locator('#combine-bar .dropdown-toggle').tap();
  await expect(page.locator('#combine-bar .export-scope')).toBeVisible();
  await shot(page, 'selection-phone');
});
