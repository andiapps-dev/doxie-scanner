import { test, expect } from '@playwright/test';

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
// of failing loudly. Dispatching the three events directly targets
// exactly what app.js's listeners consume and is deterministic.
async function dragReorder(page, source, target, { before = true } = {}) {
  const sourceHandle = await source.elementHandle();
  const targetHandle = await target.elementHandle();
  await page.evaluate(
    ([src, tgt, before]) => {
      const dt = new DataTransfer();
      const fire = (el, type, x, y) =>
        el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt, clientX: x, clientY: y }));
      const srcRect = src.getBoundingClientRect();
      fire(src, 'dragstart', srcRect.left + srcRect.width / 2, srcRect.top + srcRect.height / 2);
      const tgtRect = tgt.getBoundingClientRect();
      const x = before ? tgtRect.left + 2 : tgtRect.right - 2;
      const y = tgtRect.top + tgtRect.height / 2;
      fire(tgt, 'dragover', x, y);
      fire(tgt, 'drop', x, y);
      fire(src, 'dragend', x, y);
    },
    [sourceHandle, targetHandle, before],
  );
}

// One long scripted walkthrough rather than many small tests: this file
// doubles as demo-recording material (record-demo.sh turns its video
// into demo.gif) and an integration test against a real running
// container, so it needs to play out as one continuous, presentable
// session rather than parallel/isolated cases. Seed data (two scans,
// three pages total) is written by generate-seed-data.py before the
// container starts — see record-demo.sh.

test('scripted walkthrough of the doxie-scanner UI', async ({ page }) => {
  await page.goto('/');

  // No physical scanner in this environment, so the status badge should
  // settle on "not connected" and the start-scan button should stay
  // disabled — this is real, not simulated, behavior.
  const badge = page.locator('#scanner-badge');
  await expect(badge).toHaveText(/not connected/i, { timeout: 10_000 });
  await expect(page.locator('#start-scan-btn')).toBeDisabled();

  // Seed data: two scans should already be listed.
  const jobList = page.locator('#job-list');
  await expect(jobList.getByText('Q3 Invoice — Rivertown Supply')).toBeVisible();
  await expect(jobList.getByText('Cover Letter Draft')).toBeVisible();

  // Open the invoice scan (2 pages).
  await jobList.getByText('Q3 Invoice — Rivertown Supply').click();
  const grid = page.locator('#page-grid');
  await expect(grid.locator('.page-tile')).toHaveCount(2);

  // Reorder: drag page 2 in front of page 1, then persist via the
  // PATCH /pages/order call the UI fires on drop.
  const [reorderResp] = await Promise.all([
    page.waitForResponse((r) => r.url().includes('/pages/order') && r.request().method() === 'PATCH'),
    dragReorder(page, grid.locator('.page-tile[data-page="2"]'), grid.locator('.page-tile[data-page="1"]')),
  ]);
  expect(reorderResp.ok()).toBeTruthy();
  await expect(grid.locator('.page-tile').first()).toHaveAttribute('data-page', '2');

  // Open the (now first) tile, rotate it, then crop it.
  await grid.locator('.page-tile').first().locator('.page-thumb').click();
  const modal = page.locator('#page-modal');
  await expect(modal).toBeVisible();
  await expect(page.locator('#page-modal-image')).toBeVisible();

  await page.locator('#pm-rotate-right').click();
  await expect(page.locator('#busy-overlay')).toBeHidden();

  await page.locator('#pm-crop-start').click();
  await expect(page.locator('#page-modal-crop-actions')).toBeVisible();
  await page.waitForTimeout(500); // let the crop handles render for the recording
  await page.locator('#pm-crop-save').click();
  await expect(page.locator('#page-modal-view-actions')).toBeVisible();

  // Select this page for combine, then close the viewer. #pm-combine-check
  // is a Bootstrap btn-check (visually hidden; its <label> is the actual
  // clickable control), so click the label rather than the input itself.
  await page.locator('label[for="pm-combine-check"]').click();
  await expect(page.locator('#pm-combine-check')).toBeChecked();
  await page.locator('#page-modal .btn-close').click();
  await expect(modal).toBeHidden();

  // Select the other scan's page for combine too — pages from two
  // different jobs, both originally "page 1" in their own scan.
  await jobList.getByText('Cover Letter Draft').click();
  await expect(grid.locator('.page-tile')).toHaveCount(1);
  await grid.locator('.page-tile .combine-check').check();

  const combineBar = page.locator('#combine-bar');
  await expect(combineBar).toBeVisible();
  await expect(page.locator('#combine-count')).toHaveText('2');
  const thumbs = page.locator('.combine-thumb');
  await expect(thumbs).toHaveCount(2);

  // Reorder by dragging the second thumbnail (the letter page) in front
  // of the first (the invoice page) — confirm the actual image swapped
  // position, not just that the sequential 1/2 labels are still there.
  const secondThumbSrc = await thumbs.nth(1).locator('img').getAttribute('src');
  await dragReorder(page, thumbs.nth(1), thumbs.nth(0));
  await expect(thumbs.first().locator('img')).toHaveAttribute('src', secondThumbSrc);

  // Click a thumbnail for a view-only preview — no editing actions shown.
  await thumbs.first().locator('img').click();
  await expect(modal).toBeVisible();
  await expect(page.locator('#page-modal-view-actions')).toBeHidden();
  await page.locator('#page-modal .btn-close').click();
  await expect(modal).toBeHidden();

  // Remove the invoice page from the selection via its thumbnail's X
  // button (it's now second, after the reorder above).
  await thumbs.nth(1).locator('.combine-thumb-remove').click();
  await expect(thumbs).toHaveCount(1);

  // Re-select it from the invoice scan's page grid, then combine both
  // pages into one PDF.
  await jobList.getByText('Q3 Invoice — Rivertown Supply').click();
  await grid.locator('.page-tile[data-page="2"] .combine-check').check();
  await expect(thumbs).toHaveCount(2);
  await page.locator('#combine-title').fill('Demo Combined');
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.locator('#combine-btn').click(),
  ]);
  expect(download.suggestedFilename()).toMatch(/\.pdf$/);

  await page.locator('#combine-clear-btn').click();
  await expect(combineBar).toBeHidden();

  // Rename the currently-open scan.
  await page.locator('#job-name-input').fill('Q3 Invoice (renamed)');
  await page.locator('#job-rename-btn').click();
  await expect(jobList.getByText('Q3 Invoice (renamed)')).toBeVisible();

  // Delete one page from it and confirm the page count drops.
  await grid.locator('.page-tile').first().locator('.page-thumb').click();
  page.once('dialog', (d) => d.accept());
  await page.locator('#pm-delete').click();
  await expect(modal).toBeHidden();
  await expect(grid.locator('.page-tile')).toHaveCount(1);
});
