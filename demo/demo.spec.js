import { test, expect, request as pwRequest } from '@playwright/test';

// Headless recordings never show an OS mouse cursor, so a viewer can't
// tell where a click landed. Injects a small red dot that tracks real
// 'mousemove'/'mousedown'/'mouseup' events, high enough z-index to sit
// above Bootstrap's modal/backdrop. Called once per test, right after
// goto('/'), since each test gets a fresh document.
async function installCursor(page) {
  await page.evaluate(() => {
    const cursor = document.createElement('div');
    cursor.id = '__demo_cursor__';
    Object.assign(cursor.style, {
      position: 'fixed',
      top: '-100px',
      left: '-100px',
      width: '18px',
      height: '18px',
      marginLeft: '-9px',
      marginTop: '-9px',
      borderRadius: '50%',
      background: 'rgba(220, 53, 69, 0.55)',
      border: '2px solid rgba(220, 53, 69, 0.95)',
      boxShadow: '0 0 0 2px rgba(255, 255, 255, 0.6)',
      pointerEvents: 'none',
      zIndex: 2147483647,
      transition: 'transform 0.08s ease-out',
      transform: 'scale(1)',
    });
    document.documentElement.appendChild(cursor);
    window.addEventListener(
      'mousemove',
      (e) => {
        cursor.style.left = e.clientX + 'px';
        cursor.style.top = e.clientY + 'px';
      },
      true,
    );
    window.addEventListener('mousedown', () => (cursor.style.transform = 'scale(1.7)'), true);
    window.addEventListener('mouseup', () => (cursor.style.transform = 'scale(1)'), true);
  });
}

// Moves the real mouse to the element (in visible steps, so the injected
// cursor dot travels across the screen instead of teleporting) and
// clicks it — used in place of locator.click() throughout so every
// interaction is visible in the recording, not just its result.
async function clickAt(page, locator) {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y, { steps: 20 });
  await page.waitForTimeout(150);
  await page.mouse.down();
  await page.waitForTimeout(90);
  await page.mouse.up();
}

// Same idea as clickAt, but without a click — for pointing the cursor at
// something purely informational (a status badge) that isn't itself an
// interactive control.
async function pointAt(page, locator) {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 20 });
}

// Drags one page thumbnail in front of another with the real mouse. The grid
// uses Pointer Events, so a genuine press-move-release is all it needs (no
// synthetic drag events), and the cursor dot visibly carries the page across.
async function dragBefore(page, source, target) {
  const s = await source.boundingBox();
  const t = await target.boundingBox();
  await page.mouse.move(s.x + s.width / 2, s.y + s.height / 2, { steps: 15 });
  await page.waitForTimeout(150);
  await page.mouse.down();
  await page.mouse.move(t.x + 14, t.y + t.height / 2, { steps: 30 });
  await page.waitForTimeout(250);
  await page.mouse.up();
}

// Every action in this spec resolves near-instantly in a headless browser,
// with no natural travel time or hesitation to pad the recording out, so
// `beat` is a deliberate pause after each visually meaningful change.
async function beat(page, ms = 600) {
  await page.waitForTimeout(ms);
}

// There is no scanner attached, so every clip shows the connected state by
// changing only that part of the real status response (which also reports
// which features, such as auto-rotate and Paperless, are available).
async function connectScanner(page) {
  await page.route('**/api/scanner/status', async (route) => {
    const real = await (await route.fetch()).json();
    await route.fulfill({ json: { ...real, connected: true, error: undefined, vid: '2740', pid: '000c', driver: 'doxie-dx400' } });
  });
}

const SCAN_ID = 'demo-receipts'; // the seeded raw scan the first test "scans"
const SCAN_NAME = 'Scan 2026-07-21 10:42';

// The story the README's demo.gif tells, one numbered test per clip (see
// CAPTIONS in record-demo.sh, which pairs them by position, not by name):
//   1 scan with auto-crop and auto-rotate   2 the pages come back trimmed
//   3 drag pages into order                 4 export as a PDF or as images
//   5 send to Paperless-ngx
// It is also an integration test: every step asserts what it shows.
//
// There is no scanner attached, so test 1 fakes only the scanner's side at
// the network layer: the connected badge and a few polls of progress. The
// pages it then "delivers" are real: record-demo.sh runs the app's own
// auto-crop and auto-rotate (the same endpoints a real scan ends with) over
// the raw seeded pages, so the trimming, straightening and turning shown
// afterwards really happened. Paperless is a small stand-in server (fake-paperless.py)
// that the app talks to for real.
//
// Serial and in one worker: server-side state (the page order) carries from
// one test to the next; client-side state (the selection) does not, so each
// test that needs a selection makes its own.
test.describe.serial('doxie-scanner demo', () => {
  // record-demo.sh has already run the app's own auto-crop and auto-rotate
  // over the raw seeded pages; check that it really happened.
  test.beforeAll(async ({ baseURL }) => {
    const api = await pwRequest.newContext({ baseURL });
    const job = await (await api.get(`/api/scans/${SCAN_ID}`)).json();
    expect(job.pages.filter((p) => p.autoCrop).length).toBeGreaterThanOrEqual(2);
    expect(job.pages.filter((p) => p.autoRotated).length).toBeGreaterThanOrEqual(1);
    await api.dispose();
  });

  test('1 - scan with auto-crop and auto-rotate', async ({ page }) => {
    await connectScanner(page);

    // Hide the seeded scan from the list until the pretend scan "finishes".
    let scanDone = false;
    await page.route('**/api/scans', async (route) => {
      if (route.request().method() === 'POST') {
        await route.fulfill({ status: 202, json: { jobId: SCAN_ID, status: 'running' } });
        return;
      }
      const jobs = await (await route.fetch()).json();
      await route.fulfill({ json: scanDone ? jobs : jobs.filter((j) => j.id !== SCAN_ID) });
    });

    // The page polls every half second; the stages that matter most are held
    // a little longer so they can be read. Once the steps run out, the real job is served.
    const steps = [
      [{ stage: 'checking' }, 1],
      [{ stage: 'scanning', stageDone: 1, stageTotal: 3, pagesScanned: 0 }, 1],
      [{ stage: 'scanning', stageDone: 2, stageTotal: 3, pagesScanned: 1 }, 1],
      [{ stage: 'scanning', stageDone: 3, stageTotal: 3, pagesScanned: 2 }, 1],
      [{ stage: 'saving', pagesScanned: 3 }, 1],
      [{ stage: 'cropping', stageDone: 1, stageTotal: 3, pagesScanned: 3 }, 2],
      [{ stage: 'cropping', stageDone: 2, stageTotal: 3, pagesScanned: 3 }, 1],
      [{ stage: 'cropping', stageDone: 3, stageTotal: 3, pagesScanned: 3 }, 1],
      [{ stage: 'rotating', stageDone: 1, stageTotal: 3, pagesScanned: 3 }, 1],
      [{ stage: 'rotating', stageDone: 2, stageTotal: 3, pagesScanned: 3 }, 1],
      [{ stage: 'rotating', stageDone: 3, stageTotal: 3, pagesScanned: 3 }, 2],
    ].flatMap(([s, holds]) => Array(holds).fill(s));
    let poll = 0;
    await page.route(`**/api/scans/${SCAN_ID}`, async (route) => {
      if (poll >= steps.length) {
        scanDone = true;
        await route.fallback();
        return;
      }
      const real = await (await route.fetch()).json();
      await route.fulfill({ json: { ...real, status: 'running', pages: [], pageCount: 0, ...steps[poll++] } });
    });

    await page.goto('/');
    await installCursor(page);
    await expect(page.locator('#scanner-badge')).toHaveText(/scanner connected/i, { timeout: 10_000 });
    await expect(page.locator('#start-scan-btn')).toBeEnabled();
    await expect(page.locator('#autocrop-toggle')).toBeChecked();
    await expect(page.locator('#autorotate-toggle')).toBeChecked();
    await pointAt(page, page.locator('#autocrop-toggle'));
    await beat(page, 700);
    await pointAt(page, page.locator('#autorotate-toggle'));
    await beat(page, 600);

    await clickAt(page, page.locator('#start-scan-btn'));
    await expect(page.locator('#scan-progress')).toBeVisible();
    await expect(page.locator('#scan-progress-text')).toContainText(/Trimming pages/, { timeout: 15_000 });
    await expect(page.locator('#scan-progress-text')).toContainText(/Turning pages upright/, { timeout: 15_000 });
    await expect(page.locator('#scan-progress')).toBeHidden({ timeout: 20_000 });
    await expect(page.locator('#job-list').getByText(SCAN_NAME)).toBeVisible();
    await expect(page.locator('#page-grid .page-thumbnail')).toHaveCount(3);
    await beat(page, 1000);
  });

  test('2 - pages come back trimmed and upright', async ({ page }) => {
    await connectScanner(page);
    await page.goto('/');
    await installCursor(page);
    await clickAt(page, page.locator('#job-list').getByText(SCAN_NAME));
    const grid = page.locator('#page-grid');
    await expect(grid.locator('.page-thumbnail')).toHaveCount(3);
    await expect(grid.getByText(/auto-cropped/i).first()).toBeVisible();
    await expect(grid.getByText(/auto-rotated/i).first()).toBeVisible();
    await beat(page, 1000);

    // Open the first page and flick through the scan with the page switcher.
    await clickAt(page, grid.locator('.page-thumbnail').first().locator('.page-thumb'));
    const modal = page.locator('#page-modal');
    await expect(modal).toBeVisible();
    await expect(page.locator('#page-modal-image')).toBeVisible();
    await beat(page, 1000);
    await clickAt(page, page.locator('#pm-next'));
    await expect(page.locator('#page-modal-title')).toContainText('2');
    await beat(page, 1000);
    await clickAt(page, page.locator('#pm-next'));
    await expect(page.locator('#page-modal-title')).toContainText('3');
    await beat(page, 1100);
    await clickAt(page, page.locator('#page-modal .btn-close'));
    await expect(modal).toBeHidden();
  });

  test('3 - drag pages into order', async ({ page }) => {
    await connectScanner(page);
    await page.goto('/');
    await installCursor(page);
    await clickAt(page, page.locator('#job-list').getByText(SCAN_NAME));
    const grid = page.locator('#page-grid');
    await expect(grid.locator('.page-thumbnail')).toHaveCount(3);
    await beat(page, 600);

    // Pull the third page (the statement) to the front.
    const [reorder] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/pages/order') && r.request().method() === 'PATCH'),
      dragBefore(page, grid.locator('.page-thumbnail[data-page="3"]'), grid.locator('.page-thumbnail[data-page="1"]')),
    ]);
    expect(reorder.ok()).toBeTruthy();
    await expect(grid.locator('.page-thumbnail').first()).toHaveAttribute('data-page', '3');
    // The labels follow the new order.
    await expect(grid.locator('.page-thumbnail').first()).toContainText('Page 1');
    await beat(page, 1000);
  });

  test('4 - export a pdf or separate images', async ({ page }) => {
    await connectScanner(page);
    await page.goto('/');
    await installCursor(page);
    await clickAt(page, page.locator('#job-list').getByText(SCAN_NAME));
    const grid = page.locator('#page-grid');
    await expect(grid.locator('.page-thumbnail')).toHaveCount(3);

    // Tick the receipt and the business card (now pages 2 and 3).
    await clickAt(page, grid.locator('.page-thumbnail').nth(1).locator('.combine-check'));
    await clickAt(page, grid.locator('.page-thumbnail').nth(2).locator('.combine-check'));
    const bar = page.locator('#combine-bar');
    await expect(bar).toBeVisible();
    await expect(page.locator('#combine-count-text')).toHaveText('2 pages selected');
    await beat(page, 700);

    await clickAt(page, page.locator('#combine-title'));
    await page.keyboard.type('Receipt and card', { delay: 40 });
    await beat(page, 400);

    // The menu says what it will export, and offers a PDF or a ZIP of images.
    await clickAt(page, bar.locator('.dropdown-toggle'));
    await expect(bar.locator('.export-scope')).toHaveText('Exports the 2 selected pages');
    await beat(page, 1000);
    const [zip] = await Promise.all([
      page.context().waitForEvent('download'),
      clickAt(page, bar.locator('.export-opt[data-kind="images"][data-fmt="png"]')),
    ]);
    expect(zip.suggestedFilename()).toBe('Receipt and card.zip');
    await beat(page, 1100);
  });

  test('5 - send to paperless-ngx', async ({ page }) => {
    await connectScanner(page);
    await page.goto('/');
    await installCursor(page);
    await clickAt(page, page.locator('#job-list').getByText(SCAN_NAME));
    const grid = page.locator('#page-grid');
    await expect(grid.locator('.page-thumbnail')).toHaveCount(3);
    await clickAt(page, grid.locator('.page-thumbnail').nth(1).locator('.combine-check'));
    await beat(page, 500);

    await clickAt(page, page.locator('#combine-send-btn'));
    const dialog = page.locator('#send-modal');
    await expect(dialog).toBeVisible();
    await expect(page.locator('#send-tags label').first()).toBeVisible({ timeout: 10_000 });
    await beat(page, 600);

    await clickAt(page, page.locator('#send-title'));
    await page.keyboard.type('Rivertown Supply receipt', { delay: 35 });
    await page.locator('#send-correspondent').selectOption({ label: 'Rivertown Supply' });
    await page.locator('#send-doctype').selectOption({ label: 'Receipt' });
    await beat(page, 400);
    await clickAt(page, page.locator('#send-tags label', { hasText: 'Receipts' }));
    await beat(page, 800);

    await clickAt(page, page.locator('#send-submit'));
    await expect(dialog).toBeHidden({ timeout: 15_000 });
    await expect(page.locator('#job-sent')).toContainText('Rivertown Supply receipt');
    await expect(page.locator('#job-sent')).toContainText('Filed in Paperless', { timeout: 20_000 });
    await beat(page, 1300);
  });
});
