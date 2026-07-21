import { test, expect } from '@playwright/test';

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

// Presses down on locator (a Cropper.js resize handle) and drags it to an
// absolute page position. Unlike app.js's own page-thumbnail drag-and-drop,
// Cropper.js resizes its crop box via plain mousedown/mousemove/mouseup —
// no native HTML5 draggable involved — so a real press-move-release
// genuinely resizes it, the same as an actual user dragging the handle.
async function dragHandle(page, locator, targetX, targetY) {
  const box = await locator.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 15 });
  await page.waitForTimeout(150);
  await page.mouse.down();
  await page.mouse.move(targetX, targetY, { steps: 20 });
  await page.waitForTimeout(150);
  await page.mouse.up();
}

// app.js implements its own drag-and-drop (dragstart/dragover/drop
// listeners doing live DOM reordering) rather than relying on any
// library, and reads only clientX/clientY plus dataTransfer.effectAllowed
// off the event — nothing from the transferred data itself. Playwright's
// high-level locator.dragTo() sends real synthetic mouse input and
// expects the browser's native HTML5 drag machinery to translate that
// into dragover events on each element the cursor passes over; in
// headless Chromium that translation isn't reliable (observed: dragstart
// and drop both fire, but the intermediate dragover that actually moves
// the tile in the DOM does not), so the reorder silently no-ops instead
// of failing loudly. The actual reorder is driven by dispatching those
// three events directly, which is what app.js's listeners consume; a
// real page.mouse press-move-release is layered on top purely so the
// cursor dot visibly drags across the screen in the recording.
async function dragReorder(page, source, target, { before = true } = {}) {
  const srcBox = await source.boundingBox();
  const tgtBox = await target.boundingBox();
  const srcX = srcBox.x + srcBox.width / 2;
  const srcY = srcBox.y + srcBox.height / 2;
  const tgtX = before ? tgtBox.x + 10 : tgtBox.x + tgtBox.width - 10;
  const tgtY = tgtBox.y + tgtBox.height / 2;

  await page.mouse.move(srcX, srcY, { steps: 15 });
  await page.waitForTimeout(150);
  await page.mouse.down();
  await page.mouse.move(tgtX, tgtY, { steps: 20 });
  await page.waitForTimeout(150);

  const sourceHandle = await source.elementHandle();
  const targetHandle = await target.elementHandle();
  await page.evaluate(
    ([src, tgt, x, y]) => {
      const dt = new DataTransfer();
      const fire = (el, type) => el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt, clientX: x, clientY: y }));
      fire(src, 'dragstart');
      fire(tgt, 'dragover');
      fire(tgt, 'drop');
      fire(src, 'dragend');
    },
    [sourceHandle, targetHandle, tgtX, tgtY],
  );

  await page.mouse.up();
}

// Every action in this spec resolves near-instantly in a headless
// browser, with no natural travel time or hesitation to pad the
// recording out, so back-to-back actions would otherwise produce only a
// frame or two of each state before cutting to the next. `beat` is a
// deliberate pause after each visually meaningful state change, giving a
// viewer (and the recording) time to actually register what happened.
async function beat(page, ms = 800) {
  await page.waitForTimeout(ms);
}

// Split into several small, numbered tests rather than one long one, so
// each gets its own Playwright-recorded video — record-demo.sh stitches
// a title card in front of each clip (matching the numbered names below,
// see CAPTIONS in record-demo.sh) instead of one undifferentiated
// recording. Playwright truncates long test titles into a hashed
// test-results/ folder name, so record-demo.sh doesn't try to sort those
// folders back into order — it reads results.json's declaration order
// instead, which is why keeping these tests (and CAPTIONS) in sync by
// position, not by name-matching, is what actually keeps titles and
// clips paired correctly.
//
// test.describe.serial keeps them running in this exact order, in one
// worker, sharing the same running container: server-side state
// (renamed jobs, rotated/cropped/reordered pages) persists from one test
// to the next, but each test gets a fresh page/context, so any purely
// client-side state (the combine selection) does not — later tests that
// need a combine selection build it themselves rather than relying on an
// earlier test's clicks. If an earlier test fails, `serial` skips the
// rest instead of cascading into confusing unrelated failures.
test.describe.serial('doxie-scanner UI walkthrough', () => {
  test('1 - live scanner status and scan list', async ({ page }) => {
    await page.goto('/');
    await installCursor(page);

    // No physical scanner in this environment, so the status badge
    // should settle on "not connected" and the start-scan button should
    // stay disabled — this is real, not simulated, behavior.
    const badge = page.locator('#scanner-badge');
    await expect(badge).toHaveText(/not connected/i, { timeout: 10_000 });
    await expect(page.locator('#start-scan-btn')).toBeDisabled();
    await pointAt(page, badge);
    await beat(page, 1200);

    // Seed data: two scans should already be listed.
    const jobList = page.locator('#job-list');
    await expect(jobList.getByText('Q3 Invoice — Rivertown Supply')).toBeVisible();
    await expect(jobList.getByText('Cover Letter Draft')).toBeVisible();
    await beat(page, 1200);
  });

  test('2 - switch the UI language', async ({ page }) => {
    await page.goto('/');
    await installCursor(page);

    const switcher = page.locator('#lang-switcher');
    await pointAt(page, switcher);
    await beat(page, 800);

    await expect(page.locator('#start-scan-btn')).toHaveText('Start scan');

    // The switcher reloads the page rather than patching the DOM in
    // place, so every dynamic render (job list, help text, etc.) picks
    // up the new language too, not just the static markup.
    await Promise.all([page.waitForEvent('load'), switcher.selectOption('es')]);
    await installCursor(page);
    await expect(page.locator('#start-scan-btn')).toHaveText('Iniciar escaneo');
    await beat(page, 1200);

    // Confirm a dynamically-rendered string (not just static markup)
    // picks up the new language too.
    await clickAt(page, page.locator('#job-list').getByText('Cover Letter Draft'));
    await expect(page.locator('#job-detail p.text-muted')).toContainText('escaneos dúplex');
    await beat(page, 1200);

    await Promise.all([page.waitForEvent('load'), switcher.selectOption('en')]);
    await installCursor(page);
    await expect(page.locator('#start-scan-btn')).toHaveText('Start scan');
    await beat(page, 800);
  });

  test('3 - start and run a scan', async ({ page }) => {
    // This container has no physical scanner attached, so the badge
    // would otherwise stay red for the whole recording and "Start scan"
    // would stay disabled forever — there'd be nothing to show. Instead,
    // fake a connected scanner and a completed scan entirely at the
    // network layer (page.route intercepts), so the recording can also
    // show what using the app actually looks like. Nothing server-side
    // or in the real driver is touched; the two real seed jobs are
    // untouched too, since this fake job only ever exists in this test's
    // mocked responses.
    const FAKE_JOB_ID = 'demo-live-scan';
    const FAKE_JOB_NAME = 'Scan (demo)';
    const FAKE_PAGE_COUNT = 2;

    // Reuse the real seed invoice's two actual pages' image bytes for the
    // fake scan's result, so it looks like a real 2-page document rather
    // than a blank/broken image — and so the progress count (which counts
    // up to FAKE_PAGE_COUNT below) matches what the completed job
    // actually contains.
    const pageBytes = [];
    for (let i = 1; i <= FAKE_PAGE_COUNT; i++) {
      pageBytes.push(await (await page.request.get(`/api/scans/demo-invoice/pages/${i}`)).body());
    }
    const realJobs = await (await page.request.get('/api/scans')).json();

    await page.route('**/api/scanner/status', (route) =>
      route.fulfill({ json: { connected: true, vid: '2740', pid: '000c', driver: 'doxie-dx400' } }),
    );

    await page.route('**/api/scans', async (route) => {
      if (route.request().method() === 'POST') {
        await route.fulfill({ status: 202, json: { jobId: FAKE_JOB_ID, status: 'running' } });
      } else {
        const fakeSummary = {
          id: FAKE_JOB_ID,
          name: FAKE_JOB_NAME,
          createdAt: new Date().toISOString(),
          status: 'completed',
          pageCount: FAKE_PAGE_COUNT,
          duplex: false,
        };
        await route.fulfill({ json: [fakeSummary, ...realJobs] });
      }
    });

    // The frontend polls GET /api/scans/{id} once a second while a scan
    // is running; report "running" with an increasing pagesScanned count
    // so the UI ticks up 1, 2, ..., then "completed" with exactly
    // FAKE_PAGE_COUNT pages — matching what was just "scanned", not a
    // different, smaller number.
    let pollCount = 0;
    await page.route(`**/api/scans/${FAKE_JOB_ID}`, async (route) => {
      pollCount++;
      if (pollCount <= FAKE_PAGE_COUNT) {
        await route.fulfill({
          json: {
            id: FAKE_JOB_ID,
            name: FAKE_JOB_NAME,
            driver: 'doxie-dx400',
            createdAt: new Date().toISOString(),
            status: 'running',
            duplex: false,
            dpi: 300,
            pageCount: 0,
            pages: [],
            pagesScanned: pollCount,
          },
        });
      } else {
        await route.fulfill({
          json: {
            id: FAKE_JOB_ID,
            name: FAKE_JOB_NAME,
            driver: 'doxie-dx400',
            createdAt: new Date().toISOString(),
            completedAt: new Date().toISOString(),
            status: 'completed',
            duplex: false,
            dpi: 300,
            pageCount: FAKE_PAGE_COUNT,
            pages: Array.from({ length: FAKE_PAGE_COUNT }, (_, i) => ({
              index: i + 1,
              file: `page-${String(i + 1).padStart(3, '0')}.png`,
              widthPx: 850,
              heightPx: 1100,
            })),
          },
        });
      }
    });

    for (let i = 1; i <= FAKE_PAGE_COUNT; i++) {
      await page.route(`**/api/scans/${FAKE_JOB_ID}/pages/${i}*`, (route) =>
        route.fulfill({ contentType: 'image/png', body: pageBytes[i - 1] }),
      );
    }

    await page.goto('/');
    await installCursor(page);

    const badge = page.locator('#scanner-badge');
    await expect(badge).toHaveText(/scanner connected/i, { timeout: 5_000 });
    await expect(page.locator('#start-scan-btn')).toBeEnabled();
    await pointAt(page, badge);
    await beat(page, 1200);

    await clickAt(page, page.locator('#start-scan-btn'));
    await expect(page.locator('#scan-progress')).toBeVisible();
    await beat(page, 1800);

    await expect(page.locator('#scan-progress')).toBeHidden({ timeout: 10_000 });
    const jobList = page.locator('#job-list');
    await expect(jobList.getByText(FAKE_JOB_NAME)).toBeVisible();
    await expect(page.locator('#page-grid').locator('.page-thumbnail')).toHaveCount(FAKE_PAGE_COUNT);
    await beat(page, 1200);
  });

  test('4 - browse, rotate, and crop a page', async ({ page }) => {
    await page.goto('/');
    await installCursor(page);
    const jobList = page.locator('#job-list');
    const grid = page.locator('#page-grid');

    await clickAt(page, jobList.getByText('Q3 Invoice — Rivertown Supply'));
    await expect(grid.locator('.page-thumbnail')).toHaveCount(2);
    await beat(page);

    // Reorder: drag page 2 in front of page 1, then persist via the
    // PATCH /pages/order call the UI fires on drop.
    const [reorderResp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/pages/order') && r.request().method() === 'PATCH'),
      dragReorder(page, grid.locator('.page-thumbnail[data-page="2"]'), grid.locator('.page-thumbnail[data-page="1"]')),
    ]);
    expect(reorderResp.ok()).toBeTruthy();
    await expect(grid.locator('.page-thumbnail').first()).toHaveAttribute('data-page', '2');
    await beat(page, 1000);

    // Open the (now first) tile, rotate it, then crop it.
    await clickAt(page, grid.locator('.page-thumbnail').first().locator('.page-thumb'));
    const modal = page.locator('#page-modal');
    await expect(modal).toBeVisible();
    await expect(page.locator('#page-modal-image')).toBeVisible();
    await beat(page);

    await clickAt(page, page.locator('#pm-rotate-right'));
    await expect(page.locator('#busy-overlay')).toBeHidden();
    await beat(page, 1000);

    await clickAt(page, page.locator('#pm-crop-start'));
    await expect(page.locator('#page-modal-crop-actions')).toBeVisible();
    await beat(page, 800); // let the crop handles render

    // Cropper.js starts with autoCropArea: 1 — a crop box already
    // covering the whole image — so saving immediately would be a no-op
    // crop. Drag the bottom-right handle inward (keeping the top-left
    // corner fixed) to an actual smaller region first, so this
    // demonstrates and exercises a real crop.
    const cropBoxBox = await page.locator('.cropper-crop-box').boundingBox();
    await dragHandle(
      page,
      page.locator('.cropper-point.point-se'),
      cropBoxBox.x + cropBoxBox.width * 0.6,
      cropBoxBox.y + cropBoxBox.height * 0.6,
    );
    await beat(page, 800);

    await clickAt(page, page.locator('#pm-crop-save'));
    await expect(page.locator('#page-modal-view-actions')).toBeVisible();
    await beat(page, 1000);

    // Two JPEG export options now — high quality vs. smaller — so a
    // compact single image doesn't require wrapping it in a PDF.
    await clickAt(page, page.locator('#page-modal-view-actions .dropdown-toggle'));
    await beat(page, 500);
    const [exportDownload] = await Promise.all([
      page.context().waitForEvent('download'),
      clickAt(page, page.locator('.pm-export-link[data-quality="90"]')),
    ]);
    expect(exportDownload.suggestedFilename()).toMatch(/\.jpg$/);
    await beat(page, 800);

    await clickAt(page, page.locator('#page-modal .btn-close'));
    await expect(modal).toBeHidden();
  });

  test('5 - extract text from a page', async ({ page }) => {
    // Clicking "Copy text" for real triggers navigator.clipboard's
    // permission flow with nothing to grant it (no real user, no
    // headless UI to interact with). Chromium quickly rejects the write,
    // but the pending permission arbitration silently swallows the
    // *next* real click afterward — verified by bisection (removing the
    // Copy click alone fixed the following Back click), and this isn't
    // reliably fixable via context.grantPermissions() either: it worked
    // over plain "localhost" but reproduced again over a real LAN IP
    // (matching this container's actual hostname), so it's origin/
    // environment-sensitive in a way not worth chasing further. Stubbing
    // navigator.clipboard sidesteps Chromium's real permission machinery
    // entirely — and shows the actual "Copied!" success path, which is
    // arguably the more representative demo than the no-permission
    // fallback anyway.
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: async () => {} },
        configurable: true,
      });
    });

    await page.goto('/');
    await installCursor(page);
    const jobList = page.locator('#job-list');
    const grid = page.locator('#page-grid');
    const modal = page.locator('#page-modal');

    await clickAt(page, jobList.getByText('Cover Letter Draft'));
    await expect(grid.locator('.page-thumbnail')).toHaveCount(1);
    await beat(page);

    await clickAt(page, grid.locator('.page-thumbnail').first().locator('.page-thumb'));
    await expect(modal).toBeVisible();
    await beat(page);

    await clickAt(page, page.locator('#pm-extract-text'));
    await expect(page.locator('#page-modal-ocr-wrap')).toBeVisible();
    // Deskew (unpaper) + OCR (tesseract) genuinely takes several real
    // seconds against a real page — wait for the busy overlay to clear
    // rather than a fixed pause.
    await expect(page.locator('#busy-overlay')).toBeHidden({ timeout: 20_000 });
    await expect(page.locator('#pm-ocr-text')).not.toHaveValue('');
    await beat(page, 1800);

    await clickAt(page, page.locator('#pm-ocr-copy'));
    await beat(page, 1200);

    await clickAt(page, page.locator('#pm-ocr-back'));
    await expect(page.locator('#page-modal-image-wrap')).toBeVisible();
    await beat(page, 800);

    await clickAt(page, page.locator('#page-modal .btn-close'));
    await expect(modal).toBeHidden();
  });

  test('6 - combine pages from multiple scans into one pdf', async ({ page }) => {
    await page.goto('/');
    await installCursor(page);
    const jobList = page.locator('#job-list');
    const grid = page.locator('#page-grid');
    const modal = page.locator('#page-modal');
    const combineBar = page.locator('#combine-bar');
    const thumbs = page.locator('.combine-thumb');

    // Select the invoice's first tile (page 2, after test 2's reorder)
    // and the letter's only page — both originally "page 1" in their own
    // scan, which is exactly why the combine bar labels by position
    // instead of original page number.
    await clickAt(page, jobList.getByText('Q3 Invoice — Rivertown Supply'));
    await expect(grid.locator('.page-thumbnail')).toHaveCount(2);
    await clickAt(page, grid.locator('.page-thumbnail[data-page="2"] .combine-check'));
    await beat(page);

    await clickAt(page, jobList.getByText('Cover Letter Draft'));
    await expect(grid.locator('.page-thumbnail')).toHaveCount(1);
    await clickAt(page, grid.locator('.page-thumbnail .combine-check'));

    await expect(combineBar).toBeVisible();
    await expect(page.locator('#combine-count-text')).toContainText('2');
    await expect(thumbs).toHaveCount(2);
    await beat(page, 1000);

    // Reorder by dragging the second thumbnail (the letter page) in
    // front of the first (the invoice page) — confirm the actual image
    // swapped position, not just that the sequential 1/2 labels persist.
    const secondThumbSrc = await thumbs.nth(1).locator('img').getAttribute('src');
    await dragReorder(page, thumbs.nth(1), thumbs.nth(0));
    await expect(thumbs.first().locator('img')).toHaveAttribute('src', secondThumbSrc);
    await beat(page, 1000);

    // Click a thumbnail for a view-only preview — no editing actions shown.
    await clickAt(page, thumbs.first().locator('img'));
    await expect(modal).toBeVisible();
    await expect(page.locator('#page-modal-view-actions')).toBeHidden();
    await beat(page, 1200);
    await clickAt(page, page.locator('#page-modal .btn-close'));
    await expect(modal).toBeHidden();

    // Remove the invoice page from the selection via its thumbnail's X
    // button (it's now second, after the reorder above), then re-select
    // it from the invoice scan's page grid, and combine both into a PDF.
    await clickAt(page, thumbs.nth(1).locator('.combine-thumb-remove'));
    await expect(thumbs).toHaveCount(1);
    await beat(page, 1000);

    await clickAt(page, jobList.getByText('Q3 Invoice — Rivertown Supply'));
    await clickAt(page, grid.locator('.page-thumbnail[data-page="2"] .combine-check'));
    await expect(thumbs).toHaveCount(2);
    await beat(page);

    await clickAt(page, page.locator('#combine-title'));
    await page.locator('#combine-title').fill('Demo Combined');
    await beat(page, 600);

    // Show off the PNG/JPEG choice — PNG (lossless) for photo/art
    // content, JPEG (smaller, default) for the common text-scan case —
    // before combining with the default JPEG selection still in effect.
    await pointAt(page, page.locator('#combine-format'));
    await beat(page, 600);
    await page.locator('#combine-format').selectOption('png');
    await beat(page, 800);
    await page.locator('#combine-format').selectOption('jpeg');
    await beat(page, 800);

    const [download] = await Promise.all([
      page.waitForEvent('download'),
      clickAt(page, page.locator('#combine-btn')),
    ]);
    expect(download.suggestedFilename()).toMatch(/\.pdf$/);
    await beat(page, 1000);

    await clickAt(page, page.locator('#combine-clear-btn'));
    await expect(combineBar).toBeHidden();
  });

  test('7 - export a scan as pdf, rename it, and delete a page', async ({ page }) => {
    await page.goto('/');
    await installCursor(page);
    const jobList = page.locator('#job-list');
    const grid = page.locator('#page-grid');
    const modal = page.locator('#page-modal');

    await clickAt(page, jobList.getByText('Q3 Invoice — Rivertown Supply'));
    await expect(grid.locator('.page-thumbnail')).toHaveCount(2);
    await beat(page);

    // Export the whole scan as one PDF — same PNG/JPEG choice as the
    // combine bar, defaulting to JPEG.
    await pointAt(page, page.locator('#job-export-format'));
    await beat(page, 600);
    const [wholeScanDownload] = await Promise.all([
      page.waitForEvent('download'),
      clickAt(page, page.locator('#job-export-pdf-btn')),
    ]);
    expect(wholeScanDownload.suggestedFilename()).toMatch(/\.pdf$/);
    await beat(page, 1000);

    await clickAt(page, page.locator('#job-name-input'));
    await page.locator('#job-name-input').fill('Q3 Invoice (renamed)');
    await beat(page, 600);
    await clickAt(page, page.locator('#job-rename-btn'));
    await expect(jobList.getByText('Q3 Invoice (renamed)')).toBeVisible();
    await beat(page, 1200);

    // Delete one page from it and confirm the page count drops.
    await clickAt(page, grid.locator('.page-thumbnail').first().locator('.page-thumb'));
    await expect(modal).toBeVisible();
    await beat(page, 800);
    page.once('dialog', (d) => d.accept());
    await clickAt(page, page.locator('#pm-delete'));
    await expect(modal).toBeHidden();
    await expect(grid.locator('.page-thumbnail')).toHaveCount(1);
    await beat(page, 1000);
  });
});
